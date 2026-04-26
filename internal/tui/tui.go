// Package tui implements the bubbletea-based scope management UI.
//
// Entry point: Run(v, account). Account picker is shown when account is empty.
// Selecting an account transitions to the scope view (M2+). Selecting "+ new
// account" kicks off OAuth with no login_hint and routes to scope view on
// success.
package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/charon/internal/vault"
)

// Run launches the TUI, blocking until the user exits.
//
// If account is empty, the account picker is shown first. Otherwise, the TUI
// jumps directly to the scope view for that account.
//
// M1: only the account picker is implemented. The selected account or
// new-account intent is printed and the function returns.
func Run(v vault.Store, account string) error {
	m, err := newModel(v, account)
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
	switch {
	case final.newAccount:
		fmt.Println("Selected: + new account (OAuth wiring lands in M2)")
	case final.selected != "":
		fmt.Printf("Selected: %s (scope view lands in M2)\n", final.selected)
	}
	return nil
}
