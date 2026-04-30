package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/xianxu/charon/internal/providers"
	"github.com/xianxu/charon/internal/vault"
)

// providerPickerItem is one row in the top-level provider picker.
// Either an existing provider (name + label + summary) or the
// "+ add provider" affordance (#15 stub for catalog).
type providerPickerItem struct {
	// Static identity.
	name      string // "google" / "openai" / "anthropic"
	label     string // "Google" / "OpenAI" / "Anthropic"
	typeLabel string // "OAuth" / "Admin key" / "API key"

	// Dynamic summary state computed at picker creation.
	glyph        string // "●" green configured / "○" red not-set / "" oauth-no-glyph
	summary      string // "3 accounts" / "2 projects" / "admin key not set"
	provType     string // vault.TypeOAuth / vault.TypeAdminKey / vault.TypeCatalog
	adminKeySet  bool   // for admin-key providers — drives the glyph color

	// Affordance row (terminal "+ add provider"). Mutually exclusive
	// with the identity fields.
	isAddProvider bool
}

// providerPickerModel is the new top-level screen. Lists Google
// (always), plus any admin-key providers registered via
// WithAdminKeyProvider, plus a "+ add provider" stub for the catalog
// (#15) flow.
type providerPickerModel struct {
	items     []providerPickerItem
	cursor    int
	statusMsg string // transient hint shown on hover/action; clears on next nav
}

// providerSelectedMsg is emitted when the user picks a provider.
// Forwarded to the top-level model which sets up the per-type entity
// list and routes screens.
type providerSelectedMsg struct {
	name     string
	provType string
}

// addProviderMsg signals the user wanted to add a Tier 3 catalog
// provider (#15). Phase 1 just shows a "coming soon" status; #15
// wires the catalog picker.
type addProviderMsg struct{}

// newProviderPickerModel builds the picker by combining what's in
// the vault (existing accounts → counts) with what's registered as
// admin-key providers (stores → glyph state).
//
// adminStores is keyed by provider name; passing an empty/nil map is
// fine — the picker just shows Google (and the catalog "+" row).
func newProviderPickerModel(
	v vault.Store,
	adminStores map[string]*providers.AdminKeyStore,
) (providerPickerModel, error) {
	creds, err := v.List()
	if err != nil {
		return providerPickerModel{}, fmt.Errorf("list accounts: %w", err)
	}

	// Per-provider counters.
	googleAccounts := 0
	adminCounts := map[string]int{} // provider name → minted-key count
	for _, c := range creds {
		switch c.CredType() {
		case vault.TypeOAuth:
			if c.Provider == "google" {
				googleAccounts++
			}
		case vault.TypeAdminKey:
			adminCounts[c.Provider]++
		}
	}

	items := []providerPickerItem{
		{
			name:      "google",
			label:     "Google",
			typeLabel: "OAuth",
			provType:  vault.TypeOAuth,
			summary:   pluralize(googleAccounts, "account", "accounts"),
		},
	}

	// Sort admin-key providers alphabetically so the picker is stable
	// across runs.
	adminNames := make([]string, 0, len(adminStores))
	for name := range adminStores {
		adminNames = append(adminNames, name)
	}
	sort.Strings(adminNames)
	for _, name := range adminNames {
		store := adminStores[name]
		set := store.IsSet()
		item := providerPickerItem{
			name:        name,
			label:       providerLabel(name),
			typeLabel:   "Admin key",
			provType:    vault.TypeAdminKey,
			adminKeySet: set,
		}
		if set {
			item.glyph = "●"
			item.summary = pluralize(adminCounts[name], entityTerm(name), entityTermPlural(name))
		} else {
			item.glyph = "○"
			item.summary = "admin key not set"
		}
		items = append(items, item)
	}

	items = append(items, providerPickerItem{isAddProvider: true})

	return providerPickerModel{items: items}, nil
}

// providerLabel maps a provider name to its display label. Falls
// back to a Title-cased version of name for unknown providers.
func providerLabel(name string) string {
	switch name {
	case "google":
		return "Google"
	case "openai":
		return "OpenAI"
	case "anthropic":
		return "Anthropic"
	}
	if name == "" {
		return ""
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

// entityTerm returns the singular per-provider term for a credential
// (project for OpenAI, workspace for Anthropic, account otherwise).
// Used by the picker summary and by adminKeyListModel's screen title.
func entityTerm(provider string) string {
	switch provider {
	case "openai":
		return "project"
	case "anthropic":
		return "workspace"
	}
	return "account"
}

func entityTermPlural(provider string) string {
	switch provider {
	case "openai":
		return "projects"
	case "anthropic":
		return "workspaces"
	}
	return "accounts"
}

// upstreamContainerLabel returns the per-provider phrase for the
// upstream container that holds API keys. Used in mint flow Step 2
// where naming the provider explicitly clarifies that the user is
// picking a real OpenAI/Anthropic container, not a charon concept.
// Falls back to "container" for unknown providers.
func upstreamContainerLabel(provider string) string {
	switch provider {
	case "openai":
		return "OpenAI project"
	case "anthropic":
		return "Anthropic workspace"
	}
	return "container"
}

func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%d %s", n, plural)
}

func (m providerPickerModel) Update(msg tea.Msg) (providerPickerModel, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch keyMsg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
			m.statusMsg = ""
		}
	case "down", "j":
		if m.cursor < len(m.items)-1 {
			m.cursor++
			m.statusMsg = ""
		}
	case "enter":
		if m.cursor < 0 || m.cursor >= len(m.items) {
			return m, nil
		}
		item := m.items[m.cursor]
		if item.isAddProvider {
			// Stub for #15. Catalog (Tier 3 long-tail providers —
			// Anthropic, Groq, Mistral, etc.) ships in the catalog
			// issue. For now, surface a status message naming what's
			// coming so users aren't left wondering why nothing
			// happened.
			m.statusMsg = "+ add provider opens the catalog picker — coming in #15. " +
				"Today: Google (OAuth) + OpenAI (admin key) only."
			return m, func() tea.Msg { return addProviderMsg{} }
		}
		return m, func() tea.Msg {
			return providerSelectedMsg{name: item.name, provType: item.provType}
		}
	case "q", "esc", "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func (m providerPickerModel) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Charon"))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("Provider"))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", 60))
	b.WriteString("\n")

	for i, item := range m.items {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}

		var line string
		if item.isAddProvider {
			line = "+ add provider"
			if i == m.cursor {
				line = selectedStyle.Render(line)
			} else {
				line = mutedStyle.Render(line)
			}
		} else {
			line = renderProviderItem(item)
			if i == m.cursor {
				line = selectedStyle.Render(line)
			}
		}
		b.WriteString(cursor)
		b.WriteString(line)
		b.WriteString("\n")
	}

	b.WriteString("\n")
	if m.statusMsg != "" {
		b.WriteString(helpStyle.Render(m.statusMsg))
		b.WriteString("\n")
	}
	b.WriteString(helpStyle.Render("↑↓ nav   enter select   q quit"))
	return b.String()
}

// renderProviderItem composes the provider row: label, type label,
// glyph (admin-key only), summary. Width-stable so columns align
// across rows.
func renderProviderItem(item providerPickerItem) string {
	// Columns: label (16 chars) typeLabel (12) glyph+summary
	label := padOrTrunc(item.label, 16)
	typeLab := padOrTrunc(item.typeLabel, 12)

	var glyph string
	if item.glyph != "" {
		// Color the glyph by configured-state. The admin-key red/green
		// is a primary affordance; using lipgloss directly here so it
		// renders even when this row isn't cursor-highlighted.
		switch item.glyph {
		case "●":
			glyph = lipgloss.NewStyle().Foreground(lipgloss.Color("70")).Render(item.glyph) + " "
		case "○":
			glyph = lipgloss.NewStyle().Foreground(lipgloss.Color("167")).Render(item.glyph) + " "
		default:
			glyph = item.glyph + " "
		}
	}
	return fmt.Sprintf("%s %s %s%s", label, typeLab, glyph, item.summary)
}

func padOrTrunc(s string, width int) string {
	if len(s) >= width {
		if len(s) > width {
			return s[:width]
		}
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}
