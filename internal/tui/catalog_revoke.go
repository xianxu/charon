package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/charon/internal/providers/catalog"
	"github.com/xianxu/charon/internal/vault"
)

// catalogRevokeModel is the confirm-then-execute modal for revoking a
// TypeCatalog credential. Mirror of adminRevokeModel's confirm /
// in-progress / error shape, but single-target: catalog credentials
// don't cascade.
//
// Posture:
//   - Upstream success or ErrNoRevokeEndpoint → vault.Delete → done.
//   - Upstream failure → error overlay with the upstream message;
//     on any key, fall back to local-delete + status note pointing
//     at console_url. Same "user wants this gone" stance the OAuth
//     Google revoke flow takes for AI Studio cleanup.
type catalogRevokeModel struct {
	entry   catalog.Entry
	account string
	keyHint string
	v       vault.Store

	state catalogRevokeState
	err   error // upstream error for the error-state view
}

type catalogRevokeState int

const (
	catalogRevokeStateConfirm catalogRevokeState = iota
	catalogRevokeStateInProgress
	catalogRevokeStateUpstreamFailed
)

// catalogRevokeDoneMsg signals the revoke completed (vault is
// consistent). statusNote carries an optional message for the
// provider picker — e.g. "removed locally; upstream may still be
// active — clean up at <console_url>".
type catalogRevokeDoneMsg struct {
	statusNote string
}

// catalogRevokeCancelMsg signals user cancelled the modal.
type catalogRevokeCancelMsg struct{}

// catalogRevokeUpstreamResultMsg carries the upstream revoke outcome
// back into the model.
type catalogRevokeUpstreamResultMsg struct {
	err error
}

func newCatalogRevokeModel(entry catalog.Entry, account string, v vault.Store) (catalogRevokeModel, error) {
	cred, err := v.Get(entry.ID, account)
	if err != nil {
		return catalogRevokeModel{}, fmt.Errorf("read credential %s/%s: %w", entry.ID, account, err)
	}
	if cred.Catalog == nil {
		return catalogRevokeModel{}, fmt.Errorf("credential %s/%s has no Catalog payload", entry.ID, account)
	}
	return catalogRevokeModel{
		entry:   entry,
		account: account,
		keyHint: hintFromKeyMaterial(cred.Catalog.KeyMaterial),
		v:       v,
		state:   catalogRevokeStateConfirm,
	}, nil
}

func (m catalogRevokeModel) Update(msg tea.Msg) (catalogRevokeModel, tea.Cmd) {
	switch m.state {
	case catalogRevokeStateConfirm:
		return m.updateConfirm(msg)
	case catalogRevokeStateInProgress:
		return m.updateInProgress(msg)
	case catalogRevokeStateUpstreamFailed:
		return m.updateUpstreamFailed(msg)
	}
	return m, nil
}

func (m catalogRevokeModel) updateConfirm(msg tea.Msg) (catalogRevokeModel, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch k.String() {
	case "y", "enter":
		m.state = catalogRevokeStateInProgress
		entry := m.entry
		account := m.account
		v := m.v
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cred, err := v.Get(entry.ID, account)
			if err != nil {
				return catalogRevokeUpstreamResultMsg{err: fmt.Errorf("re-read credential: %w", err)}
			}
			if cred.Catalog == nil {
				return catalogRevokeUpstreamResultMsg{err: fmt.Errorf("credential lost Catalog payload")}
			}
			return catalogRevokeUpstreamResultMsg{err: entry.RevokeKey(ctx, cred.Catalog.KeyMaterial)}
		}
	case "n", "esc":
		return m, func() tea.Msg { return catalogRevokeCancelMsg{} }
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func (m catalogRevokeModel) updateInProgress(msg tea.Msg) (catalogRevokeModel, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	r, ok := msg.(catalogRevokeUpstreamResultMsg)
	if !ok {
		return m, nil
	}
	switch {
	case r.err == nil:
		// Upstream deactivated. Local delete, done.
		if err := m.v.Delete(m.entry.ID, m.account); err != nil {
			m.state = catalogRevokeStateUpstreamFailed
			m.err = fmt.Errorf("upstream revoked, but local delete failed: %w", err)
			return m, nil
		}
		return m, func() tea.Msg {
			return catalogRevokeDoneMsg{
				statusNote: fmt.Sprintf("Revoked and removed %s/%s", m.entry.ID, m.account),
			}
		}
	case errors.Is(r.err, catalog.ErrNoRevokeEndpoint):
		// No revoke endpoint; local-delete + console_url message.
		if err := m.v.Delete(m.entry.ID, m.account); err != nil {
			m.state = catalogRevokeStateUpstreamFailed
			m.err = fmt.Errorf("local delete failed: %w", err)
			return m, nil
		}
		note := fmt.Sprintf("Removed %s/%s — clean up the key at %s", m.entry.ID, m.account, m.entry.ConsoleURL)
		if m.entry.ConsoleURL == "" {
			note = fmt.Sprintf("Removed %s/%s (catalog has no upstream revoke for this provider)",
				m.entry.ID, m.account)
		}
		return m, func() tea.Msg { return catalogRevokeDoneMsg{statusNote: note} }
	default:
		// Upstream attempted-and-failed. Surface to the user.
		m.state = catalogRevokeStateUpstreamFailed
		m.err = r.err
		return m, nil
	}
}

func (m catalogRevokeModel) updateUpstreamFailed(msg tea.Msg) (catalogRevokeModel, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch k.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "n", "esc":
		// Abort entirely — leave the credential in vault. User can
		// retry revoke later.
		return m, func() tea.Msg { return catalogRevokeCancelMsg{} }
	default:
		// Any other key → fall back to local-delete with a note that
		// upstream still has the key live. Same posture as Google
		// OAuth revoke flow handling AI Studio cleanup failures.
		if err := m.v.Delete(m.entry.ID, m.account); err != nil {
			m.err = fmt.Errorf("local delete failed: %w", err)
			return m, nil
		}
		note := fmt.Sprintf("Removed %s/%s locally — upstream revoke failed (%v); clean up at %s",
			m.entry.ID, m.account, m.err, m.entry.ConsoleURL)
		if m.entry.ConsoleURL == "" {
			note = fmt.Sprintf("Removed %s/%s locally — upstream revoke failed (%v)",
				m.entry.ID, m.account, m.err)
		}
		return m, func() tea.Msg { return catalogRevokeDoneMsg{statusNote: note} }
	}
}

func (m catalogRevokeModel) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("%s › %s › revoke", appName(), providerLabel(m.entry.ID))))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", 60))
	b.WriteString("\n\n")

	switch m.state {
	case catalogRevokeStateConfirm:
		b.WriteString(rowDelStyle.Render(fmt.Sprintf("  Revoke %s/%s", m.entry.ID, m.account)))
		b.WriteString("\n\n")
		if m.keyHint != "" {
			b.WriteString(fmt.Sprintf("    Hint:  %s\n", m.keyHint))
			b.WriteString("\n")
		}
		if m.entry.Revoke != nil {
			b.WriteString("  Charon will attempt to deactivate the key upstream and\n")
			b.WriteString("  remove the credential from its vault.\n")
		} else {
			b.WriteString("  Charon will remove the credential from its vault. The\n")
			b.WriteString("  key remains active at the provider until you delete it\n")
			b.WriteString("  manually:\n\n")
			if m.entry.ConsoleURL != "" {
				b.WriteString("    " + m.entry.ConsoleURL + "\n")
			}
		}
		b.WriteString("\n")
		b.WriteString(helpStyle.Render("[y/enter] proceed    [n/esc] cancel"))
	case catalogRevokeStateInProgress:
		b.WriteString("  Revoking...\n\n")
		b.WriteString(helpStyle.Render("(ctrl+c to abort)"))
	case catalogRevokeStateUpstreamFailed:
		b.WriteString(rowDelStyle.Render("  Upstream revoke failed"))
		b.WriteString("\n\n")
		if m.err != nil {
			b.WriteString("  " + m.err.Error())
			b.WriteString("\n\n")
		}
		if m.entry.ConsoleURL != "" {
			b.WriteString("  Manual cleanup: " + m.entry.ConsoleURL + "\n\n")
		}
		b.WriteString(helpStyle.Render("any key: remove locally anyway    [n/esc] keep credential"))
	}
	return b.String()
}
