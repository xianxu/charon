package main

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/xianxu/charon/internal/proxy"
)

// executeCmd runs a cobra command with args and returns stdout, stderr, and error.
func executeCmd(root *cobra.Command, args ...string) (stdout, stderr string, err error) {
	outBuf := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	root.SetOut(outBuf)
	root.SetErr(errBuf)
	root.SetArgs(args)
	err = root.Execute()
	return outBuf.String(), errBuf.String(), err
}

// buildRoot creates a fresh root command for testing (avoids global state).
func buildRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "charon",
		Short: "Credential proxy for AI agents",
	}
	root.PersistentFlags().StringVar(&configDir, "config-dir", defaultConfigDir(), "configuration directory")
	root.PersistentFlags().StringVar(&listenAddr, "addr", "127.0.0.1:8230", "proxy listen address")
	root.AddCommand(serveCmd())
	root.AddCommand(runCmd())
	root.AddCommand(accountsCmd())
	root.AddCommand(statusCmd())
	root.AddCommand(vaultCmd())
	return root
}

// startTestProxy starts a proxy server on a dynamic port and returns its address.
// The proxy is stopped when the test completes.
func startTestProxy(t *testing.T) (addr string, cfgDir string) {
	t.Helper()
	cfgDir = t.TempDir()

	ca, err := proxy.LoadOrCreateCA(cfgDir)
	if err != nil {
		t.Fatalf("failed to create CA: %v", err)
	}
	_, err = proxy.BuildCABundle(cfgDir, ca.CertPEM)
	if err != nil {
		t.Fatalf("failed to build CA bundle: %v", err)
	}

	audit := proxy.NopAuditLog()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	addr = ln.Addr().String()

	srv := &proxy.Server{
		Vault: newVault(),
		Audit: audit,
		Addr:  addr,
		CA:    ca,
	}
	httpSrv := &http.Server{Handler: srv}
	go httpSrv.Serve(ln)
	t.Cleanup(func() { httpSrv.Close() })

	return addr, cfgDir
}

func TestRootHelp(t *testing.T) {
	root := buildRoot()
	stdout, _, err := executeCmd(root, "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"serve", "run", "accounts", "status", "vault"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("root help missing subcommand %q", want)
		}
	}
}

func TestServeHelp(t *testing.T) {
	root := buildRoot()
	stdout, _, err := executeCmd(root, "serve", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--audit-log", "--addr", "--config-dir"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("serve help missing flag %q", want)
		}
	}
}

func TestServeDefaultAddr(t *testing.T) {
	root := buildRoot()
	stdout, _, err := executeCmd(root, "serve", "--help")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "127.0.0.1:8230") {
		t.Error("serve help should show default addr 127.0.0.1:8230")
	}
}

func TestRunHelp(t *testing.T) {
	root := buildRoot()
	stdout, _, err := executeCmd(root, "run", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"HTTPS_PROXY", "charon serve", "python", "curl"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("run help missing %q", want)
		}
	}
}

func TestRunRequiresArgs(t *testing.T) {
	root := buildRoot()
	_, _, err := executeCmd(root, "run")
	if err == nil {
		t.Error("expected error when run has no args")
	}
}

func TestRunRequiresProxy(t *testing.T) {
	root := buildRoot()
	_, _, err := executeCmd(root, "--addr", "127.0.0.1:19999", "run", "--", "echo", "hi")
	if err == nil {
		t.Error("expected error when proxy not running")
	}
	if err != nil && !strings.Contains(err.Error(), "not reachable") {
		t.Errorf("expected 'not reachable' error, got: %v", err)
	}
}

func TestRunRequiresCABundle(t *testing.T) {
	addr, _ := startTestProxy(t)

	// Run with a different config-dir that has no CA bundle.
	emptyDir := t.TempDir()
	root := buildRoot()
	_, _, err := executeCmd(root, "--addr", addr, "--config-dir", emptyDir, "run", "--", "echo", "hi")
	if err == nil {
		t.Error("expected error about missing CA bundle")
	}
	if err != nil && !strings.Contains(err.Error(), "CA bundle not found") {
		t.Errorf("expected 'CA bundle not found' error, got: %v", err)
	}
}

func TestVaultSetHelp(t *testing.T) {
	root := buildRoot()
	stdout, _, err := executeCmd(root, "vault", "set", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--provider", "--account", "--token", "--ttl"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("vault set help missing flag %q", want)
		}
	}
}

func TestVaultSetRequiresAllFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"missing all", []string{"vault", "set"}},
		{"missing account and token", []string{"vault", "set", "--provider", "google"}},
		{"missing token", []string{"vault", "set", "--provider", "google", "--account", "user@gmail.com"}},
		{"missing provider", []string{"vault", "set", "--account", "user@gmail.com", "--token", "tok"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := buildRoot()
			_, _, err := executeCmd(root, tt.args...)
			if err == nil {
				t.Error("expected error for missing flags")
			}
		})
	}
}

func TestVaultDeleteHelp(t *testing.T) {
	root := buildRoot()
	stdout, _, err := executeCmd(root, "vault", "delete", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--provider", "--account"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("vault delete help missing flag %q", want)
		}
	}
}

func TestVaultDeleteRequiresFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"missing all", []string{"vault", "delete"}},
		{"missing account", []string{"vault", "delete", "--provider", "google"}},
		{"missing provider", []string{"vault", "delete", "--account", "user@gmail.com"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := buildRoot()
			_, _, err := executeCmd(root, tt.args...)
			if err == nil {
				t.Error("expected error for missing flags")
			}
		})
	}
}

func TestStatusWhenProxyNotRunning(t *testing.T) {
	root := buildRoot()
	stdout, _, err := executeCmd(root, "--addr", "127.0.0.1:19998", "status")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "not running") {
		t.Errorf("expected 'not running' message, got: %q", stdout)
	}
}

func TestStatusWhenProxyRunning(t *testing.T) {
	addr, cfgDir := startTestProxy(t)

	root := buildRoot()
	stdout, _, err := executeCmd(root, "--addr", addr, "--config-dir", cfgDir, "status")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "ok") {
		t.Errorf("expected 'ok' in status, got: %q", stdout)
	}
}

func TestServeCreatesCAFiles(t *testing.T) {
	_, cfgDir := startTestProxy(t)

	for _, name := range []string{"ca.pem", "ca-key.pem", "ca-bundle.pem"} {
		path := filepath.Join(cfgDir, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("expected %s to exist: %v", name, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("%s is empty", name)
		}
	}

	info, _ := os.Stat(filepath.Join(cfgDir, "ca-key.pem"))
	if info != nil && info.Mode().Perm() != 0600 {
		t.Errorf("ca-key.pem should be 0600, got %o", info.Mode().Perm())
	}
}

func TestServeHealthEndpointJSON(t *testing.T) {
	addr, _ := startTestProxy(t)

	resp, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var health struct {
		Status string `json:"status"`
		Addr   string `json:"addr"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if health.Status != "ok" {
		t.Errorf("expected status 'ok', got %q", health.Status)
	}
	if health.Addr != addr {
		t.Errorf("expected addr %q, got %q", addr, health.Addr)
	}
}

func TestConfigDirDefault(t *testing.T) {
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".config", "charon")
	got := defaultConfigDir()
	if got != want {
		t.Errorf("defaultConfigDir() = %q, want %q", got, want)
	}
}
