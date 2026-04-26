package tui

import "github.com/charmbracelet/lipgloss"

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#7D56F4"))

	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(lipgloss.Color("#7D56F4"))

	mutedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Italic(true)

	// Row state styles. Diff colors (green/red) take priority over the
	// requested badge tint, since target≠realized means the user has decided.
	rowGrantedStyle = lipgloss.NewStyle() // realized=on, target=on
	rowOffStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))   // realized=off, target=off, no badge
	rowReqStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("178"))   // realized=off, target=off, requested — muted yellow
	rowAddStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("70"))    // realized=off, target=on — green
	rowDelStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("167"))   // realized=on, target=off — red

	rowCursorStyle = lipgloss.NewStyle().Bold(true).Underline(true)
)

// styleForRow picks the right base style for a row's (realized, target,
// requested) combo, then adds cursor emphasis if highlight is true.
func styleForRow(r scopeRow, highlight bool) lipgloss.Style {
	var base lipgloss.Style
	switch {
	case r.realized && r.target:
		base = rowGrantedStyle
	case !r.realized && r.target:
		base = rowAddStyle
	case r.realized && !r.target:
		base = rowDelStyle
	case r.requested:
		base = rowReqStyle
	default:
		base = rowOffStyle
	}
	if highlight {
		base = base.Bold(true).Underline(true)
	}
	return base
}
