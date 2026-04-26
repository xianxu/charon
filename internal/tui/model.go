package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/charon/internal/vault"
)

// newAccountAuthedMsg is the result of an OAuth flow kicked off by
// "+ new account" in the picker. cred.Account is the email Google told us
// the user authenticated as.
type newAccountAuthedMsg struct {
	cred *vault.Credential
	err  error
}

type screen int

const (
	screenPicker screen = iota
	screenScopes
)

// model is the top-level bubbletea model.
type model struct {
	current screen
	picker  pickerModel
	scopes  scopesModel

	vault       vault.Store
	auth        Authenticator
	fetchDenied denialFetcher

	width, height int

	// exit signals
	exitNote string
	err      error
}

type Option func(*model)

func WithDenialFetcher(f denialFetcher) Option {
	return func(m *model) { m.fetchDenied = f }
}

// WithAuthenticator wires the OAuth dispatch used for apply. Required before
// the scope view can apply changes; without it, apply is a no-op.
func WithAuthenticator(a Authenticator) Option {
	return func(m *model) { m.auth = a }
}

func newModel(v vault.Store, initialAccount string, opts ...Option) (model, error) {
	p, err := newPickerModel(v)
	if err != nil {
		return model{}, err
	}
	m := model{
		current: screenPicker,
		picker:  p,
		vault:   v,
	}
	for _, opt := range opts {
		opt(&m)
	}
	if initialAccount != "" {
		rows, err := loadScopeRows(v, initialAccount, m.fetchDenied)
		if err != nil {
			return model{}, err
		}
		m.scopes = newScopesModel(initialAccount, rows, m.auth)
		m.current = screenScopes
	}
	return m, nil
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// Forward to active screen so scopesModel can resize its row window.
		if m.current == screenScopes {
			var cmd tea.Cmd
			m.scopes, cmd = m.scopes.Update(msg)
			return m, cmd
		}
		return m, nil

	case accountSelectedMsg:
		rows, err := loadScopeRows(m.vault, msg.email, m.fetchDenied)
		if err != nil {
			m.err = err
			return m, tea.Quit
		}
		m.scopes = newScopesModel(msg.email, rows, m.auth)
		m.current = screenScopes
		return m, nil

	case newAccountMsg:
		// First-time auth: empty scopes (just openid+email via
		// requiredGoogleScopes), no login_hint. The browser opens, user
		// picks the Google account they want, completes consent. Auth
		// returns a credential whose Account is the discovered email.
		if m.auth == nil {
			m.err = fmt.Errorf("no authenticator configured")
			return m, tea.Quit
		}
		auth := m.auth
		return m, func() tea.Msg {
			cred, err := auth.Auth("", nil, nil, false)
			return newAccountAuthedMsg{cred: cred, err: err}
		}

	case newAccountAuthedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, tea.Quit
		}
		if err := m.vault.Set(msg.cred); err != nil {
			m.err = err
			return m, tea.Quit
		}
		rows, err := loadScopeRows(m.vault, msg.cred.Account, m.fetchDenied)
		if err != nil {
			m.err = err
			return m, tea.Quit
		}
		m.scopes = newScopesModel(msg.cred.Account, rows, m.auth)
		m.current = screenScopes
		return m, nil

	case scopesQuitMsg:
		return m, tea.Quit

	case applyResultMsg:
		// Side effect: persist the new credential before forwarding to scopes.
		// Forwarded message lets scopes update its row state.
		if msg.err == nil && msg.cred != nil {
			if err := m.vault.Set(msg.cred); err != nil {
				msg.err = err
			}
		}
		var cmd tea.Cmd
		m.scopes, cmd = m.scopes.Update(msg)
		return m, cmd

	case revokeAccountMsg:
		// Look up credential, call Revoke, delete from vault, then exit.
		// Errors (vault miss, network failure) surface as applyResultMsg
		// so the user sees them in the existing apply-error overlay.
		cred, err := m.vault.Get("google", msg.account)
		if err != nil {
			return m, func() tea.Msg {
				return applyResultMsg{err: err}
			}
		}
		if m.auth != nil {
			if err := m.auth.Revoke(cred.RefreshToken); err != nil {
				return m, func() tea.Msg {
					return applyResultMsg{err: err}
				}
			}
		}
		if err := m.vault.Delete("google", msg.account); err != nil {
			return m, func() tea.Msg {
				return applyResultMsg{err: err}
			}
		}
		m.exitNote = "Revoked and removed " + msg.account
		return m, tea.Quit
	}

	switch m.current {
	case screenPicker:
		var cmd tea.Cmd
		m.picker, cmd = m.picker.Update(msg)
		return m, cmd
	case screenScopes:
		var cmd tea.Cmd
		m.scopes, cmd = m.scopes.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m model) View() string {
	switch m.current {
	case screenPicker:
		return m.picker.View()
	case screenScopes:
		return m.scopes.View()
	}
	return ""
}
