package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/charon/internal/vault"
)

// untoggleGmailReadonly returns a model with cursor on the gmail.readonly
// row and that row toggled off (i.e. a pending reduction).
func untoggleGmailReadonly(t *testing.T, v vault.Store, auth Authenticator) scopesModel {
	t.Helper()
	m := newScopesForTest(t, v, "a@gmail.com", auth)
	m, _ = moveToFirstListRow(t, m)
	for i, r := range m.rows {
		if r.short == "gmail.readonly" {
			for visIdx, rowIdx := range m.filtered {
				if rowIdx == i {
					m.cursor = visIdx
				}
			}
			break
		}
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	return m
}

func TestReduceConfirmContinueDispatchesAuthForceFresh(t *testing.T) {
	v := vaultWithBase("a@gmail.com", "https://www.googleapis.com/auth/gmail.readonly")
	auth := &stubAuth{
		returnCred: &vault.Credential{
			Provider: "google", Account: "a@gmail.com",
			Scopes: []string{
				"openid",
				"https://www.googleapis.com/auth/userinfo.email",
			},
		},
	}
	m := untoggleGmailReadonly(t, v, auth)

	// Open confirmation modal.
	m, _ = m.Update(keyPress("enter"))
	if m.state != stateReduceConfirm {
		t.Fatalf("after enter on reduction: state=%v want stateReduceConfirm", m.state)
	}

	// User presses 'y' to continue.
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if m.state != stateApplying {
		t.Errorf("after y: state=%v want stateApplying", m.state)
	}
	if cmd == nil {
		t.Fatal("expected applyCmd")
	}
	msg := cmd()
	if _, ok := msg.(applyResultMsg); !ok {
		t.Fatalf("expected applyResultMsg, got %T", msg)
	}
	if auth.calls != 1 {
		t.Errorf("auth calls = %d, want 1", auth.calls)
	}
	if !auth.gotForceFresh {
		t.Errorf("expected forceFresh=true on reductive apply, got false")
	}
}

func TestReduceConfirmCancelReturnsNormal(t *testing.T) {
	v := vaultWithBase("a@gmail.com", "https://www.googleapis.com/auth/gmail.readonly")
	auth := &stubAuth{}
	m := untoggleGmailReadonly(t, v, auth)
	m, _ = m.Update(keyPress("enter"))
	if m.state != stateReduceConfirm {
		t.Fatalf("setup: not in stateReduceConfirm")
	}

	for _, key := range []string{"n", "c"} {
		mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		if mm.state != stateNormal {
			t.Errorf("after %q: state=%v want stateNormal", key, mm.state)
		}
		if auth.calls != 0 {
			t.Errorf("auth should not be called on cancel, got %d", auth.calls)
		}
	}
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if mm.state != stateNormal {
		t.Errorf("after esc: state=%v want stateNormal", mm.state)
	}
}

func TestAdditiveApplyUsesForceFreshFalse(t *testing.T) {
	v := vaultWithBase("a@gmail.com")
	auth := &stubAuth{
		returnCred: &vault.Credential{
			Provider: "google", Account: "a@gmail.com",
			Scopes: []string{
				"openid",
				"https://www.googleapis.com/auth/userinfo.email",
				"https://www.googleapis.com/auth/gmail.readonly",
			},
		},
	}
	m := newScopesForTest(t, v, "a@gmail.com", auth)
	m, _ = moveToFirstListRow(t, m)
	// Toggle gmail.readonly on (additive).
	for i, r := range m.rows {
		if r.short == "gmail.readonly" {
			for visIdx, rowIdx := range m.filtered {
				if rowIdx == i {
					m.cursor = visIdx
				}
			}
			break
		}
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})

	// Enter on additive change goes straight to applying — no modal.
	m, cmd := m.Update(keyPress("enter"))
	if m.state != stateApplying {
		t.Errorf("additive enter: state=%v want stateApplying", m.state)
	}
	if cmd == nil {
		t.Fatal("expected applyCmd")
	}
	cmd()
	if auth.gotForceFresh {
		t.Errorf("additive apply: forceFresh should be false")
	}
}

func TestRevokeKeyOpensConfirmModal(t *testing.T) {
	v := vaultWithBase("a@gmail.com")
	auth := &stubAuth{}
	m := newScopesForTest(t, v, "a@gmail.com", auth)
	m, _ = moveToFirstListRow(t, m)

	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	if m.state != stateRevokeConfirm {
		t.Errorf("after R: state=%v want stateRevokeConfirm", m.state)
	}
	if cmd != nil {
		t.Errorf("R alone should not dispatch a command, got %T", cmd())
	}
}

func TestRevokeConfirmEmitsRevokeAccountMsg(t *testing.T) {
	v := vaultWithBase("a@gmail.com")
	auth := &stubAuth{}
	m := newScopesForTest(t, v, "a@gmail.com", auth)
	m.state = stateRevokeConfirm

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if cmd == nil {
		t.Fatal("expected revokeAccountMsg cmd")
	}
	msg := cmd()
	r, ok := msg.(revokeAccountMsg)
	if !ok {
		t.Fatalf("expected revokeAccountMsg, got %T", msg)
	}
	if r.account != "a@gmail.com" {
		t.Errorf("account = %q, want a@gmail.com", r.account)
	}
}

func TestRevokeConfirmCancelReturnsNormal(t *testing.T) {
	v := vaultWithBase("a@gmail.com")
	m := newScopesForTest(t, v, "a@gmail.com", &stubAuth{})
	m.state = stateRevokeConfirm

	for _, key := range []string{"n", "c"} {
		mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		if mm.state != stateNormal {
			t.Errorf("after %q: state=%v want stateNormal", key, mm.state)
		}
	}
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if mm.state != stateNormal {
		t.Errorf("after esc: state=%v want stateNormal", mm.state)
	}
}

func TestModelHandlesRevokeAccountMsg(t *testing.T) {
	v := vaultWithBase("a@gmail.com")
	// Add a refresh token so Revoke gets a non-empty argument.
	cred, _ := v.Get("google", "a@gmail.com")
	cred.RefreshToken = "fake-refresh-token"
	v.Set(cred)

	auth := &stubAuth{}
	m, err := newModel(v, "a@gmail.com", WithAuthenticator(auth))
	if err != nil {
		t.Fatalf("newModel: %v", err)
	}

	updated, cmd := m.Update(revokeAccountMsg{account: "a@gmail.com"})
	mm := updated.(model)

	if auth.revokeCalls != 1 {
		t.Errorf("Revoke calls = %d, want 1", auth.revokeCalls)
	}
	if auth.gotRevokeTok != "fake-refresh-token" {
		t.Errorf("Revoke got %q, want fake-refresh-token", auth.gotRevokeTok)
	}
	if _, err := v.Get("google", "a@gmail.com"); err == nil {
		t.Error("vault should have deleted the credential after revoke")
	}
	if cmd == nil {
		t.Error("expected tea.Quit cmd after revoke")
	}
	if mm.exitNote == "" {
		t.Error("expected exitNote describing the revoke")
	}
}
