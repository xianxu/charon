package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/charon/internal/vault"
)

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
	pendingNewAccount bool
	exitNote          string
	err               error
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
		m.pendingNewAccount = true
		m.exitNote = "+ new account flow lands later — pick an existing account for now"
		return m, tea.Quit

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
