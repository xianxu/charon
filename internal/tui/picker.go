package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/charon/internal/vault"
)

type pickerItem struct {
	email      string
	scopeCount int
	isNew      bool
}

type pickerModel struct {
	items  []pickerItem
	cursor int
}

func newPickerModel(v vault.Store) (pickerModel, error) {
	creds, err := v.List()
	if err != nil {
		return pickerModel{}, fmt.Errorf("list accounts: %w", err)
	}
	var items []pickerItem
	for _, c := range creds {
		if c.Provider != "google" {
			continue
		}
		items = append(items, pickerItem{
			email:      c.Account,
			scopeCount: len(c.Scopes),
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].email < items[j].email })
	items = append(items, pickerItem{isNew: true})
	return pickerModel{items: items}, nil
}

// Messages emitted by the picker.
type accountSelectedMsg struct{ email string }
type newAccountMsg struct{}

func (m pickerModel) Update(msg tea.Msg) (pickerModel, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch keyMsg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.items)-1 {
			m.cursor++
		}
	case "enter":
		if m.cursor < 0 || m.cursor >= len(m.items) {
			return m, nil
		}
		item := m.items[m.cursor]
		if item.isNew {
			return m, func() tea.Msg { return newAccountMsg{} }
		}
		return m, func() tea.Msg { return accountSelectedMsg{email: item.email} }
	case "q", "esc", "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func (m pickerModel) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Google accounts"))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", 40))
	b.WriteString("\n")
	for i, item := range m.items {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}
		var line string
		if item.isNew {
			line = "+ new account"
		} else {
			scopes := "scope"
			if item.scopeCount != 1 {
				scopes = "scopes"
			}
			line = fmt.Sprintf("%s  (%d %s)", item.email, item.scopeCount, scopes)
		}
		if i == m.cursor {
			line = selectedStyle.Render(line)
		} else if item.isNew {
			line = mutedStyle.Render(line)
		}
		b.WriteString(cursor)
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("↑/↓: navigate    enter: select    q/esc: quit"))
	b.WriteString("\n")
	return b.String()
}
