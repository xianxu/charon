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

// model is the top-level bubbletea model. Sub-models (picker, scopes...) live
// inside and are routed to by `current`.
type model struct {
	current screen
	picker  pickerModel
	scopes  scopesModel

	vault       vault.Store
	fetchDenied denialFetcher

	// terminal size
	width, height int

	// exit signals
	pendingNewAccount bool   // user chose "+ new account" — full flow lands in M3+
	exitNote          string // optional message printed on exit
	err               error
}

// Option configures the TUI on construction.
type Option func(*model)

// WithDenialFetcher overrides the function used to query the proxy for
// requested-scope badges. Tests inject a stub; production uses the HTTP
// fetcher.
func WithDenialFetcher(f denialFetcher) Option {
	return func(m *model) { m.fetchDenied = f }
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
		// Skip the picker: load scopes for the named account directly.
		rows, err := loadScopeRows(v, initialAccount, m.fetchDenied)
		if err != nil {
			return model{}, err
		}
		m.scopes = newScopesModel(initialAccount, rows)
		m.current = screenScopes
	}
	return m, nil
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case accountSelectedMsg:
		rows, err := loadScopeRows(m.vault, msg.email, m.fetchDenied)
		if err != nil {
			m.err = err
			return m, tea.Quit
		}
		m.scopes = newScopesModel(msg.email, rows)
		m.current = screenScopes
		return m, nil

	case newAccountMsg:
		// M2: full new-account flow (OAuth → discover email → scope view) is
		// scheduled for M3. For now, surface a note and exit cleanly.
		m.pendingNewAccount = true
		m.exitNote = "+ new account flow lands in M3 — using existing account for now"
		return m, tea.Quit

	case scopesQuitMsg:
		// M2: no pending changes possible, just quit. M3 will inject a save/
		// discard/cancel modal here when target≠realized.
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
