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
	"github.com/xianxu/charon/internal/providers/anthropic"
	"github.com/xianxu/charon/internal/providers/openai"
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
	root.AddCommand(scopesCmd())
	root.AddCommand(permissionsCmd())

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
	return &cobra.Command{
		Use:   "auth",
		Short: "Manage credentials via the scope-management TUI",
		Long: `Launches an interactive TUI for managing OAuth credentials.

The TUI shows existing accounts, lets you grant or revoke individual
scopes, and walks you through OAuth for new accounts. Replaces the
older 'auth google ...' subcommands (scopes, grant, fix).

Headless removal: 'charon vault delete --provider X --account Y'.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			gp, err := oauth.NewGoogleProvider()
			if err != nil {
				return fmt.Errorf("init google provider: %w", err)
			}
			gp.Output = io.Discard // suppress oauth status prints inside TUI
			// Admin-key providers (#13). Wired even when no admin key
			// has been configured yet — the TUI shows them with the
			// red ○ glyph until the user pastes an admin key.
			openaiProv := openai.New()
			anthropicProv := anthropic.New()
			return tui.Run(newVault(), "", listenAddr, gp, openaiProv, anthropicProv)
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

func permissionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "permissions [provider] [account]",
		Short: "Print granted scopes per provider and account (JSON)",
		Long: `Outputs granted scopes from the keychain as JSON. Variants:

  charon permissions
    All providers, all accounts. Shape:
      {"google":{"a@gmail.com":[...],"b@gmail.com":[...]}, ...}

  charon permissions <provider>
    One provider, all accounts. Shape:
      {"a@gmail.com":[...],"b@gmail.com":[...]}

  charon permissions <provider> <account>
    Exact account. Shape:
      ["openid","https://...userinfo.email","https://...gmail.readonly"]

Each scope string is in the form charon stores it (typically the full
URL the provider issued tokens against).

Loading per-credential data triggers one keychain access per account,
which may prompt for permission on the first access and is slower
than 'charon accounts'. Use 'charon accounts' if you only need the
account list without scopes.`,
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			payload, err := permissionsPayload(newVault(), args)
			if err != nil {
				return err
			}
			b, err := json.MarshalIndent(payload, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return nil
		},
	}
}

// permissionsPayload builds the JSON-shaped value for `charon permissions`,
// scoped to the given args. Pure function — vault is the only IO.
//
//	args=[]                  → map[provider]map[account][]scopes
//	args=[provider]          → map[account][]scopes
//	args=[provider account]  → []scopes
func permissionsPayload(v vault.Store, args []string) (any, error) {
	summaries, err := v.List()
	if err != nil {
		return nil, err
	}

	byProvider := map[string]map[string][]string{}
	for _, c := range summaries {
		if len(args) >= 1 && c.Provider != args[0] {
			continue
		}
		if len(args) >= 2 && c.Account != args[1] {
			continue
		}
		cred, err := v.Get(c.Provider, c.Account)
		if err != nil {
			// Skip individual failures; partial output is more useful than
			// none. Common cause: keychain entry exists but read denied.
			continue
		}
		if _, ok := byProvider[c.Provider]; !ok {
			byProvider[c.Provider] = map[string][]string{}
		}
		scopes := cred.Scopes
		if scopes == nil {
			scopes = []string{}
		}
		byProvider[c.Provider][c.Account] = scopes
	}

	switch len(args) {
	case 0:
		return byProvider, nil
	case 1:
		accounts := byProvider[args[0]]
		if accounts == nil {
			accounts = map[string][]string{}
		}
		return accounts, nil
	default: // 2
		if accounts, ok := byProvider[args[0]]; ok {
			if scopes, ok := accounts[args[1]]; ok {
				return scopes, nil
			}
		}
		return nil, fmt.Errorf("no credential for %s/%s", args[0], args[1])
	}
}

// providerCatalogs maps each supported OAuth provider to its scope
// catalog. Update when a new provider is wired up.
var providerCatalogs = map[string][]oauth.ScopeInfo{
	"google": oauth.GoogleScopeCatalog,
}

func scopesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "scopes",
		Short: "Print scope catalogs for all supported providers (JSON)",
		Long: `Outputs a JSON object keyed by provider name, with each provider's
full scope catalog (short names, full URLs, descriptions,
required-flag).

Intended for agent introspection. The output is just the catalog
(what's possible) — not what any specific account has granted.
Agents declare intent via X-Charon-Scope and let charon's 407 response
signal what's missing for the user to grant.

Examples:
  charon scopes                              # full snapshot, all providers
  charon scopes | jq 'keys'                  # list providers
  charon scopes | jq '.google[] | select(.short | startswith("gmail"))'`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := json.MarshalIndent(providerCatalogs, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(out))
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

