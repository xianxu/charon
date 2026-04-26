package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/charon/internal/vault"
	"github.com/xianxu/charon/internal/vault/memory"
)

func TestLoadRowsCatalogOnly(t *testing.T) {
	rows, err := loadScopeRows(memory.New(), "nobody@gmail.com", nil)
	if err != nil {
		t.Fatalf("loadScopeRows: %v", err)
	}
	if len(rows) < 5 {
		t.Fatalf("expected catalog rows, got %d", len(rows))
	}
	for _, r := range rows {
		if r.realized || r.target || r.requested || r.custom {
			t.Errorf("unauthenticated load: row %q has flags set: %+v", r.short, r)
		}
	}
}

func TestLoadRowsMarksRealizedAndTarget(t *testing.T) {
	v := memory.New()
	v.Set(&vault.Credential{
		Provider: "google",
		Account:  "a@gmail.com",
		Scopes: []string{
			"https://www.googleapis.com/auth/gmail.readonly",
			"https://www.googleapis.com/auth/calendar.readonly",
		},
	})
	rows, err := loadScopeRows(v, "a@gmail.com", nil)
	if err != nil {
		t.Fatalf("loadScopeRows: %v", err)
	}
	got := map[string]bool{}
	for _, r := range rows {
		if r.realized {
			got[r.short] = true
		}
		if r.realized != r.target {
			t.Errorf("row %q: realized=%v target=%v (M2: should match)", r.short, r.realized, r.target)
		}
	}
	for _, want := range []string{"gmail.readonly", "calendar.readonly"} {
		if !got[want] {
			t.Errorf("expected %q realized, missing from %v", want, got)
		}
	}
}

func TestLoadRowsAppendsCustomScopes(t *testing.T) {
	v := memory.New()
	v.Set(&vault.Credential{
		Provider: "google",
		Account:  "a@gmail.com",
		Scopes:   []string{"https://www.googleapis.com/auth/some.unknown.scope"},
	})
	rows, err := loadScopeRows(v, "a@gmail.com", nil)
	if err != nil {
		t.Fatalf("loadScopeRows: %v", err)
	}
	var custom *scopeRow
	for i := range rows {
		if rows[i].custom {
			custom = &rows[i]
			break
		}
	}
	if custom == nil {
		t.Fatal("expected a custom row for the unknown scope")
	}
	if custom.short != "some.unknown.scope" {
		t.Errorf("custom short = %q, want some.unknown.scope", custom.short)
	}
	if !custom.realized || !custom.target {
		t.Errorf("custom row from keychain should be realized+target")
	}
}

func TestLoadRowsBadgesFromDenials(t *testing.T) {
	v := memory.New()
	fetcher := func(account string) []string {
		return []string{
			"https://www.googleapis.com/auth/calendar.readonly", // catalog
			"https://www.googleapis.com/auth/some.brand.new",    // not in catalog
		}
	}
	rows, err := loadScopeRows(v, "a@gmail.com", fetcher)
	if err != nil {
		t.Fatalf("loadScopeRows: %v", err)
	}
	var calReq, customReq *scopeRow
	for i := range rows {
		if rows[i].short == "calendar.readonly" {
			calReq = &rows[i]
		}
		if rows[i].full == "https://www.googleapis.com/auth/some.brand.new" {
			customReq = &rows[i]
		}
	}
	if calReq == nil || !calReq.requested {
		t.Errorf("catalog row not marked requested: %+v", calReq)
	}
	if customReq == nil {
		t.Fatal("brand-new denied scope not appended as custom row")
	}
	if !customReq.requested || !customReq.custom {
		t.Errorf("custom requested row flags wrong: %+v", customReq)
	}
}

func TestLoadRowsTolerantOfNilFetcher(t *testing.T) {
	rows, err := loadScopeRows(memory.New(), "a@gmail.com", nil)
	if err != nil || len(rows) == 0 {
		t.Fatalf("nil fetcher should still load catalog: %v %d", err, len(rows))
	}
	for _, r := range rows {
		if r.requested {
			t.Errorf("nil fetcher: row %q marked requested", r.short)
		}
	}
}

func TestRowMatchesFilter(t *testing.T) {
	r := scopeRow{short: "gmail.readonly", description: "Read Gmail messages"}
	cases := []struct {
		filter string
		want   bool
	}{
		{"", true},
		{"gmail", true},
		{"GMAIL", true},
		{"readonly", true},
		{"messages", true},
		{"calendar", false},
	}
	for _, tc := range cases {
		if got := r.matches(tc.filter); got != tc.want {
			t.Errorf("matches(%q) = %v, want %v", tc.filter, got, tc.want)
		}
	}
}

func TestScopesFocusToggle(t *testing.T) {
	rows, _ := loadScopeRows(memory.New(), "a@gmail.com", nil)
	m := newScopesModel("a@gmail.com", rows, nil)

	// Initial focus is search.
	if m.focus != focusSearch {
		t.Fatalf("initial focus = %v, want search", m.focus)
	}

	// Down moves focus to list (when there are filtered rows).
	m, _ = m.Update(keyPress("down"))
	if m.focus != focusList {
		t.Errorf("after down: focus = %v, want list", m.focus)
	}
	if m.cursor != 0 {
		t.Errorf("after down: cursor = %d, want 0", m.cursor)
	}

	// Up at cursor=0 returns to search.
	m, _ = m.Update(keyPress("up"))
	if m.focus != focusSearch {
		t.Errorf("up at cursor 0: focus = %v, want search", m.focus)
	}

	// Down → list, then `/` returns to search.
	m, _ = m.Update(keyPress("down"))
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	if m.focus != focusSearch {
		t.Errorf("after / from list: focus = %v, want search", m.focus)
	}

	// From list, esc returns to search (does NOT quit when in list focus).
	m, _ = m.Update(keyPress("down"))
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.focus != focusSearch {
		t.Errorf("after esc from list: focus = %v, want search", m.focus)
	}
}

func TestScopesEscFromSearchSignalsQuit(t *testing.T) {
	rows, _ := loadScopeRows(memory.New(), "a@gmail.com", nil)
	m := newScopesModel("a@gmail.com", rows, nil)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc from search: expected quit command")
	}
	if _, ok := cmd().(scopesQuitMsg); !ok {
		t.Fatalf("expected scopesQuitMsg, got %T", cmd())
	}
}

func TestScopesQFromListSignalsQuit(t *testing.T) {
	rows, _ := loadScopeRows(memory.New(), "a@gmail.com", nil)
	m := newScopesModel("a@gmail.com", rows, nil)

	// Move to list focus first.
	m, _ = m.Update(keyPress("down"))
	if m.focus != focusList {
		t.Fatalf("setup: focus not list")
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatal("q from list: expected quit command")
	}
	if _, ok := cmd().(scopesQuitMsg); !ok {
		t.Fatalf("expected scopesQuitMsg, got %T", cmd())
	}
}

func TestScopesFilterReducesVisibleRows(t *testing.T) {
	rows, _ := loadScopeRows(memory.New(), "a@gmail.com", nil)
	m := newScopesModel("a@gmail.com", rows, nil)
	totalCatalogRows := len(m.filtered)

	// Type "gmail" — should reduce to gmail.* rows.
	for _, r := range "gmail" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if len(m.filtered) == 0 || len(m.filtered) >= totalCatalogRows {
		t.Errorf("filter 'gmail': %d rows, want a strict subset of %d", len(m.filtered), totalCatalogRows)
	}
	for _, idx := range m.filtered {
		row := m.rows[idx]
		if !row.matches("gmail") {
			t.Errorf("filtered row %q doesn't match 'gmail'", row.short)
		}
	}
}

func TestScopesViewRendersExpectedContent(t *testing.T) {
	v := memory.New()
	v.Set(&vault.Credential{
		Provider: "google",
		Account:  "a@gmail.com",
		Scopes:   []string{"https://www.googleapis.com/auth/gmail.readonly"},
	})
	fetcher := func(string) []string {
		return []string{"https://www.googleapis.com/auth/calendar.readonly"}
	}
	rows, _ := loadScopeRows(v, "a@gmail.com", fetcher)
	m := newScopesModel("a@gmail.com", rows, nil)
	out := m.View()

	for _, want := range []string{
		"google / a@gmail.com",
		"granted",
		"gmail.readonly",
		"calendar.readonly",
		"Read Gmail messages",
		"!", // badge for calendar.readonly
	} {
		if !strings.Contains(out, want) {
			t.Errorf("View() missing %q\nout:\n%s", want, out)
		}
	}
}

func TestScopesNoPendingChangesInM2(t *testing.T) {
	v := memory.New()
	v.Set(&vault.Credential{
		Provider: "google", Account: "a@gmail.com",
		Scopes: []string{"https://www.googleapis.com/auth/gmail.readonly"},
	})
	rows, _ := loadScopeRows(v, "a@gmail.com", nil)
	m := newScopesModel("a@gmail.com", rows, nil)
	if m.pendingChanges() {
		t.Error("M2 view-only: pendingChanges() must be false on load")
	}
}

func TestPickerToScopesTransition(t *testing.T) {
	v := memory.New()
	v.Set(&vault.Credential{Provider: "google", Account: "a@gmail.com"})

	m, err := newModel(v, "")
	if err != nil {
		t.Fatalf("newModel: %v", err)
	}
	if m.current != screenPicker {
		t.Fatalf("initial screen = %v, want picker", m.current)
	}

	updated, _ := m.Update(accountSelectedMsg{email: "a@gmail.com"})
	m = updated.(model)
	if m.current != screenScopes {
		t.Errorf("after selection: screen = %v, want scopes", m.current)
	}
	if m.scopes.account != "a@gmail.com" {
		t.Errorf("scope view account = %q, want a@gmail.com", m.scopes.account)
	}
}

func TestNewAccountInM2ExitsWithNote(t *testing.T) {
	m, _ := newModel(memory.New(), "")
	updated, cmd := m.Update(newAccountMsg{})
	m = updated.(model)
	if !m.pendingNewAccount {
		t.Error("expected pendingNewAccount=true")
	}
	if m.exitNote == "" {
		t.Error("expected exitNote to be set")
	}
	if cmd == nil {
		t.Error("expected tea.Quit command")
	}
}

func TestInitialAccountSkipsPicker(t *testing.T) {
	v := memory.New()
	v.Set(&vault.Credential{Provider: "google", Account: "a@gmail.com"})
	m, err := newModel(v, "a@gmail.com")
	if err != nil {
		t.Fatalf("newModel: %v", err)
	}
	if m.current != screenScopes {
		t.Errorf("with initialAccount: screen = %v, want scopes", m.current)
	}
}
