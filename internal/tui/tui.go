// Package tui implements the bubbletea-based scope management UI.
package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/charon/internal/vault"
)

// Run launches the TUI, blocking until the user exits.
//
// Required: vault. Optional: account (skips picker), addr (badges), auth
// (apply support — without it, Enter on pending changes will fail).
func Run(v vault.Store, account, addr string, auth Authenticator) error {
	var opts []Option
	if addr != "" {
		opts = append(opts, WithDenialFetcher(httpDenialFetcher(addr)))
	}
	if auth != nil {
		opts = append(opts, WithAuthenticator(auth))
	}
	m, err := newModel(v, account, opts...)
	if err != nil {
		return err
	}
	prog := tea.NewProgram(m)
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
