package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestDumpScopesView prints the rendered scope view so we can inspect what's
// actually being emitted. Run with: go test ./internal/tui -v -run TestDumpScopesView
func TestDumpScopesView(t *testing.T) {
	v := vaultWithBase("a@gmail.com")
	rows, _ := loadScopeRows(v, "a@gmail.com", nil)
	m := newScopesModel("a@gmail.com", rows, nil)

	view := m.View()
	lines := strings.Split(view, "\n")

	t.Logf("rendered %d lines (no height set), %d total chars", len(lines), len(view))
	for i, line := range lines {
		t.Logf("%3d: %q", i+1, line)
	}
}

// TestDumpScopesViewSmallTerminal verifies row windowing fits a 22-line pane.
func TestDumpScopesViewSmallTerminal(t *testing.T) {
	v := vaultWithBase("a@gmail.com")
	rows, _ := loadScopeRows(v, "a@gmail.com", nil)
	m := newScopesModel("a@gmail.com", rows, nil)

	// Simulate a small terminal pane.
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 22})

	view := m.View()
	lines := strings.Split(view, "\n")
	t.Logf("rendered %d lines (height=22)", len(lines))
	for i, line := range lines {
		t.Logf("%3d: %q", i+1, line)
	}

	// Header and search bar should be in the first few lines (visible).
	first5 := strings.Join(lines[:5], "\n")
	if !strings.Contains(first5, "google / a@gmail.com") {
		t.Errorf("header missing from first 5 lines:\n%s", first5)
	}
	if !strings.Contains(first5, "filter (substring)") {
		t.Errorf("search placeholder missing from first 5 lines:\n%s", first5)
	}
	// Total lines should fit within reasonable height (≤ 22 + minor padding).
	if len(lines) > 25 {
		t.Errorf("rendered %d lines, want ≤ 25 for height=22", len(lines))
	}
}

func TestDumpScopesViewTinyTerminal(t *testing.T) {
	v := vaultWithBase("a@gmail.com")
	rows, _ := loadScopeRows(v, "a@gmail.com", nil)
	m := newScopesModel("a@gmail.com", rows, nil)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 16})

	view := m.View()
	lines := strings.Split(view, "\n")
	t.Logf("rendered %d lines (height=16)", len(lines))
	for i, line := range lines {
		t.Logf("%3d: %q", i+1, line)
	}
}
