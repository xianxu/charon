package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestSmallTerminalLayout is a regression test for the rendering pipeline:
// at a fixed height, the rendered view should be exactly that many lines,
// with the header and search bar at the top. This catches the class of
// bugs where the row block grew too tall and pushed the chrome off-screen
// (#000005 image 12 ghosting), or where a trailing newline scrolled the
// top off (#000005 final off-by-one).
func TestSmallTerminalLayout(t *testing.T) {
	v := vaultWithBase("a@gmail.com")
	rows, _ := loadScopeRows(v, "a@gmail.com", nil)
	m := newScopesModel("a@gmail.com", rows, nil)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 22})

	view := m.View()
	lines := strings.Split(view, "\n")

	// Auto-detection subtracts 1 for safety, so height=22 -> we render 21 lines.
	if got, want := len(lines), 21; got != want {
		t.Errorf("rendered %d lines for height=22, want %d", got, want)
	}
	first5 := strings.Join(lines[:5], "\n")
	if !strings.Contains(first5, "google / a@gmail.com") {
		t.Errorf("header missing from first 5 lines:\n%s", first5)
	}
	if !strings.Contains(first5, "filter (substring)") {
		t.Errorf("search placeholder missing from first 5 lines:\n%s", first5)
	}
}
