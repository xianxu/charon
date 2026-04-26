package tui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/charon/internal/oauth"
	"github.com/xianxu/charon/internal/vault"
)

// scopeRow is one displayable row in the scope view.
//
// In M2 (view-only), `target` always equals `realized` — toggling lands in M3.
// `requested` is set if the proxy has a recent denial entry for this scope.
type scopeRow struct {
	short       string
	full        string
	description string
	realized    bool
	target      bool
	requested   bool
	custom      bool // not in static catalog (came from keychain)
}

// denialFetcher returns scopes that have been denied for the given account.
// Implementations should be best-effort: an unreachable proxy should return
// (nil, nil), not an error, so the TUI degrades gracefully.
type denialFetcher func(account string) []string

// httpDenialFetcher queries proxy at addr for /scopes/denied.
func httpDenialFetcher(addr string) denialFetcher {
	return func(account string) []string {
		u := fmt.Sprintf("http://%s/scopes/denied?provider=google&account=%s",
			addr, url.QueryEscape(account))
		client := http.Client{Timeout: 1 * time.Second}
		resp, err := client.Get(u)
		if err != nil {
			return nil
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil
		}
		var denials []struct {
			Scope string `json:"scope"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&denials); err != nil {
			return nil
		}
		out := make([]string, 0, len(denials))
		for _, d := range denials {
			out = append(out, d.Scope)
		}
		return out
	}
}

// loadScopeRows builds the row set for an account by combining the static
// catalog, any keychain-only scopes, and the proxy's denial badges.
func loadScopeRows(v vault.Store, account string, fetchDenied denialFetcher) ([]scopeRow, error) {
	cred, err := v.Get("google", account)
	granted := map[string]bool{}
	if err == nil {
		for _, s := range cred.Scopes {
			granted[s] = true
		}
	}

	rows := make([]scopeRow, 0, len(oauth.GoogleScopeCatalog))
	seen := map[string]bool{}
	for _, info := range oauth.GoogleScopeCatalog {
		realized := granted[info.Scope]
		rows = append(rows, scopeRow{
			short:       info.Short,
			full:        info.Scope,
			description: info.Description,
			realized:    realized,
			target:      realized,
		})
		seen[info.Scope] = true
	}
	// Append any granted scopes that aren't in the catalog as "custom" rows.
	if cred != nil {
		extras := make([]string, 0)
		for _, s := range cred.Scopes {
			if !seen[s] {
				extras = append(extras, s)
			}
		}
		sort.Strings(extras)
		for _, s := range extras {
			rows = append(rows, scopeRow{
				short:       customShortName(s),
				full:        s,
				description: "(custom scope)",
				realized:    true,
				target:      true,
				custom:      true,
			})
		}
	}
	// Mark requested rows from the proxy denial list. Append any denied scopes
	// that aren't otherwise in the catalog so the user can see them too.
	if fetchDenied != nil {
		denied := fetchDenied(account)
		denialSet := map[string]bool{}
		for _, s := range denied {
			denialSet[s] = true
		}
		for i := range rows {
			if denialSet[rows[i].full] {
				rows[i].requested = true
				delete(denialSet, rows[i].full)
			}
		}
		// Anything left in denialSet is a brand-new scope the proxy saw that's
		// not in the catalog and not in the keychain — surface it as a custom
		// row so the user can act on it.
		extras := make([]string, 0, len(denialSet))
		for s := range denialSet {
			extras = append(extras, s)
		}
		sort.Strings(extras)
		for _, s := range extras {
			rows = append(rows, scopeRow{
				short:       customShortName(s),
				full:        s,
				description: "(requested by proxy)",
				requested:   true,
				custom:      true,
			})
		}
	}
	return rows, nil
}

// customShortName picks a readable display name for a scope URL not in the
// catalog: the trailing path segment, or the URL itself if there's no path.
func customShortName(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 && i < len(s)-1 {
		return s[i+1:]
	}
	return s
}

// matches returns true if filter (case-insensitive substring) matches either
// the short name or the description.
func (r scopeRow) matches(filter string) bool {
	if filter == "" {
		return true
	}
	f := strings.ToLower(filter)
	return strings.Contains(strings.ToLower(r.short), f) ||
		strings.Contains(strings.ToLower(r.description), f)
}

// scopesFocus is which sub-control inside scopesModel has focus.
type scopesFocus int

const (
	focusSearch scopesFocus = iota
	focusList
)

type scopesModel struct {
	account  string
	rows     []scopeRow
	cursor   int // index into filtered (visible) rows
	filtered []int
	search   textinput.Model
	focus    scopesFocus
	width    int
}

func newScopesModel(account string, rows []scopeRow) scopesModel {
	ti := textinput.New()
	ti.Placeholder = "filter (substring)"
	ti.Prompt = "/ "
	ti.CharLimit = 64
	ti.Width = 40
	ti.Focus()
	m := scopesModel{
		account: account,
		rows:    rows,
		search:  ti,
		focus:   focusSearch,
	}
	m.recomputeFiltered()
	return m
}

func (m *scopesModel) recomputeFiltered() {
	m.filtered = m.filtered[:0]
	q := m.search.Value()
	for i, r := range m.rows {
		if r.matches(q) {
			m.filtered = append(m.filtered, i)
		}
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = 0
		if len(m.filtered) > 0 {
			m.cursor = len(m.filtered) - 1
		}
	}
}

// pendingChanges reports whether target differs from realized for any row.
// In M2 this is always false (no toggles), but the helper is defined now so
// the quit gate logic in M3 has a single source of truth.
func (m scopesModel) pendingChanges() bool {
	for _, r := range m.rows {
		if r.target != r.realized {
			return true
		}
	}
	return false
}

func (m scopesModel) Update(msg tea.Msg) (scopesModel, tea.Cmd) {
	keyMsg, isKey := msg.(tea.KeyMsg)
	if !isKey {
		return m, nil
	}
	switch m.focus {
	case focusSearch:
		switch keyMsg.String() {
		case "down", "enter":
			if len(m.filtered) > 0 {
				m.focus = focusList
				m.search.Blur()
			}
			return m, nil
		case "esc":
			// M2: no pending changes possible, just signal quit.
			return m, func() tea.Msg { return scopesQuitMsg{} }
		case "ctrl+c":
			return m, tea.Quit
		}
		var cmd tea.Cmd
		m.search, cmd = m.search.Update(msg)
		m.recomputeFiltered()
		return m, cmd
	case focusList:
		switch keyMsg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			} else {
				m.focus = focusSearch
				m.search.Focus()
			}
		case "down", "j":
			if m.cursor < len(m.filtered)-1 {
				m.cursor++
			}
		case "/":
			m.focus = focusSearch
			m.search.Focus()
		case "esc":
			m.focus = focusSearch
			m.search.Focus()
		case "q":
			return m, func() tea.Msg { return scopesQuitMsg{} }
		case "ctrl+c":
			return m, tea.Quit
		}
		return m, nil
	}
	return m, nil
}

// scopesQuitMsg signals the top-level model that the scope view wants to
// exit. The top-level decides whether to quit the program or show a prompt.
type scopesQuitMsg struct{}

func (m scopesModel) View() string {
	var b strings.Builder

	// Header
	granted := 0
	for _, r := range m.rows {
		if r.realized {
			granted++
		}
	}
	header := fmt.Sprintf("google / %s — %d of %d granted",
		m.account, granted, len(m.rows))
	b.WriteString(titleStyle.Render(header))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", 60))
	b.WriteString("\n")

	// Search bar
	b.WriteString(m.search.View())
	b.WriteString("\n\n")

	// Rows
	if len(m.filtered) == 0 {
		b.WriteString(mutedStyle.Render("  (no scopes match filter)"))
		b.WriteString("\n")
	}
	for visIdx, rowIdx := range m.filtered {
		r := m.rows[rowIdx]
		check := "[ ]"
		if r.target {
			check = "[x]"
		}
		badge := " "
		if r.requested {
			badge = "!"
		}
		cursor := "  "
		if m.focus == focusList && visIdx == m.cursor {
			cursor = "> "
		}
		line := fmt.Sprintf("%s %s %-25s %s", check, badge, r.short, r.description)
		styled := styleForRow(r, m.focus == focusList && visIdx == m.cursor).Render(line)
		b.WriteString(cursor)
		b.WriteString(styled)
		b.WriteString("\n")
	}

	// Help
	b.WriteString("\n")
	if m.focus == focusSearch {
		b.WriteString(helpStyle.Render("type to filter    ↓/enter: list    esc: quit"))
	} else {
		b.WriteString(helpStyle.Render("↑/↓: navigate    /: search    q/esc: search/quit    ctrl+c: quit"))
	}
	b.WriteString("\n")
	return b.String()
}
