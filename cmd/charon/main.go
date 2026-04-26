package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/xianxu/charon/internal/oauth"
	"github.com/xianxu/charon/internal/proxy"
	"github.com/xianxu/charon/internal/service"
	"github.com/xianxu/charon/internal/tui"
	"github.com/xianxu/charon/internal/vault"
	"github.com/xianxu/charon/internal/vault/keychain"
)

var (
	listenAddr string
	auditPath  string
	verbose    bool
)

func main() {
	root := &cobra.Command{
		Use:   "charon",
		Short: "Credential proxy for AI agents",
		Long:  "Charon is a credential proxy that injects OAuth tokens into HTTPS requests, keeping tokens invisible to AI agents.",
	}

	root.PersistentFlags().StringVar(&listenAddr, "addr", "127.0.0.1:8230", "proxy listen address")

	root.AddCommand(serveCmd())
	root.AddCommand(runCmd())
	root.AddCommand(authCmd())
	root.AddCommand(accountsCmd())
	root.AddCommand(statusCmd())
	root.AddCommand(serviceCmd())
	root.AddCommand(vaultCmd())
	root.AddCommand(tuiCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func newVault() vault.Store {
	return keychain.New()
}

func serveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the HTTPS credential proxy",
		RunE: func(cmd *cobra.Command, args []string) error {
			ca, err := proxy.LoadOrCreateCA()
			if err != nil {
				return fmt.Errorf("failed to init CA: %w", err)
			}
			log.Printf("CA loaded from keychain")

			bundlePath, cleanup, err := proxy.BuildCABundle(ca.CertPEM)
			if err != nil {
				return fmt.Errorf("failed to build CA bundle: %w", err)
			}
			defer cleanup()
			log.Printf("CA bundle: %s", bundlePath)

			audit, err := proxy.NewAuditLog(auditPath)
			if err != nil {
				return fmt.Errorf("failed to init audit log: %w", err)
			}
			defer audit.Close()

			refreshers := make(map[string]proxy.Refresher)
			if gp, err := oauth.NewGoogleProvider(); err == nil {
				refreshers["google"] = gp
			} else {
				log.Printf("warning: Google OAuth not available: %v", err)
			}

			srv := &proxy.Server{
				Vault:        newVault(),
				Audit:        audit,
				Addr:         listenAddr,
				CA:           ca,
				Refreshers:   refreshers,
				Verbose:      verbose,
				ScopeTracker: proxy.NewScopeTracker(100, 24*time.Hour),
			}
			return srv.ListenAndServe()
		},
	}
	cmd.Flags().StringVar(&auditPath, "audit-log", "", "audit log file path (default: stderr)")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "enable debug logging")
	return cmd
}

func runCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run -- <command> [args...]",
		Short: "Run a command with proxy environment configured",
		Long: `Launches a child process with HTTPS_PROXY and CA trust environment
variables set so that all HTTPS traffic is routed through Charon.

The proxy must already be running (charon serve).

Example:
  charon run -- python my_agent.py
  charon run -- curl https://gmail.googleapis.com/gmail/v1/users/me/profile

Without arguments, prints the proxy environment variables for debugging.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Strip leading "--" if present.
			if len(args) > 0 && args[0] == "--" {
				args = args[1:]
			}

			// No args: print proxy info for debugging.
			if len(args) == 0 {
				return printProxyInfo(cmd)
			}

			// Check proxy is running and fetch CA cert.
			proxyURL := fmt.Sprintf("http://%s", listenAddr)
			resp, err := http.Get(proxyURL + "/ca.pem")
			if err != nil {
				return fmt.Errorf("proxy not reachable at %s — is 'charon serve' running?\n  %w", listenAddr, err)
			}
			caPEM, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 200 || len(caPEM) == 0 {
				return fmt.Errorf("could not fetch CA cert from proxy")
			}

			// Build ephemeral CA bundle.
			bundlePath, _, err := proxy.BuildCABundle(caPEM)
			if err != nil {
				return fmt.Errorf("failed to build CA bundle: %w", err)
			}
			// No cleanup — syscall.Exec replaces this process, OS cleans up on exit.
			// The temp dir will be cleaned on next boot or by OS temp cleanup.
			caPath := proxy.CAPathFromBundle(bundlePath)

			// Resolve command path.
			binary, err := exec.LookPath(args[0])
			if err != nil {
				return fmt.Errorf("command not found: %s", args[0])
			}

			// Build environment with proxy and CA trust vars.
			env := os.Environ()
			env = setEnv(env, "HTTPS_PROXY", proxyURL)
			env = setEnv(env, "HTTP_PROXY", proxyURL)
			env = setEnv(env, "SSL_CERT_FILE", bundlePath)
			env = setEnv(env, "REQUESTS_CA_BUNDLE", bundlePath)                // Python requests
			env = setEnv(env, "CURL_CA_BUNDLE", bundlePath)                    // curl
			env = setEnv(env, "NODE_EXTRA_CA_CERTS", caPath)                   // Node.js (additive)
			env = setEnv(env, "GRPC_DEFAULT_SSL_ROOTS_FILE_PATH", bundlePath)  // gRPC

			fmt.Fprintf(os.Stderr, "charon: proxying through %s\n", listenAddr)

			// Exec replaces this process with the child.
			return syscall.Exec(binary, args, env)
		},
	}
	return cmd
}

func printProxyInfo(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()
	proxyURL := fmt.Sprintf("http://%s", listenAddr)

	// Check if proxy is running.
	resp, err := http.Get(proxyURL + "/healthz")
	if err != nil {
		fmt.Fprintf(out, "Proxy: not running (cannot reach %s)\n", listenAddr)
		return nil
	}
	resp.Body.Close()

	fmt.Fprintf(out, "Proxy: %s\n", proxyURL)
	fmt.Fprintf(out, "\nEnvironment variables set by 'charon run':\n")
	fmt.Fprintf(out, "  HTTPS_PROXY=%s\n", proxyURL)
	fmt.Fprintf(out, "  HTTP_PROXY=%s\n", proxyURL)
	fmt.Fprintf(out, "  SSL_CERT_FILE=<temp>/ca-bundle.pem\n")
	fmt.Fprintf(out, "  REQUESTS_CA_BUNDLE=<temp>/ca-bundle.pem\n")
	fmt.Fprintf(out, "  CURL_CA_BUNDLE=<temp>/ca-bundle.pem\n")
	fmt.Fprintf(out, "  NODE_EXTRA_CA_CERTS=<temp>/ca.pem\n")
	fmt.Fprintf(out, "  GRPC_DEFAULT_SSL_ROOTS_FILE_PATH=<temp>/ca-bundle.pem\n")
	fmt.Fprintf(out, "\nUsage:\n")
	fmt.Fprintf(out, "  charon run -- <command> [args...]\n")
	fmt.Fprintf(out, "\nExamples:\n")
	fmt.Fprintf(out, "  charon run -- curl -s https://gmail.googleapis.com/gmail/v1/users/me/profile\n")
	fmt.Fprintf(out, "  charon run -- python my_agent.py\n")
	fmt.Fprintf(out, "  charon run -- gmail search \"from:alice subject:invoice\"\n")
	return nil
}

func serviceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Manage charon as an OS service",
	}
	cmd.AddCommand(serviceInstallCmd())
	cmd.AddCommand(serviceUninstallCmd())
	cmd.AddCommand(serviceStartCmd())
	cmd.AddCommand(serviceStopCmd())
	cmd.AddCommand(serviceStatusCmd())
	return cmd
}

func serviceInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install charon as a system service (starts on login)",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := service.New()
			if err != nil {
				return err
			}
			binary, err := os.Executable()
			if err != nil {
				return fmt.Errorf("could not determine binary path: %w", err)
			}
			serveArgs := []string{"serve"}
			if listenAddr != "127.0.0.1:8230" {
				serveArgs = append(serveArgs, "--addr", listenAddr)
			}
			if err := mgr.Install(binary, serveArgs); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Service installed and started.\n")
			return nil
		},
	}
}

func serviceUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Uninstall charon system service",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := service.New()
			if err != nil {
				return err
			}
			if err := mgr.Uninstall(); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Service uninstalled.\n")
			return nil
		},
	}
}

func serviceStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the charon service",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := service.New()
			if err != nil {
				return err
			}
			if err := mgr.Start(); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Service started.\n")
			return nil
		},
	}
}

func serviceStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the charon service",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := service.New()
			if err != nil {
				return err
			}
			if err := mgr.Stop(); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Service stopped.\n")
			return nil
		},
	}
}

func serviceStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show charon service status",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := service.New()
			if err != nil {
				return err
			}
			status, err := mgr.Status()
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Service: %s\n", status)
			return nil
		},
	}
}

func authCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Authenticate with a service provider",
	}
	cmd.AddCommand(authGoogleCmd())
	cmd.AddCommand(authRemoveCmd())
	cmd.AddCommand(authFixCmd())
	return cmd
}

func authGoogleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "google [account-email]",
		Short: "Authenticate a Google account via OAuth",
		Long: `Runs the Google OAuth 2.0 flow: opens a browser for authorization,
then stores the tokens in the OS keychain.

If no email is provided, the account is detected from the OAuth response.
If an email is provided, it's used as a login hint to pre-select the account.

Example:
  charon auth google
  charon auth google user@gmail.com`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var account string
			if len(args) > 0 {
				account = args[0]
			}
			out := cmd.OutOrStdout()

			gp, err := oauth.NewGoogleProvider()
			if err != nil {
				return err
			}

			// Check for existing scopes for incremental auth.
			v := newVault()
			var existingScopes []string
			if account != "" {
				if existing, err := v.Get("google", account); err == nil {
					existingScopes = existing.Scopes
				}
			}

			if account != "" {
				fmt.Fprintf(out, "Authenticating %s with Google...\n", account)
			} else {
				fmt.Fprintf(out, "Authenticating with Google...\n")
			}
			cred, err := gp.Auth(account, nil, existingScopes)
			if err != nil {
				return err
			}

			if err := v.Set(cred); err != nil {
				return fmt.Errorf("failed to store credential: %w", err)
			}

			notifyProxyCacheClear()
			fmt.Fprintf(out, "Authenticated %s (scopes: %v)\n", cred.Account, cred.Scopes)
			return nil
		},
	}
	cmd.AddCommand(authGoogleScopesCmd())
	cmd.AddCommand(authGoogleGrantCmd())
	cmd.AddCommand(authGoogleFixCmd())
	return cmd
}

func authGoogleScopesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "scopes [account-email]",
		Short: "List Google OAuth scopes",
		Long: `Without an account email, shows the catalog of known Google scopes.
With an account email, shows granted scopes for that account.

Example:
  charon auth google scopes
  charon auth google scopes user@gmail.com`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			if len(args) == 0 {
				// Show scope catalog.
				fmt.Fprintln(out, "Known Google OAuth scopes:")
				fmt.Fprintln(out)
				for _, s := range oauth.GoogleScopeCatalog {
					fmt.Fprintf(out, "  %-25s %s\n", s.Short, s.Description)
				}
				fmt.Fprintln(out)
				fmt.Fprintln(out, "Use full scope URLs or short names with 'charon auth google grant'.")
				return nil
			}

			// Show granted scopes for account.
			account := args[0]
			v := newVault()
			cred, err := v.Get("google", account)
			if err != nil {
				return fmt.Errorf("no credentials found for google/%s", account)
			}

			if len(cred.Scopes) == 0 {
				fmt.Fprintf(out, "google / %s: no scopes recorded\n", account)
				return nil
			}

			fmt.Fprintf(out, "google / %s:\n", account)
			fmt.Fprintln(out)
			fmt.Fprintln(out, "  Granted:")
			for _, scope := range cred.Scopes {
				info := oauth.LookupGoogleScope(scope)
				if info != nil {
					fmt.Fprintf(out, "    %-25s %s\n", info.Short, info.Description)
				} else {
					fmt.Fprintf(out, "    %s\n", scope)
				}
			}
			return nil
		},
	}
}

func authGoogleGrantCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "grant <account-email> <scope> [scope...]",
		Short: "Grant additional scopes to a Google account",
		Long: `Triggers an incremental OAuth flow to add scopes to an existing account.
Scopes can be specified as short names or full URLs.

Example:
  charon auth google grant user@gmail.com calendar.readonly
  charon auth google grant user@gmail.com calendar.readonly drive.readonly`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			account := args[0]
			out := cmd.OutOrStdout()

			// Resolve short scope names to full URLs.
			var scopes []string
			for _, s := range args[1:] {
				scopes = append(scopes, oauth.ResolveGoogleScope(s))
			}

			gp, err := oauth.NewGoogleProvider()
			if err != nil {
				return err
			}

			v := newVault()
			var existingScopes []string
			if existing, err := v.Get("google", account); err == nil {
				existingScopes = existing.Scopes
			}

			fmt.Fprintf(out, "Granting scopes to %s: %v\n", account, scopes)
			cred, err := gp.Auth(account, scopes, existingScopes)
			if err != nil {
				return err
			}

			if err := v.Set(cred); err != nil {
				return fmt.Errorf("failed to store credential: %w", err)
			}

			notifyProxyCacheClear()
			fmt.Fprintf(out, "Authenticated %s (scopes: %v)\n", cred.Account, cred.Scopes)
			return nil
		},
	}
}

func authGoogleFixCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "fix [account-email]",
		Short: "Grant missing scopes detected by the proxy",
		Long: `Shows scopes that were requested by callers but not granted, and offers
to grant them via an incremental OAuth flow.

Example:
  charon auth google fix
  charon auth google fix user@gmail.com`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			// Query the running proxy for scope denials.
			var url string
			if len(args) > 0 {
				url = fmt.Sprintf("http://%s/scopes/denied?provider=google&account=%s", listenAddr, args[0])
			} else {
				url = fmt.Sprintf("http://%s/scopes/denied?provider=google", listenAddr)
			}

			resp, err := http.Get(url)
			if err != nil {
				return fmt.Errorf("proxy not reachable at %s — is 'charon serve' running?", listenAddr)
			}
			defer resp.Body.Close()

			var denials []struct {
				Provider  string `json:"provider"`
				Account   string `json:"account"`
				Scope     string `json:"scope"`
				Count     int    `json:"count"`
				LastSeen  string `json:"last_seen"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&denials); err != nil {
				return fmt.Errorf("failed to parse proxy response: %w", err)
			}

			if len(denials) == 0 {
				fmt.Fprintln(out, "No missing scopes detected.")
				return nil
			}

			// Group by account.
			byAccount := make(map[string][]string)
			for _, d := range denials {
				byAccount[d.Account] = append(byAccount[d.Account], d.Scope)
			}

			gp, err := oauth.NewGoogleProvider()
			if err != nil {
				return err
			}
			v := newVault()

			i := 0
			total := len(byAccount)
			for account, scopes := range byAccount {
				i++
				fmt.Fprintf(out, "\n[%d/%d] google / %s (%d scopes: %s)\n",
					i, total, account, len(scopes), strings.Join(scopes, ", "))
				fmt.Fprintf(out, "Grant? [Y/n] ")

				var answer string
				fmt.Scanln(&answer)
				if answer != "" && answer != "y" && answer != "Y" {
					fmt.Fprintln(out, "Skipped.")
					continue
				}

				var existingScopes []string
				if existing, err := v.Get("google", account); err == nil {
					existingScopes = existing.Scopes
				}

				cred, err := gp.Auth(account, scopes, existingScopes)
				if err != nil {
					fmt.Fprintf(out, "Error: %v\n", err)
					continue
				}
				if err := v.Set(cred); err != nil {
					fmt.Fprintf(out, "Error storing credential: %v\n", err)
					continue
				}
				notifyProxyCacheClear()
				fmt.Fprintf(out, "Granted. Scopes: %v\n", cred.Scopes)
			}
			return nil
		},
	}
}

func authFixCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "fix",
		Short: "Grant missing scopes across all providers",
		Long: `Shows scopes that were requested by callers but not granted across all
providers and accounts, and offers to grant them sequentially.

Example:
  charon auth fix`,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			resp, err := http.Get(fmt.Sprintf("http://%s/scopes/denied", listenAddr))
			if err != nil {
				return fmt.Errorf("proxy not reachable at %s — is 'charon serve' running?", listenAddr)
			}
			defer resp.Body.Close()

			var denials []struct {
				Provider string `json:"provider"`
				Account  string `json:"account"`
				Scope    string `json:"scope"`
				Count    int    `json:"count"`
				LastSeen string `json:"last_seen"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&denials); err != nil {
				return fmt.Errorf("failed to parse proxy response: %w", err)
			}

			if len(denials) == 0 {
				fmt.Fprintln(out, "No missing scopes detected.")
				return nil
			}

			// Group by provider:account.
			type key struct{ provider, account string }
			byKey := make(map[key][]string)
			var order []key
			for _, d := range denials {
				k := key{d.Provider, d.Account}
				if _, exists := byKey[k]; !exists {
					order = append(order, k)
				}
				byKey[k] = append(byKey[k], d.Scope)
			}

			fmt.Fprintf(out, "Missing scopes detected (%d provider/account pairs):\n", len(order))

			for i, k := range order {
				scopes := byKey[k]
				fmt.Fprintf(out, "\n[%d/%d] %s / %s (%d scopes: %s)\n",
					i+1, len(order), k.provider, k.account, len(scopes), strings.Join(scopes, ", "))

				if k.provider != "google" {
					fmt.Fprintf(out, "  Provider %q not yet supported for auto-fix.\n", k.provider)
					continue
				}

				fmt.Fprintf(out, "Grant? [Y/n] ")
				var answer string
				fmt.Scanln(&answer)
				if answer != "" && answer != "y" && answer != "Y" {
					fmt.Fprintln(out, "Skipped.")
					continue
				}

				gp, err := oauth.NewGoogleProvider()
				if err != nil {
					fmt.Fprintf(out, "Error: %v\n", err)
					continue
				}
				v := newVault()
				var existingScopes []string
				if existing, err := v.Get("google", k.account); err == nil {
					existingScopes = existing.Scopes
				}

				cred, err := gp.Auth(k.account, scopes, existingScopes)
				if err != nil {
					fmt.Fprintf(out, "Error: %v\n", err)
					continue
				}
				if err := v.Set(cred); err != nil {
					fmt.Fprintf(out, "Error storing credential: %v\n", err)
					continue
				}
				notifyProxyCacheClear()
				fmt.Fprintf(out, "Granted. Scopes: %v\n", cred.Scopes)
			}
			return nil
		},
	}
}

func authRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <provider> <account>",
		Short: "Remove stored credentials for an account",
		Long: `Removes OAuth tokens from the keychain for the given provider and account.

Example:
  charon auth remove google user@gmail.com`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			provider, account := args[0], args[1]
			v := newVault()
			if err := v.Delete(provider, account); err != nil {
				return fmt.Errorf("failed to remove credential: %w", err)
			}
			notifyProxyCacheClear()
			fmt.Fprintf(cmd.OutOrStdout(), "Removed credential for %s/%s\n", provider, account)
			return nil
		},
	}
}

// setEnv sets a key=value in an env slice, replacing if exists.
func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

// notifyProxyCacheClear tells a running proxy to clear its credential cache.
// Best-effort — if proxy isn't running, silently ignored.
func notifyProxyCacheClear() {
	resp, err := http.Post(fmt.Sprintf("http://%s/cache/clear", listenAddr), "", nil)
	if err != nil {
		return // proxy not running, fine
	}
	resp.Body.Close()
}

func accountsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "accounts",
		Short: "List stored credential accounts",
		RunE: func(cmd *cobra.Command, args []string) error {
			v := newVault()
			creds, err := v.List()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(creds) == 0 {
				fmt.Fprintln(out, "No accounts stored.")
				return nil
			}
			for _, c := range creds {
				if len(c.Scopes) > 0 {
					fmt.Fprintf(out, "  %s / %s (scopes: %s)\n", c.Provider, c.Account, strings.Join(c.Scopes, ", "))
				} else {
					fmt.Fprintf(out, "  %s / %s\n", c.Provider, c.Account)
				}
			}
			return nil
		},
	}
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show proxy status",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			proxyURL := fmt.Sprintf("http://%s/healthz", listenAddr)
			resp, err := http.Get(proxyURL)
			if err != nil {
				fmt.Fprintf(out, "Proxy: not running (cannot reach %s)\n", listenAddr)
				return nil
			}
			defer resp.Body.Close()

			var health struct {
				Status string `json:"status"`
				Addr   string `json:"addr"`
			}
			json.NewDecoder(resp.Body).Decode(&health)
			fmt.Fprintf(out, "Proxy: %s on %s\n", health.Status, health.Addr)
			fmt.Fprintf(out, "CA: stored in keychain (service: charon)\n")
			return nil
		},
	}
}

func vaultCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vault",
		Short: "Manage credentials in the vault",
	}
	cmd.AddCommand(vaultSetCmd())
	cmd.AddCommand(vaultDeleteCmd())
	return cmd
}

func vaultSetCmd() *cobra.Command {
	var provider, account, token, ttl string
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Manually store a token (for testing)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if provider == "" || account == "" || token == "" {
				return fmt.Errorf("--provider, --account, and --token are required")
			}
			cred := &vault.Credential{
				Provider:    provider,
				Account:     account,
				AccessToken: token,
			}
			if ttl != "" {
				d, err := time.ParseDuration(ttl)
				if err != nil {
					return fmt.Errorf("invalid --ttl: %w", err)
				}
				cred.Expiry = time.Now().Add(d)
			}
			v := newVault()
			err := v.Set(cred)
			if err != nil {
				return err
			}
			notifyProxyCacheClear()
			fmt.Fprintf(cmd.OutOrStdout(), "Stored token for %s/%s\n", provider, account)
			return nil
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "", "credential provider (e.g. google)")
	cmd.Flags().StringVar(&account, "account", "", "account identifier (e.g. user@gmail.com)")
	cmd.Flags().StringVar(&token, "token", "", "access token")
	cmd.Flags().StringVar(&ttl, "ttl", "", "token time-to-live (e.g. 1h, 30m, 3600s). omit for no expiry")
	return cmd
}

func vaultDeleteCmd() *cobra.Command {
	var provider, account string
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Remove a credential from the vault",
		RunE: func(cmd *cobra.Command, args []string) error {
			if provider == "" || account == "" {
				return fmt.Errorf("--provider and --account are required")
			}
			v := newVault()
			if err := v.Delete(provider, account); err != nil {
				return err
			}
			notifyProxyCacheClear()
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted credential for %s/%s\n", provider, account)
			return nil
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "", "credential provider")
	cmd.Flags().StringVar(&account, "account", "", "account identifier")
	return cmd
}

func tuiCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "tui [account-email]",
		Short:  "Launch the scope-management TUI (work in progress, see #000005)",
		Hidden: true,
		Args:   cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var account string
			if len(args) > 0 {
				account = args[0]
			}
			gp, err := oauth.NewGoogleProvider()
			if err != nil {
				return fmt.Errorf("init google provider: %w", err)
			}
			gp.Output = io.Discard // suppress oauth status prints inside TUI
			return tui.Run(newVault(), account, listenAddr, gp)
		},
	}
}
