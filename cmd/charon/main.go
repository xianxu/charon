package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/xianxu/charon/internal/oauth"
	"github.com/xianxu/charon/internal/proxy"
	"github.com/xianxu/charon/internal/vault"
	"github.com/xianxu/charon/internal/vault/keychain"
)

var (
	listenAddr string
	auditPath  string
	configDir  string
	verbose    bool
)

func defaultConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "charon")
}

func main() {
	root := &cobra.Command{
		Use:   "charon",
		Short: "Credential proxy for AI agents",
		Long:  "Charon is a credential proxy that injects OAuth tokens into HTTPS requests, keeping tokens invisible to AI agents.",
	}

	root.PersistentFlags().StringVar(&configDir, "config-dir", defaultConfigDir(), "configuration directory")
	root.PersistentFlags().StringVar(&listenAddr, "addr", "127.0.0.1:8230", "proxy listen address")

	root.AddCommand(serveCmd())
	root.AddCommand(runCmd())
	root.AddCommand(authCmd())
	root.AddCommand(accountsCmd())
	root.AddCommand(statusCmd())
	root.AddCommand(vaultCmd())

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
			ca, err := proxy.LoadOrCreateCA(configDir)
			if err != nil {
				return fmt.Errorf("failed to init CA: %w", err)
			}
			log.Printf("CA cert: %s/ca.pem", configDir)

			bundlePath, err := proxy.BuildCABundle(configDir, ca.CertPEM)
			if err != nil {
				log.Printf("warning: could not build CA bundle: %v", err)
			} else {
				log.Printf("CA bundle: %s", bundlePath)
			}

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
				Vault:      newVault(),
				Audit:      audit,
				Addr:       listenAddr,
				CA:         ca,
				Refreshers: refreshers,
				Verbose:    verbose,
			}
			return srv.ListenAndServe()
		},
	}
	cmd.Flags().StringVar(&auditPath, "audit-log", "", "audit log path (default: <config-dir>/audit.log)")
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
  charon run -- curl https://gmail.googleapis.com/gmail/v1/users/me/profile`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Strip leading "--" if present (cobra passes it through with ArgsOnly).
			if args[0] == "--" {
				args = args[1:]
			}
			if len(args) == 0 {
				return fmt.Errorf("usage: charon run -- <command> [args...]")
			}

			// Check proxy is running.
			proxyURL := fmt.Sprintf("http://%s", listenAddr)
			resp, err := http.Get(proxyURL + "/healthz")
			if err != nil {
				return fmt.Errorf("proxy not reachable at %s — is 'charon serve' running?\n  %w", listenAddr, err)
			}
			resp.Body.Close()

			// Locate CA bundle.
			bundlePath := filepath.Join(configDir, "ca-bundle.pem")
			caPath := filepath.Join(configDir, "ca.pem")
			if _, err := os.Stat(bundlePath); err != nil {
				return fmt.Errorf("CA bundle not found at %s — run 'charon serve' first", bundlePath)
			}

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
			env = setEnv(env, "REQUESTS_CA_BUNDLE", bundlePath)     // Python requests
			env = setEnv(env, "CURL_CA_BUNDLE", bundlePath)          // curl
			env = setEnv(env, "NODE_EXTRA_CA_CERTS", caPath)         // Node.js (additive)
			env = setEnv(env, "GRPC_DEFAULT_SSL_ROOTS_FILE_PATH", bundlePath) // gRPC

			fmt.Fprintf(os.Stderr, "charon: proxying through %s\n", listenAddr)

			// Exec replaces this process with the child.
			return syscall.Exec(binary, args, env)
		},
	}
	return cmd
}

func authCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Authenticate with a service provider",
	}
	cmd.AddCommand(authGoogleCmd())
	return cmd
}

func authGoogleCmd() *cobra.Command {
	var scopes []string
	cmd := &cobra.Command{
		Use:   "google <account-email>",
		Short: "Authenticate a Google account via OAuth",
		Long: `Runs the Google OAuth 2.0 flow: opens a browser for authorization,
then stores the tokens in the OS keychain.

Example:
  charon auth google user@gmail.com
  charon auth google user@gmail.com --scope https://www.googleapis.com/auth/calendar.readonly`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			account := args[0]
			out := cmd.OutOrStdout()

			gp, err := oauth.NewGoogleProvider()
			if err != nil {
				return err
			}

			// Check for existing scopes for incremental auth.
			v := newVault()
			var existingScopes []string
			if existing, err := v.Get("google", account); err == nil {
				existingScopes = existing.Scopes
			}

			fmt.Fprintf(out, "Authenticating %s with Google...\n", account)
			cred, err := gp.Auth(account, scopes, existingScopes)
			if err != nil {
				return err
			}

			if err := v.Set(cred); err != nil {
				return fmt.Errorf("failed to store credential: %w", err)
			}

			notifyProxyCacheClear()
			fmt.Fprintf(out, "Authenticated %s (scopes: %v)\n", account, cred.Scopes)
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&scopes, "scope", nil, "OAuth scopes (default: gmail.readonly)")
	return cmd
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
				fmt.Fprintf(out, "  %s / %s\n", c.Provider, c.Account)
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

			// Show CA info.
			caPath := filepath.Join(configDir, "ca.pem")
			if _, err := os.Stat(caPath); err == nil {
				fmt.Fprintf(out, "CA cert: %s\n", caPath)
			}
			bundlePath := filepath.Join(configDir, "ca-bundle.pem")
			if _, err := os.Stat(bundlePath); err == nil {
				fmt.Fprintf(out, "CA bundle: %s\n", bundlePath)
			}

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
