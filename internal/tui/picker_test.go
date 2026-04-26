package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/charon/internal/vault"
	"github.com/xianxu/charon/internal/vault/memory"
)

func setupVault(t *testing.T, creds ...*vault.Credential) *memory.Store {
	t.Helper()
	v := memory.New()
	for _, c := range creds {
		if err := v.Set(c); err != nil {
			t.Fatalf("vault.Set: %v", err)
		}
	}
	return v
}

func TestPickerEmpty(t *testing.T) {
	m, err := newPickerModel(memory.New())
	if err != nil {
		t.Fatalf("newPickerModel: %v", err)
	}
	if len(m.items) != 1 {
		t.Fatalf("items = %d, want 1 (only +new account)", len(m.items))
	}
	if !m.items[0].isNew {
		t.Errorf("only item should be 'new account'")
	}
}

func TestPickerSortsAccountsAndPlacesNewLast(t *testing.T) {
	v := setupVault(t,
		&vault.Credential{Provider: "google", Account: "z@gmail.com", Scopes: []string{"openid"}},
		&vault.Credential{Provider: "google", Account: "a@gmail.com", Scopes: []string{"openid", "email"}},
		&vault.Credential{Provider: "dropbox", Account: "x@dropbox.com"}, // non-google, must be filtered
	)
	m, err := newPickerModel(v)
	if err != nil {
		t.Fatalf("newPickerModel: %v", err)
	}
	if len(m.items) != 3 {
		t.Fatalf("items = %d, want 3 (2 google + new)", len(m.items))
	}
	if m.items[0].email != "a@gmail.com" || m.items[1].email != "z@gmail.com" {
		t.Errorf("accounts not sorted alphabetically: %+v", m.items[:2])
	}
	if !m.items[2].isNew {
		t.Errorf("last item must be '+ new account', got %+v", m.items[2])
	}
	for _, it := range m.items {
		if it.email == "x@dropbox.com" {
			t.Errorf("non-google account leaked into google picker")
		}
	}
}

func TestPickerNavigation(t *testing.T) {
	v := setupVault(t,
		&vault.Credential{Provider: "google", Account: "a@gmail.com"},
		&vault.Credential{Provider: "google", Account: "b@gmail.com"},
	)
	m, _ := newPickerModel(v)

	steps := []struct {
		key        string
		wantCursor int
	}{
		{"down", 1},
		{"down", 2},
		{"down", 2}, // boundary clamps
		{"up", 1},
		{"up", 0},
		{"up", 0}, // boundary clamps
		{"j", 1},  // vim-style aliases
		{"k", 0},
	}
	for _, s := range steps {
		var cmd tea.Cmd
		m, cmd = m.Update(keyPress(s.key))
		_ = cmd
		if m.cursor != s.wantCursor {
			t.Errorf("after %q: cursor = %d, want %d", s.key, m.cursor, s.wantCursor)
		}
	}
}

func TestPickerSelectExistingAccount(t *testing.T) {
	v := setupVault(t, &vault.Credential{Provider: "google", Account: "a@gmail.com"})
	m, _ := newPickerModel(v)

	_, cmd := m.Update(keyPress("enter"))
	if cmd == nil {
		t.Fatal("enter on existing account: expected command")
	}
	msg := cmd()
	got, ok := msg.(accountSelectedMsg)
	if !ok {
		t.Fatalf("expected accountSelectedMsg, got %T", msg)
	}
	if got.email != "a@gmail.com" {
		t.Errorf("selected = %q, want a@gmail.com", got.email)
	}
}

func TestPickerSelectNewAccount(t *testing.T) {
	m, _ := newPickerModel(memory.New())
	// Only item is "+ new account", cursor starts at 0.
	_, cmd := m.Update(keyPress("enter"))
	if cmd == nil {
		t.Fatal("enter on +new: expected command")
	}
	if _, ok := cmd().(newAccountMsg); !ok {
		t.Fatalf("expected newAccountMsg, got %T", cmd())
	}
}

func TestPickerViewRendersAllItems(t *testing.T) {
	v := setupVault(t,
		&vault.Credential{Provider: "google", Account: "a@gmail.com", Scopes: []string{"openid", "email"}},
		&vault.Credential{Provider: "google", Account: "b@gmail.com", Scopes: []string{"openid"}},
	)
	m, _ := newPickerModel(v)
	out := m.View()

	for _, want := range []string{"Google accounts", "a@gmail.com", "b@gmail.com", "+ new account", "2 scopes", "1 scope"} {
		if !strings.Contains(out, want) {
			t.Errorf("View() missing %q, got:\n%s", want, out)
		}
	}
}

// keyPress builds a tea.KeyMsg from a key name like "up", "enter", "j".
func keyPress(name string) tea.KeyMsg {
	switch name {
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
	}
}
