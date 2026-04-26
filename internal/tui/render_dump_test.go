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

	// Raw height: at h=22, we render exactly 22 lines (no trailing newline).
	if got, want := len(lines), 22; got != want {
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

// TestResizeAdjustsLayout verifies that a WindowSizeMsg after the model is
// already running causes the rendered view to resize accordingly. This is
// what triggers when the user drags the terminal window edge.
func TestResizeAdjustsLayout(t *testing.T) {
	v := vaultWithBase("a@gmail.com")
	rows, _ := loadScopeRows(v, "a@gmail.com", nil)
	m := newScopesModel("a@gmail.com", rows, nil)

	// 20 catalog rows + 7 chrome = 27 max — rendering is capped at the
	// catalog size when the terminal is taller than needed.
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if got, want := len(strings.Split(m.View(), "\n")), 27; got != want {
		t.Errorf("after Height=30: rendered %d, want %d", got, want)
	}

	// Shrink below the catalog — content should shrink with it (raw 16).
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 16})
	if got, want := len(strings.Split(m.View(), "\n")), 16; got != want {
		t.Errorf("after shrink to Height=16: rendered %d, want %d", got, want)
	}

	// Grow back — content should grow (raw 25).
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 25})
	if got, want := len(strings.Split(m.View(), "\n")), 25; got != want {
		t.Errorf("after grow to Height=25: rendered %d, want %d", got, want)
	}

	// Header + search must remain visible at the top through all sizes.
	first5 := strings.Join(strings.Split(m.View(), "\n")[:5], "\n")
	if !strings.Contains(first5, "google / a@gmail.com") {
		t.Errorf("header missing after resize:\n%s", first5)
	}
}

// TestResizePreservesCursorVisibility verifies that when the cursor is past
// the new visible window after a shrink, adjustWindow scrolls the window
// to keep the cursor in view.
func TestResizePreservesCursorVisibility(t *testing.T) {
	v := vaultWithBase("a@gmail.com")
	rows, _ := loadScopeRows(v, "a@gmail.com", nil)
	m := newScopesModel("a@gmail.com", rows, nil)

	// Tall terminal first, move cursor near the bottom of the catalog.
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m, _ = m.Update(keyPress("down"))
	for i := 0; i < 15; i++ {
		m, _ = m.Update(keyPress("down"))
	}
	cursorAfterDowns := m.cursor
	if cursorAfterDowns < 10 {
		t.Fatalf("setup: cursor only at %d after 15 downs", cursorAfterDowns)
	}

	// Shrink the terminal. Cursor must remain inside the visible window.
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 16})
	visible := m.visibleRowCount()
	if m.cursor < m.windowStart || m.cursor >= m.windowStart+visible {
		t.Errorf("after shrink: cursor %d not in window [%d, %d)",
			m.cursor, m.windowStart, m.windowStart+visible)
	}
}
