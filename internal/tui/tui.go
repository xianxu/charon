// Package tui implements the bubbletea-based scope management UI.
//
// Entry point: Run(v, account, addr). When account is empty, the account
// picker is shown first. When non-empty, the scope view loads for that
// account directly.
package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/charon/internal/vault"
)

// Run launches the TUI, blocking until the user exits.
//
// `addr` is the proxy listen address used to fetch requested-scope badges
// from /scopes/denied. Empty addr disables badges (acceptable for offline
// usage).
func Run(v vault.Store, account, addr string) error {
	var opts []Option
	if addr != "" {
		opts = append(opts, WithDenialFetcher(httpDenialFetcher(addr)))
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
