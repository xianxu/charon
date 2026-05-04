package tui

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/charon/internal/providers/catalog"
	"github.com/xianxu/charon/internal/vault/memory"
)

// anthropicLikeRevokeEntry mirrors the catalog package's test fixture
// — local copy so this TUI test doesn't reach across packages for an
// unexported helper.
func anthropicLikeRevokeEntry(listURL, revokeURL, consoleURL string) catalog.Entry {
	return catalog.Entry{
		ID:               "anthropic",
		Name:             "Anthropic",
		HostnamePatterns: []string{"api.anthropic.com"},
		Auth: catalog.Auth{
			Style:        "header",
			Header:       "x-api-key",
			ExtraHeaders: map[string]string{"anthropic-version": "2023-06-01"},
		},
		Revoke: &catalog.Revoke{
			ListEndpoint: &catalog.ListEndpoint{
				URL:        listURL,
				KeyMatch:   "partial_key_hint",
				ResultPath: "data[].id",
			},
			Method:     "POST",
			URL:        revokeURL,
			Body:       `{"status":"inactive"}`,
			AuthSource: "pasted_key",
		},
		ConsoleURL: consoleURL,
	}
}

const fakeAnthropicListBody = `{
  "data": [
    {"id": "apikey_001", "partial_key_hint": "sk-ant-…AAAA", "status": "active"}
  ]
}`

const matchingPastedKey = "sk-ant-api03-zzzzzzzzzzzzzzzzzAAAA"

func TestCatalogRevoke_ConfirmHappyPath_DeactivatesAndDeletes(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/organizations/api_keys", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, fakeAnthropicListBody)
	})
	revoked := false
	mux.HandleFunc("/v1/organizations/api_keys/apikey_001", func(w http.ResponseWriter, r *http.Request) {
		revoked = true
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	entry := anthropicLikeRevokeEntry(
		srv.URL+"/v1/organizations/api_keys",
		srv.URL+"/v1/organizations/api_keys/{key_id}",
		"https://console.anthropic.com/settings/keys",
	)
	v := memory.New()
	storeCatalogCred(t, v, "anthropic", "personal", matchingPastedKey)

	m, err := newCatalogRevokeModel(entry, "personal", v)
	if err != nil {
		t.Fatalf("newCatalogRevokeModel: %v", err)
	}

	// y → in-progress with cmd that does the upstream call.
	updated, cmd := m.Update(tea.KeyMsg{Runes: []rune{'y'}, Type: tea.KeyRunes})
	m = updated
	if m.state != catalogRevokeStateInProgress {
		t.Fatalf("after y: state = %d, want inProgress", m.state)
	}
	if cmd == nil {
		t.Fatal("y produced no cmd")
	}
	resultMsg := cmd()
	if _, ok := resultMsg.(catalogRevokeUpstreamResultMsg); !ok {
		t.Fatalf("cmd() = %T, want catalogRevokeUpstreamResultMsg", resultMsg)
	}
	updated, doneCmd := m.Update(resultMsg)
	m = updated
	if doneCmd == nil {
		t.Fatal("expected catalogRevokeDoneMsg cmd")
	}
	done, ok := doneCmd().(catalogRevokeDoneMsg)
	if !ok {
		t.Fatalf("doneCmd() = %T, want catalogRevokeDoneMsg", doneCmd())
	}
	if !strings.Contains(done.statusNote, "Revoked and removed") {
		t.Errorf("statusNote = %q, want 'Revoked and removed' substring", done.statusNote)
	}
	if !revoked {
		t.Error("upstream revoke endpoint was not called")
	}
	// vault entry is gone.
	if _, err := v.Get("anthropic", "personal"); err == nil {
		t.Error("vault still has anthropic/personal after successful revoke")
	}
}

func TestCatalogRevoke_NoEndpoint_LocalDeleteWithConsoleHint(t *testing.T) {
	entry := catalog.Entry{
		ID:         "groq",
		Name:       "Groq",
		Auth:       catalog.Auth{Style: "bearer"},
		ConsoleURL: "https://console.groq.com/keys",
	}
	v := memory.New()
	storeCatalogCred(t, v, "groq", "default", "gsk-key-AAAA")

	m, err := newCatalogRevokeModel(entry, "default", v)
	if err != nil {
		t.Fatalf("newCatalogRevokeModel: %v", err)
	}
	// Confirm view should mention manual cleanup.
	view := m.View()
	if !strings.Contains(view, "manually") {
		t.Errorf("confirm view missing manual-cleanup language:\n%s", view)
	}

	updated, cmd := m.Update(tea.KeyMsg{Runes: []rune{'y'}, Type: tea.KeyRunes})
	m = updated
	resultMsg := cmd()
	r, ok := resultMsg.(catalogRevokeUpstreamResultMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want catalogRevokeUpstreamResultMsg", resultMsg)
	}
	if !errors.Is(r.err, catalog.ErrNoRevokeEndpoint) {
		t.Errorf("upstream err = %v, want ErrNoRevokeEndpoint", r.err)
	}
	updated, doneCmd := m.Update(resultMsg)
	m = updated
	done, ok := doneCmd().(catalogRevokeDoneMsg)
	if !ok {
		t.Fatalf("doneCmd() = %T, want catalogRevokeDoneMsg", doneCmd())
	}
	if !strings.Contains(done.statusNote, "console.groq.com/keys") {
		t.Errorf("statusNote = %q, want console.groq.com URL", done.statusNote)
	}
	if _, err := v.Get("groq", "default"); err == nil {
		t.Error("vault still has groq/default after no-endpoint revoke")
	}
}

func TestCatalogRevoke_UpstreamFailure_PromptsLocalDeleteAndCarries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":"insufficient_scope"}`)
	}))
	defer srv.Close()
	entry := anthropicLikeRevokeEntry(srv.URL, srv.URL+"/{key_id}", "https://console.anthropic.com/settings/keys")
	v := memory.New()
	storeCatalogCred(t, v, "anthropic", "personal", matchingPastedKey)

	m, _ := newCatalogRevokeModel(entry, "personal", v)
	updated, cmd := m.Update(tea.KeyMsg{Runes: []rune{'y'}, Type: tea.KeyRunes})
	m = updated
	resultMsg := cmd()
	updated, _ = m.Update(resultMsg)
	m = updated
	if m.state != catalogRevokeStateUpstreamFailed {
		t.Fatalf("expected upstream-failed state, got %d", m.state)
	}
	view := m.View()
	if !strings.Contains(view, "401") {
		t.Errorf("error view doesn't mention 401:\n%s", view)
	}
	if !strings.Contains(view, "console.anthropic.com") {
		t.Errorf("error view missing console URL:\n%s", view)
	}

	// any key (other than n/esc) → fall back to local delete.
	updated, doneCmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated
	if doneCmd == nil {
		t.Fatal("any-key after upstream-fail produced no cmd")
	}
	done, ok := doneCmd().(catalogRevokeDoneMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want catalogRevokeDoneMsg", doneCmd())
	}
	if !strings.Contains(done.statusNote, "Removed") || !strings.Contains(done.statusNote, "upstream revoke failed") {
		t.Errorf("statusNote = %q, want 'Removed … upstream revoke failed' phrasing", done.statusNote)
	}
	if _, err := v.Get("anthropic", "personal"); err == nil {
		t.Error("vault entry should have been deleted on local-delete fallback")
	}
}

func TestCatalogRevoke_UpstreamFailure_EscPreservesCredential(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	entry := anthropicLikeRevokeEntry(srv.URL, srv.URL+"/{key_id}", "")
	v := memory.New()
	storeCatalogCred(t, v, "anthropic", "personal", matchingPastedKey)
	m, _ := newCatalogRevokeModel(entry, "personal", v)

	m, cmd := m.Update(tea.KeyMsg{Runes: []rune{'y'}, Type: tea.KeyRunes})
	m, _ = m.Update(cmd())
	if m.state != catalogRevokeStateUpstreamFailed {
		t.Fatalf("state = %d, want upstreamFailed", m.state)
	}
	_, escCmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if escCmd == nil {
		t.Fatal("esc produced no cmd")
	}
	if _, ok := escCmd().(catalogRevokeCancelMsg); !ok {
		t.Fatalf("escCmd() = %T, want catalogRevokeCancelMsg", escCmd())
	}
	if _, err := v.Get("anthropic", "personal"); err != nil {
		t.Errorf("vault entry missing after esc on upstream-fail; expected preserved")
	}
}

func TestCatalogRevoke_ConfirmCancelEmitsCancelMsg(t *testing.T) {
	v := memory.New()
	storeCatalogCred(t, v, "anthropic", "personal", "sk-ant-key-AAAA")
	entry := catalog.Entry{ID: "anthropic", Name: "Anthropic", Auth: catalog.Auth{Style: "header", Header: "x-api-key"}}
	m, _ := newCatalogRevokeModel(entry, "personal", v)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc on confirm produced no cmd")
	}
	if _, ok := cmd().(catalogRevokeCancelMsg); !ok {
		t.Fatalf("cmd() = %T, want catalogRevokeCancelMsg", cmd())
	}
}
