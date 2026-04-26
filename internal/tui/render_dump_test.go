package tui

import (
	"strings"
	"testing"
)

// TestDumpScopesView prints the rendered scope view so we can inspect what's
// actually being emitted. Run with: go test ./internal/tui -v -run TestDumpScopesView
func TestDumpScopesView(t *testing.T) {
	v := vaultWithBase("a@gmail.com")
	rows, _ := loadScopeRows(v, "a@gmail.com", nil)
	m := newScopesModel("a@gmail.com", rows, nil)

	view := m.View()
	lines := strings.Split(view, "\n")

	t.Logf("rendered %d lines, %d total chars", len(lines), len(view))
	for i, line := range lines {
		t.Logf("%3d: %q", i+1, line)
	}
}
