package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/charon/internal/vault"
)

type screen int

const (
	screenPicker screen = iota
	// screenScopes — added in M2
)

// model is the top-level bubbletea model. Sub-models (picker, scopes...) live
// inside and are routed to by `current`.
type model struct {
	current screen
	picker  pickerModel

	// terminal size
	width, height int

	// exit signals
	selected   string // account email selected
	newAccount bool   // user chose "+ new account"
	err        error
}

func newModel(v vault.Store, initialAccount string) (model, error) {
	p, err := newPickerModel(v)
	if err != nil {
		return model{}, err
	}
	return model{current: screenPicker, picker: p}, nil
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case accountSelectedMsg:
		m.selected = msg.email
		return m, tea.Quit

	case newAccountMsg:
		m.newAccount = true
		return m, tea.Quit

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		}
	}

	switch m.current {
	case screenPicker:
		var cmd tea.Cmd
		m.picker, cmd = m.picker.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m model) View() string {
	switch m.current {
	case screenPicker:
		return m.picker.View()
	}
	return ""
}
