package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/charon/internal/providers/catalog"
	"github.com/xianxu/charon/internal/vault"
	"github.com/xianxu/charon/internal/vault/memory"
)

func newCatalogPasteFixture(t *testing.T) (catalogPasteModel, vault.Store) {
	t.Helper()
	v := memory.New()
	entry := catalog.Entry{
		ID:               "anthropic",
		Name:             "Anthropic",
		SignupURL:        "https://console.anthropic.com",
		KeyURL:           "https://console.anthropic.com/settings/keys",
		HostnamePatterns: []string{"api.anthropic.com"},
		Auth: catalog.Auth{
			Style:  "header",
			Header: "x-api-key",
		},
	}
	return newCatalogPasteModel(entry, v), v
}

// typeRunes drives a textinput by feeding each rune as a tea.KeyMsg.
// The bubbles/list / textinput Update path expects one rune-key per
// character.
func typeRunes(m catalogPasteModel, s string) catalogPasteModel {
	for _, r := range s {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

func TestCatalogPaste_HappyPath_StoresCredAndEmitsDone(t *testing.T) {
	m, v := newCatalogPasteFixture(t)

	// Step 1: type account name
	m = typeRunes(m, "personal")
	if got := strings.TrimSpace(m.accountInput.Value()); got != "personal" {
		t.Fatalf("account input = %q, want %q", got, "personal")
	}

	// Enter advances to step 2
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.state != catalogPasteStateKey {
		t.Fatalf("state = %d after enter on account, want catalogPasteStateKey", m.state)
	}

	// Step 2: type key, enter to store
	m = typeRunes(m, "sk-ant-FAKE")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected cmd from enter on key")
	}

	// Vault should have the credential stored under anthropic/personal
	cred, err := v.Get("anthropic", "personal")
	if err != nil {
		t.Fatalf("vault.Get: %v", err)
	}
	if cred.CredType() != vault.TypeCatalog {
		t.Errorf("CredType = %q, want %q", cred.CredType(), vault.TypeCatalog)
	}
	if cred.Catalog == nil || cred.Catalog.KeyMaterial != "sk-ant-FAKE" {
		t.Errorf("Catalog.KeyMaterial = %+v, want sk-ant-FAKE", cred.Catalog)
	}

	// Cmd should emit catalogPasteDoneMsg with provider+account
	msg := cmd()
	done, ok := msg.(catalogPasteDoneMsg)
	if !ok {
		t.Fatalf("expected catalogPasteDoneMsg, got %T", msg)
	}
	if done.provider != "anthropic" || done.account != "personal" {
		t.Errorf("done = %+v, want anthropic/personal", done)
	}
}

func TestCatalogPaste_EscFromAccount_EmitsCancel(t *testing.T) {
	m, v := newCatalogPasteFixture(t)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("expected cmd from esc on account")
	}
	if _, ok := cmd().(catalogPasteCancelMsg); !ok {
		t.Errorf("expected catalogPasteCancelMsg, got %T", cmd())
	}
	creds, _ := v.List()
	if len(creds) != 0 {
		t.Errorf("vault should be empty after cancel, got %d creds", len(creds))
	}
}

func TestCatalogPaste_EnterOnEmptyAccount_DoesNotAdvance(t *testing.T) {
	m, _ := newCatalogPasteFixture(t)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // no input yet
	if m.state != catalogPasteStateAccount {
		t.Errorf("state = %d, want catalogPasteStateAccount (empty account should not advance)", m.state)
	}
}

func TestCatalogPaste_EscFromKey_GoesBackToAccount(t *testing.T) {
	m, _ := newCatalogPasteFixture(t)
	m = typeRunes(m, "personal")
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // → key state
	m = typeRunes(m, "partial-key")

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.state != catalogPasteStateAccount {
		t.Fatalf("state = %d after esc on key, want catalogPasteStateAccount", m.state)
	}
	// Account name should be preserved
	if got := strings.TrimSpace(m.accountInput.Value()); got != "personal" {
		t.Errorf("account input lost on back-nav: %q", got)
	}
	// Key input should be cleared
	if got := m.keyInput.Value(); got != "" {
		t.Errorf("key input not cleared on back-nav: %q", got)
	}
}

func TestCatalogPaste_EnterOnEmptyKey_DoesNotStore(t *testing.T) {
	m, v := newCatalogPasteFixture(t)
	m = typeRunes(m, "personal")
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // → key state
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // empty key — no advance
	if m.state != catalogPasteStateKey {
		t.Errorf("state = %d, want catalogPasteStateKey", m.state)
	}
	creds, _ := v.List()
	if len(creds) != 0 {
		t.Errorf("vault should be empty after empty-key enter, got %d", len(creds))
	}
}

func TestCatalogPaste_ViewIncludesUrlsAndHost(t *testing.T) {
	m, _ := newCatalogPasteFixture(t)
	out := m.View()
	if !strings.Contains(out, "console.anthropic.com") {
		t.Errorf("view missing signup URL, got:\n%s", out)
	}
	if !strings.Contains(out, "/settings/keys") {
		t.Errorf("view missing key URL, got:\n%s", out)
	}
	if !strings.Contains(out, "api.anthropic.com") {
		t.Errorf("view missing hostname, got:\n%s", out)
	}
	if !strings.Contains(out, "ctrl+o") {
		t.Errorf("view missing ctrl+o hint, got:\n%s", out)
	}
}

func TestCatalogPaste_KeyViewIsMasked(t *testing.T) {
	m, _ := newCatalogPasteFixture(t)
	m = typeRunes(m, "personal")
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // → key state
	m = typeRunes(m, "sk-ant-FAKE-VISIBLE")
	out := m.View()
	if strings.Contains(out, "sk-ant-FAKE-VISIBLE") {
		t.Errorf("key view leaked plaintext key:\n%s", out)
	}
	if !strings.Contains(out, "Account: personal") {
		t.Errorf("key view should echo confirmed account name, got:\n%s", out)
	}
}
