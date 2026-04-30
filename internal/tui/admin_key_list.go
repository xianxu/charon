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

// adminKeyListRow is one row in the admin-key entity list. Three
// kinds: the admin-key state row at top, project rows in the middle,
// and the trailing "+ new project" / "+ new workspace" affordance.
type adminKeyListRow struct {
	kind adminKeyRowKind

	// adminKeyRow fields:
	adminKeySet bool
	adminLabel  string // "xianxu@gmail.com / acme-inc" or "not set — press enter to configure"

	// projectRow fields:
	account    string // X-Charon-Account
	projectID  string // proj_… / ws_…
	keyHint    string // "sk-…xyz" — derived from KeyMaterial prefix+suffix
}

type adminKeyRowKind int

const (
	rowAdminKey adminKeyRowKind = iota
	rowProject
	rowAddNew
)

// adminKeyListModel renders the entity-list for admin-key providers
// (OpenAI projects, Anthropic workspaces). Phase 1: render +
// navigation only. Phase 2 wires the paste/mint/revoke actions.
type adminKeyListModel struct {
	provider     string // "openai" / "anthropic"
	entityPlural string // "Projects" / "Workspaces"
	entityAdd    string // "+ new project" / "+ new workspace"

	rows        []adminKeyListRow
	cursor      int
	adminKeySet bool

	// adminLabelStr is the human label shown on the admin-key row
	// (`<OrgLabel> / <OrgName>` when set, fallback otherwise).
	adminLabelStr string

	// transient status (shown below the help line)
	statusMsg string
}

// adminKeyListBackMsg signals "navigate back to provider picker."
type adminKeyListBackMsg struct{}

// newAdminKeyListModel builds the model from the vault + admin-key
// store state. Errors propagate from vault.List; missing admin key is
// not an error (the row just renders red).
func newAdminKeyListModel(
	provider string,
	v vault.Store,
	store *providers.AdminKeyStore,
) (adminKeyListModel, error) {
	m := adminKeyListModel{
		provider:     provider,
		entityPlural: titleCase(entityTermPlural(provider)),
		entityAdd:    "+ new " + entityTerm(provider),
	}

	// Admin-key state row
	if store != nil && store.IsSet() {
		_, meta, err := store.Get()
		if err == nil {
			m.adminKeySet = true
			m.adminLabelStr = formatAdminLabel(meta)
		} else {
			// Corrupt-meta path: don't claim the admin key is set; the
			// user needs to re-paste.
			m.adminKeySet = false
			m.adminLabelStr = "(corrupt meta — re-paste admin key)"
		}
	}
	m.rows = append(m.rows, adminKeyListRow{
		kind:        rowAdminKey,
		adminKeySet: m.adminKeySet,
		adminLabel:  m.adminLabelStr,
	})

	// Project rows: vault entries with Type==admin-key for this provider
	creds, err := v.List()
	if err != nil {
		return adminKeyListModel{}, fmt.Errorf("list credentials: %w", err)
	}
	type projItem struct {
		account, projectID, hint string
	}
	var projects []projItem
	for _, c := range creds {
		if c.Provider != provider || c.CredType() != vault.TypeAdminKey || c.AdminKey == nil {
			continue
		}
		projects = append(projects, projItem{
			account:   c.Account,
			projectID: c.AdminKey.ProjectID,
			hint:      hintFromKeyMaterial(c.AdminKey.KeyMaterial),
		})
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].account < projects[j].account })
	for _, p := range projects {
		m.rows = append(m.rows, adminKeyListRow{
			kind:      rowProject,
			account:   p.account,
			projectID: p.projectID,
			keyHint:   p.hint,
		})
	}

	// Trailing add-new affordance.
	m.rows = append(m.rows, adminKeyListRow{kind: rowAddNew})

	return m, nil
}

// formatAdminLabel produces "<label> / <name>" with safe fallbacks.
func formatAdminLabel(meta providers.AdminMeta) string {
	switch {
	case meta.OrgLabel != "" && meta.OrgName != "":
		return meta.OrgLabel + " / " + meta.OrgName
	case meta.OrgLabel != "":
		return meta.OrgLabel
	case meta.OrgName != "":
		return meta.OrgName
	case meta.OrgID != "":
		// Truncate the opaque OrgID for display — full id isn't useful
		// to the eye and may overflow the row width.
		if len(meta.OrgID) > 20 {
			return meta.OrgID[:20] + "…"
		}
		return meta.OrgID
	}
	return "(unlabeled)"
}

// hintFromKeyMaterial builds a "sk-…xyz" pattern from the captured key
// material so the list shows a recognizable suffix without exposing
// the secret. Mirrors the partial-key-hint convention from the
// upstream admin APIs.
func hintFromKeyMaterial(material string) string {
	if material == "" {
		return ""
	}
	// First few chars (the prefix) are not secret; last 3 are
	// recognizable but don't compromise the secret.
	const prefixLen = 3
	const suffixLen = 3
	if len(material) <= prefixLen+suffixLen+1 {
		return material // too short to redact meaningfully — surface as-is
	}
	return material[:prefixLen] + "…" + material[len(material)-suffixLen:]
}

func (m adminKeyListModel) Update(msg tea.Msg) (adminKeyListModel, tea.Cmd) {
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
		if m.cursor < len(m.rows)-1 {
			m.cursor++
			m.statusMsg = ""
		}
	case "enter":
		// Phase 1 stub. Phase 2 will dispatch on row kind:
		//   rowAdminKey + !adminKeySet → open admin-key paste flow
		//   rowAdminKey +  adminKeySet → open admin-key replace flow
		//   rowProject               → open project detail
		//   rowAddNew + !adminKeySet  → flash "set admin key first" status
		//   rowAddNew +  adminKeySet  → open mint flow
		row := m.rows[m.cursor]
		if row.kind == rowAddNew && !m.adminKeySet {
			m.statusMsg = "set the admin key first — see the row above"
			return m, nil
		}
		m.statusMsg = "(action coming in M4 phase 2)"
		return m, nil
	case "r":
		// Phase 1 stub for revoke; phase 2 wires the modal + cascade.
		row := m.rows[m.cursor]
		if row.kind == rowAddNew {
			return m, nil
		}
		m.statusMsg = "(revoke coming in M4 phase 2)"
		return m, nil
	case "esc":
		return m, func() tea.Msg { return adminKeyListBackMsg{} }
	case "q":
		return m, tea.Quit
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func (m adminKeyListModel) View() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render(fmt.Sprintf("Charon › %s", providerLabel(m.provider))))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(m.entityPlural))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", 60))
	b.WriteString("\n")

	for i, row := range m.rows {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}
		var line string
		switch row.kind {
		case rowAdminKey:
			line = renderAdminKeyRow(row)
		case rowProject:
			line = renderProjectRow(row)
		case rowAddNew:
			line = m.entityAdd
			if !m.adminKeySet {
				line = mutedStyle.Render(line) + mutedStyle.Render("   (admin key required — see above)")
			}
		}
		if i == m.cursor && row.kind != rowAdminKey {
			line = selectedStyle.Render(line)
		} else if i == m.cursor && row.kind == rowAdminKey {
			// Don't selectedStyle-overlay the admin row (the colored
			// glyph would be obscured). Bold + prefix arrow is enough.
			line = lipgloss.NewStyle().Bold(true).Render(line)
		}
		b.WriteString(cursor)
		b.WriteString(line)
		b.WriteString("\n")
		// Visual separator between admin-key row and the rest.
		if row.kind == rowAdminKey {
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	if m.statusMsg != "" {
		b.WriteString(helpStyle.Render(m.statusMsg))
		b.WriteString("\n")
	}
	b.WriteString(helpStyle.Render("↑↓ nav   enter open   r revoke   esc back   q quit"))
	return b.String()
}

func renderAdminKeyRow(row adminKeyListRow) string {
	if row.adminKeySet {
		glyph := lipgloss.NewStyle().Foreground(lipgloss.Color("70")).Render("●")
		return fmt.Sprintf("%s Admin key   %s", glyph, row.adminLabel)
	}
	glyph := lipgloss.NewStyle().Foreground(lipgloss.Color("167")).Render("○")
	return fmt.Sprintf("%s Admin key   not set — press enter to configure", glyph)
}

func renderProjectRow(row adminKeyListRow) string {
	// Columns: account (24) projectID (16) "key" hint
	acct := padOrTrunc(row.account, 24)
	pid := padOrTrunc(row.projectID, 16)
	hint := row.keyHint
	if hint == "" {
		hint = "(no key)"
	}
	return fmt.Sprintf("%s %s key %s", acct, pid, hint)
}

// titleCase capitalizes the first letter of a string. Used for screen
// titles where the entity term is plural-lowercase ("projects" →
// "Projects").
func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
