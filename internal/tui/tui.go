// Package tui implements the bubbletea-based scope management UI.
package tui

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/charon/internal/providers"
	"github.com/xianxu/charon/internal/vault"
)

// Run launches the TUI, blocking until the user exits.
//
// Required: vault. Optional: account (skips picker, implies google
// provider), addr (badges + cache-clear), auth (OAuth apply), and
// admin-key providers via WithAdminKeyProvider for OpenAI/Anthropic
// flows.
//
// gcpFactory is optional: when non-nil, the TUI offers Google Cloud
// project setup from a realized cloud-platform row in the scope
// view. When nil, that path falls back to a status hint.
func Run(v vault.Store, account, addr string, auth Authenticator, gcpFactory func(account string) (GCPSetupClient, error), adminProviders ...providers.Provider) error {
	var opts []Option
	if addr != "" {
		opts = append(opts, WithDenialFetcher(httpDenialFetcher(addr)))
		opts = append(opts, WithProxyAddr(addr))
	}
	if auth != nil {
		opts = append(opts, WithAuthenticator(auth))
	}
	if gcpFactory != nil {
		opts = append(opts, WithGCPClientFactory(gcpFactory))
	}
	for _, p := range adminProviders {
		opts = append(opts, WithAdminKeyProvider(p))
	}
	m, err := newModel(v, account, opts...)
	if err != nil {
		return err
	}
	// alt-screen by default; CHARON_TUI_NO_ALT=1 disables for diagnosing
	// terminals where alt-screen interacts badly with size reporting.
	teaOpts := []tea.ProgramOption{}
	if os.Getenv("CHARON_TUI_NO_ALT") == "" {
		teaOpts = append(teaOpts, tea.WithAltScreen())
	}
	prog := tea.NewProgram(m, teaOpts...)
	finalModel, err := prog.Run()
	if err != nil {
		return fmt.Errorf("tui: %w", err)
	}
	final := finalModel.(model)
	if final.err != nil {
		return final.err
	}
	if final.exitNote != "" {
		fmt.Println(final.exitNote)
	}
	return nil
}
