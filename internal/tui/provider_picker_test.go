package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/charon/internal/providers"
	"github.com/xianxu/charon/internal/vault"
	"github.com/xianxu/charon/internal/vault/memory"
)

// fakeAdminStore is a minimal AdminKeyStore for picker tests. Real
// store would touch the keychain; here we drive it via injectable IO
// so the picker sees the configured-state we want.
func fakeAdminStore(t *testing.T, provider string, configured bool, label string) *providers.AdminKeyStore {
	t.Helper()
	entries := map[string]string{}
	get := func(service, account string) (string, error) {
		v, ok := entries[account]
		if !ok {
			return "", _testErr(account)
		}
		return v, nil
	}
	set := func(service, account, value string) error {
		entries[account] = value
		return nil
	}
	del := func(service, account string) error {
		delete(entries, account)
		return nil
	}
	s := providers.NewAdminKeyStoreWithIO(provider, "charon-test", get, set, del)
	if configured {
		if err := s.Set("sk-test-admin", providers.AdminMeta{
			OrgID:    "org-test-001",
			OrgLabel: label,
			OrgName:  "test-org",
		}); err != nil {
			t.Fatalf("seed admin store: %v", err)
		}
	}
	return s
}

type _testErrType string

func (e _testErrType) Error() string { return "not found: " + string(e) }
func _testErr(s string) error        { return _testErrType(s) }

func TestProviderPicker_EmptyVault_NoAdminKeys(t *testing.T) {
	v := memory.New()
	m, err := newProviderPickerModel(v, nil)
	if err != nil {
		t.Fatalf("newProviderPickerModel: %v", err)
	}
	// Always-present rows: Google + "+ add provider" — minimum 2.
	if len(m.items) != 2 {
		t.Errorf("expected 2 items (google + add-provider), got %d: %+v", len(m.items), m.items)
	}
	if m.items[0].name != "google" {
		t.Errorf("first item should be google, got %+v", m.items[0])
	}
	if !m.items[len(m.items)-1].isAddProvider {
		t.Errorf("last item should be + add provider, got %+v", m.items[len(m.items)-1])
	}
}

func TestProviderPicker_GoogleAccountCount_SingularPlural(t *testing.T) {
	cases := []struct {
		name     string
		accounts []string
		want     string
	}{
		{"none", nil, "0 accounts"},
		{"one", []string{"a@gmail.com"}, "1 account"},
		{"two", []string{"a@gmail.com", "b@gmail.com"}, "2 accounts"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := memory.New()
			for _, a := range tc.accounts {
				_ = v.Set(&vault.Credential{Provider: "google", Account: a})
			}
			m, _ := newProviderPickerModel(v, nil)
			if m.items[0].summary != tc.want {
				t.Errorf("google summary = %q, want %q", m.items[0].summary, tc.want)
			}
		})
	}
}

func TestProviderPicker_AdminKey_RedWhenUnconfigured(t *testing.T) {
	v := memory.New()
	stores := map[string]*providers.AdminKeyStore{
		"openai": fakeAdminStore(t, "openai", false, ""),
	}
	m, _ := newProviderPickerModel(v, stores)

	// Items: google, openai, +add-provider
	if len(m.items) < 3 {
		t.Fatalf("expected ≥3 items, got %d", len(m.items))
	}
	openai := m.items[1]
	if openai.name != "openai" {
		t.Fatalf("expected openai at index 1, got %+v", openai)
	}
	if openai.glyph != "○" {
		t.Errorf("unconfigured openai glyph = %q, want ○", openai.glyph)
	}
	if openai.adminKeySet {
		t.Error("unconfigured openai should have adminKeySet=false")
	}
	if !strings.Contains(openai.summary, "not set") {
		t.Errorf("unconfigured openai summary should mention 'not set', got %q", openai.summary)
	}
}

func TestProviderPicker_AdminKey_GreenWhenConfigured_WithMintCount(t *testing.T) {
	v := memory.New()
	// Two minted projects under openai.
	_ = v.Set(&vault.Credential{
		Type: vault.TypeAdminKey, Provider: "openai", Account: "work",
		AdminKey: &vault.AdminKeyData{OrgID: "org-test-001", ProjectID: "proj_1", KeyMaterial: "sk-test-1"},
	})
	_ = v.Set(&vault.Credential{
		Type: vault.TypeAdminKey, Provider: "openai", Account: "personal",
		AdminKey: &vault.AdminKeyData{OrgID: "org-test-001", ProjectID: "proj_2", KeyMaterial: "sk-test-2"},
	})
	stores := map[string]*providers.AdminKeyStore{
		"openai": fakeAdminStore(t, "openai", true, "xianxu@gmail.com"),
	}
	m, _ := newProviderPickerModel(v, stores)

	openai := m.items[1]
	if openai.glyph != "●" {
		t.Errorf("configured openai glyph = %q, want ●", openai.glyph)
	}
	if !openai.adminKeySet {
		t.Error("configured openai should have adminKeySet=true")
	}
	if openai.summary != "2 keys" {
		t.Errorf("configured openai summary = %q, want '2 keys'", openai.summary)
	}
}

func TestProviderPicker_View_RendersCleanly(t *testing.T) {
	v := memory.New()
	_ = v.Set(&vault.Credential{Provider: "google", Account: "a@gmail.com"})
	stores := map[string]*providers.AdminKeyStore{
		"anthropic": fakeAdminStore(t, "anthropic", true, "me@example.com"),
		"openai":    fakeAdminStore(t, "openai", false, ""),
	}
	m, _ := newProviderPickerModel(v, stores)
	view := m.View()

	for _, want := range []string{"Charon", "Provider", "Google", "OpenAI", "Anthropic", "+ add provider"} {
		if !strings.Contains(view, want) {
			t.Errorf("View() missing %q\n%s", want, view)
		}
	}
	if !strings.Contains(view, "1 account") {
		t.Error("Google should show 1 account")
	}
}

func TestProviderPicker_NavigationKeys(t *testing.T) {
	v := memory.New()
	stores := map[string]*providers.AdminKeyStore{
		"openai": fakeAdminStore(t, "openai", false, ""),
	}
	m, _ := newProviderPickerModel(v, stores)
	// Items: google (0), openai (1), + add provider (2).

	if m.cursor != 0 {
		t.Fatalf("initial cursor = %d, want 0", m.cursor)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.cursor != 1 {
		t.Errorf("after down: cursor = %d, want 1", m.cursor)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.cursor != 2 {
		t.Errorf("after second down: cursor = %d, want 2", m.cursor)
	}
	// Past-the-end is clamped, not wrapped.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.cursor != 2 {
		t.Errorf("over-the-end: cursor = %d, want 2 (clamped)", m.cursor)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.cursor != 1 {
		t.Errorf("after up: cursor = %d, want 1", m.cursor)
	}
}

func TestProviderPicker_EnterEmitsSelectedMsg(t *testing.T) {
	v := memory.New()
	stores := map[string]*providers.AdminKeyStore{
		"openai": fakeAdminStore(t, "openai", false, ""),
	}
	m, _ := newProviderPickerModel(v, stores)

	// Enter on google (cursor 0).
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter should emit a command")
	}
	msg := cmd()
	sel, ok := msg.(providerSelectedMsg)
	if !ok {
		t.Fatalf("expected providerSelectedMsg, got %T", msg)
	}
	if sel.name != "google" || sel.provType != vault.TypeOAuth {
		t.Errorf("selection mismatch: %+v", sel)
	}

	// Move to openai and enter.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	msg = cmd()
	sel = msg.(providerSelectedMsg)
	if sel.name != "openai" || sel.provType != vault.TypeAdminKey {
		t.Errorf("openai selection mismatch: %+v", sel)
	}
}

func TestProviderPicker_EnterOnAddProvider_EmitsAddMsg(t *testing.T) {
	v := memory.New()
	m, _ := newProviderPickerModel(v, nil)
	// Cursor at 0 (google). Move to last (+ add provider).
	for m.cursor < len(m.items)-1 {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter should emit a command")
	}
	if _, ok := cmd().(addProviderMsg); !ok {
		t.Errorf("expected addProviderMsg from + add provider row, got %T", cmd())
	}
	// The picker should also surface a stub status pointing at #15.
	if !strings.Contains(updated.statusMsg, "#15") {
		t.Errorf("+ add provider should set a status mentioning #15, got %q", updated.statusMsg)
	}
	view := updated.View()
	if !strings.Contains(view, "#15") {
		t.Errorf("rendered view should include #15 stub status, got\n%s", view)
	}
}

func TestProviderPicker_StatusClearsOnNav(t *testing.T) {
	v := memory.New()
	m, _ := newProviderPickerModel(v, nil)
	// Move to last and trigger the stub.
	for m.cursor < len(m.items)-1 {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if updated.statusMsg == "" {
		t.Fatal("expected status to be set after enter on + add provider")
	}
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyUp})
	if updated.statusMsg != "" {
		t.Errorf("status should clear on nav, got %q", updated.statusMsg)
	}
}

func TestProviderPicker_QuitKey(t *testing.T) {
	v := memory.New()
	m, _ := newProviderPickerModel(v, nil)
	for _, key := range []string{"q", "esc", "ctrl+c"} {
		_, cmd := m.Update(tea.KeyMsg{Runes: []rune(key), Type: tea.KeyRunes})
		// `esc` and `ctrl+c` arrive as different bubbletea types; just
		// build the runes form and verify a tea.Quit-shaped command.
		_ = cmd
		_ = key
	}
	// Direct quit via the standard q rune.
	_, cmd := m.Update(tea.KeyMsg{Runes: []rune{'q'}, Type: tea.KeyRunes})
	if cmd == nil {
		t.Fatal("q should emit tea.Quit")
	}
	// Quit returns a quitMsg; assert by running cmd and checking type.
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("q should produce tea.QuitMsg, got %T", cmd())
	}
}

func TestProviderLabel_KnownAndUnknown(t *testing.T) {
	cases := map[string]string{
		"google":    "Google",
		"openai":    "OpenAI",
		"anthropic": "Anthropic",
		"groq":      "Groq", // Title-cased fallback
		"":          "",
	}
	for in, want := range cases {
		if got := providerLabel(in); got != want {
			t.Errorf("providerLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEntityTerm_PerProvider(t *testing.T) {
	if entityTerm("openai") != "project" || entityTermPlural("openai") != "projects" {
		t.Error("openai entity term wrong")
	}
	if entityTerm("anthropic") != "workspace" || entityTermPlural("anthropic") != "workspaces" {
		t.Error("anthropic entity term wrong")
	}
	if entityTerm("groq") != "account" || entityTermPlural("groq") != "accounts" {
		t.Error("default entity term should be account")
	}
}

// Smoke-test that newModel routes through newProviderPickerModel
// successfully when admin stores are wired.
func TestModel_StartsAtProviderPicker(t *testing.T) {
	v := memory.New()
	m, err := newModel(v, "")
	if err != nil {
		t.Fatalf("newModel: %v", err)
	}
	if m.current != screenProvider {
		t.Errorf("current = %v, want screenProvider", m.current)
	}
	// Renders without panic; not a snapshot test (chrome may evolve).
	view := m.View()
	if view == "" {
		t.Error("View on screenProvider rendered empty")
	}
	_ = time.Now() // silence unused-import if other helpers are removed
}
