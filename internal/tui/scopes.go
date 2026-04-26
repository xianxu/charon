package tui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/charon/internal/oauth"
	"github.com/xianxu/charon/internal/vault"
	"golang.org/x/term"
)

// scopeRow is one displayable row in the scope view.
type scopeRow struct {
	short       string
	full        string
	description string
	realized    bool
	target      bool
	requested   bool
	custom      bool // not in static catalog (came from keychain or proxy)
	required    bool // structurally required — target is forced true, not togglable
}

// Authenticator is the OAuth dispatch the scope view uses to apply target
// state. Production wires *oauth.GoogleProvider; tests inject stubs.
type Authenticator interface {
	Auth(account string, scopes, existingScopes []string) (*vault.Credential, error)
}

// denialFetcher returns scopes denied for the given account. Best-effort: an
// unreachable proxy must return (nil, nil), not an error.
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
		// Required scopes are force-targeted on, since charon will always
		// include them in the next Auth request regardless of user toggles.
		target := realized
		if info.Required {
			target = true
		}
		rows = append(rows, scopeRow{
			short:       info.Short,
			full:        info.Scope,
			description: info.Description,
			realized:    realized,
			target:      target,
			required:    info.Required,
		})
		seen[info.Scope] = true
	}
	if cred != nil {
		extras := make([]string, 0)
		for _, s := range cred.Scopes {
			if !seen[s] {
				extras = append(extras, s)
				seen[s] = true
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

func customShortName(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 && i < len(s)-1 {
		return s[i+1:]
	}
	return s
}

func (r scopeRow) matches(filter string) bool {
	if filter == "" {
		return true
	}
	f := strings.ToLower(filter)
	return strings.Contains(strings.ToLower(r.short), f) ||
		strings.Contains(strings.ToLower(r.description), f)
}

type scopesFocus int

const (
	focusSearch scopesFocus = iota
	focusList
)

type scopesState int

const (
	stateNormal scopesState = iota
	stateAddCustom
	stateApplying
	stateApplyError
	stateQuitConfirm
)

type scopesModel struct {
	account        string
	rows           []scopeRow
	cursor         int
	filtered       []int
	windowStart    int // first visible row index in m.filtered
	height         int // effective terminal height; 0 means render all rows
	heightOverride int // CHARON_TUI_HEIGHT; if > 0, used in place of WindowSizeMsg
	search         textinput.Model
	custom         textinput.Model
	focus          scopesFocus
	state          scopesState
	applyErr       error
	applyStatus    string // transient message shown after success
	auth           Authenticator
}

// reservedLines is the worst-case fixed chrome around the row list:
// header (1) + separator (1) + search (1) + blank (1) + ↑more (1) + ↓more (1)
// + blank-before-help (1) + help (1) + trailing newline-as-line (1) = 9.
// Picked for the worst case (both above/below indicators showing) so total
// rendered lines stays ≤ height. When fewer indicators show, we under-fill
// by 1-2 lines, which is fine.
const reservedLines = 9

func newScopesModel(account string, rows []scopeRow, auth Authenticator) scopesModel {
	search := textinput.New()
	search.Placeholder = "filter (substring)"
	search.Prompt = "/ "
	search.CharLimit = 64
	search.Width = 40
	search.Focus()

	custom := textinput.New()
	custom.Placeholder = "https://www.googleapis.com/auth/..."
	custom.Prompt = "  url> "
	custom.CharLimit = 256
	custom.Width = 60

	m := scopesModel{
		account: account,
		rows:    rows,
		search:  search,
		custom:  custom,
		focus:   focusSearch,
		auth:    auth,
	}
	// Seed height from the OS so the first frame renders the correct number
	// of rows. bubbletea's WindowSizeMsg may not arrive before the first
	// View() call; without this, we'd over-render and the terminal scrolls
	// the top (header + search bar) off-screen on small panes. Failures
	// (e.g. stdin not a TTY in tests) leave height=0 and recomputeFiltered
	// renders all rows — matching the previous test behavior.
	for _, fd := range []uintptr{os.Stdin.Fd(), os.Stdout.Fd(), os.Stderr.Fd()} {
		if w, h, err := term.GetSize(int(fd)); err == nil && h > 0 {
			m.height = h
			debugf("newScopesModel: term.GetSize(fd=%d) -> w=%d h=%d", fd, w, h)
			break
		} else {
			debugf("newScopesModel: term.GetSize(fd=%d) failed: %v", fd, err)
		}
	}
	// Manual override: terminals (iTerm tabs, tmux panes) sometimes report
	// the parent window height rather than the actual visible area. If the
	// detected height doesn't match what the user sees, they can set
	// CHARON_TUI_HEIGHT=<rows> to override. The override is sticky — it
	// also wins over later WindowSizeMsg events.
	if env := os.Getenv("CHARON_TUI_HEIGHT"); env != "" {
		if n, err := strconv.Atoi(env); err == nil && n > 0 {
			debugf("newScopesModel: CHARON_TUI_HEIGHT override %d -> %d", m.height, n)
			m.heightOverride = n
			m.height = n
		}
	}
	debugf("newScopesModel done: height=%d, total rows=%d", m.height, len(rows))
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
	m.adjustWindow()
}

// visibleRowCount returns how many rows fit in the available terminal space.
// Returns len(m.filtered) when height isn't known yet (initial render before
// WindowSizeMsg) so we don't artificially clip on the first frame.
func (m *scopesModel) visibleRowCount() int {
	if m.height == 0 {
		return len(m.filtered)
	}
	v := m.height - reservedLines
	if v < 1 {
		v = 1
	}
	if v > len(m.filtered) {
		v = len(m.filtered)
	}
	return v
}

// adjustWindow scrolls the visible window so the cursor stays in view.
// Called after cursor moves, filter changes, or terminal resize.
func (m *scopesModel) adjustWindow() {
	visible := m.visibleRowCount()
	if visible <= 0 {
		m.windowStart = 0
		return
	}
	if m.cursor < m.windowStart {
		m.windowStart = m.cursor
	}
	if m.cursor >= m.windowStart+visible {
		m.windowStart = m.cursor - visible + 1
	}
	// Clamp.
	if m.windowStart < 0 {
		m.windowStart = 0
	}
	if m.windowStart+visible > len(m.filtered) {
		m.windowStart = len(m.filtered) - visible
		if m.windowStart < 0 {
			m.windowStart = 0
		}
	}
}

func (m scopesModel) pendingChanges() bool {
	for _, r := range m.rows {
		if r.target != r.realized {
			return true
		}
	}
	return false
}

// diff returns scopes added and removed by current target state.
func (m scopesModel) diff() (added, removed []string) {
	for _, r := range m.rows {
		switch {
		case r.target && !r.realized:
			added = append(added, r.full)
		case !r.target && r.realized:
			removed = append(removed, r.full)
		}
	}
	return added, removed
}

// targetScopes returns the full set the user wants after apply.
func (m scopesModel) targetScopes() []string {
	out := make([]string, 0)
	for _, r := range m.rows {
		if r.target {
			out = append(out, r.full)
		}
	}
	return out
}

// realizedScopes returns the full set currently granted.
func (m scopesModel) realizedScopes() []string {
	out := make([]string, 0)
	for _, r := range m.rows {
		if r.realized {
			out = append(out, r.full)
		}
	}
	return out
}

// applyResultMsg carries the outcome of an OAuth attempt back to the model.
// nil cred + nil err = no-op (e.g. cancelled before dispatch).
type applyResultMsg struct {
	cred *vault.Credential
	err  error
}

// applyCmd builds the tea.Cmd that runs OAuth for the current diff.
//
// M3 supports additive only: any reduction shorts to an error result.
// M4 will replace this branch with the revoke+reauth flow.
func (m scopesModel) applyCmd() tea.Cmd {
	added, removed := m.diff()
	if len(removed) > 0 {
		err := fmt.Errorf("scope reduction lands in M4 (would remove %d scope(s)). "+
			"Untoggle removals to apply additions only", len(removed))
		return func() tea.Msg { return applyResultMsg{err: err} }
	}
	if len(added) == 0 {
		// Nothing to do. Should be unreachable from Enter handler.
		return func() tea.Msg { return applyResultMsg{} }
	}
	if m.auth == nil {
		return func() tea.Msg {
			return applyResultMsg{err: fmt.Errorf("no authenticator configured (use tui.WithAuthenticator)")}
		}
	}
	target := m.targetScopes()
	existing := m.realizedScopes()
	account := m.account
	auth := m.auth
	return func() tea.Msg {
		cred, err := auth.Auth(account, target, existing)
		return applyResultMsg{cred: cred, err: err}
	}
}

func (m scopesModel) Update(msg tea.Msg) (scopesModel, tea.Cmd) {
	// Apply results are delivered regardless of current state.
	if r, ok := msg.(applyResultMsg); ok {
		return m.handleApplyResult(r), nil
	}
	// Window size updates affect rendering regardless of state. The env
	// override (heightOverride) sticks, ignoring the OS-reported height.
	if ws, ok := msg.(tea.WindowSizeMsg); ok {
		debugf("WindowSizeMsg: w=%d h=%d (was h=%d, override=%d)",
			ws.Width, ws.Height, m.height, m.heightOverride)
		if m.heightOverride > 0 {
			m.height = m.heightOverride
		} else {
			m.height = ws.Height
		}
		m.adjustWindow()
		return m, nil
	}

	switch m.state {
	case stateAddCustom:
		return m.updateAddCustom(msg)
	case stateApplying:
		return m.updateApplying(msg)
	case stateApplyError:
		return m.updateApplyError(msg)
	case stateQuitConfirm:
		return m.updateQuitConfirm(msg)
	}

	keyMsg, isKey := msg.(tea.KeyMsg)
	if !isKey {
		return m, nil
	}
	if m.focus == focusSearch {
		return m.updateSearch(keyMsg)
	}
	return m.updateList(keyMsg)
}

func (m scopesModel) updateSearch(msg tea.KeyMsg) (scopesModel, tea.Cmd) {
	switch msg.String() {
	case "down", "enter":
		if len(m.filtered) > 0 {
			m.focus = focusList
			m.search.Blur()
		}
		return m, nil
	case "esc":
		if m.pendingChanges() {
			m.state = stateQuitConfirm
			return m, nil
		}
		return m, func() tea.Msg { return scopesQuitMsg{} }
	case "ctrl+c":
		return m, tea.Quit
	}
	var cmd tea.Cmd
	m.search, cmd = m.search.Update(msg)
	m.recomputeFiltered()
	return m, cmd
}

func (m scopesModel) updateList(msg tea.KeyMsg) (scopesModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
			m.adjustWindow()
			debugf("up: cursor=%d windowStart=%d", m.cursor, m.windowStart)
		} else {
			m.focus = focusSearch
			m.search.Focus()
			debugf("up at top: focus=search")
		}
	case "down", "j":
		if m.cursor < len(m.filtered)-1 {
			m.cursor++
			m.adjustWindow()
			debugf("down: cursor=%d windowStart=%d", m.cursor, m.windowStart)
		}
	case "/":
		m.focus = focusSearch
		m.search.Focus()
	case " ":
		if len(m.filtered) > 0 {
			i := m.filtered[m.cursor]
			if m.rows[i].required {
				m.applyStatus = fmt.Sprintf("%s is required for charon to identify the account.", m.rows[i].short)
			} else {
				m.rows[i].target = !m.rows[i].target
				m.applyStatus = ""
			}
		}
	case "enter":
		if !m.pendingChanges() {
			return m, func() tea.Msg { return scopesQuitMsg{} }
		}
		added, removed := m.diff()
		if len(removed) > 0 {
			m.state = stateApplyError
			m.applyErr = fmt.Errorf("scope reduction lands in M4 (would remove %d scope(s)). "+
				"Untoggle removals to apply additions only", len(removed))
			return m, nil
		}
		_ = added
		m.state = stateApplying
		m.applyErr = nil
		return m, m.applyCmd()
	case "a":
		m.state = stateAddCustom
		m.custom.Reset()
		m.custom.Focus()
		return m, nil
	case "esc":
		if m.pendingChanges() {
			m.state = stateQuitConfirm
			return m, nil
		}
		m.focus = focusSearch
		m.search.Focus()
		return m, nil
	case "q":
		if m.pendingChanges() {
			m.state = stateQuitConfirm
			return m, nil
		}
		return m, func() tea.Msg { return scopesQuitMsg{} }
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func (m scopesModel) updateAddCustom(msg tea.Msg) (scopesModel, tea.Cmd) {
	keyMsg, isKey := msg.(tea.KeyMsg)
	if !isKey {
		return m, nil
	}
	switch keyMsg.String() {
	case "enter":
		raw := strings.TrimSpace(m.custom.Value())
		if raw == "" {
			// Empty input: just exit add-custom mode.
			m.state = stateNormal
			m.custom.Blur()
			return m, nil
		}
		// Refuse if it duplicates an existing row.
		for _, r := range m.rows {
			if r.full == raw {
				m.applyErr = fmt.Errorf("scope %q is already in the list", raw)
				m.state = stateApplyError
				m.custom.Blur()
				return m, nil
			}
		}
		m.rows = append(m.rows, scopeRow{
			short:       customShortName(raw),
			full:        raw,
			description: "(custom scope)",
			target:      true,
			custom:      true,
		})
		m.recomputeFiltered()
		// Move cursor to the new row in the filtered view if it's visible.
		for i, idx := range m.filtered {
			if idx == len(m.rows)-1 {
				m.cursor = i
				break
			}
		}
		m.state = stateNormal
		m.focus = focusList
		m.custom.Blur()
		m.search.Blur()
		return m, nil
	case "esc":
		m.state = stateNormal
		m.custom.Blur()
		// Restore prior focus on list so user can continue navigating.
		m.focus = focusList
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	}
	var cmd tea.Cmd
	m.custom, cmd = m.custom.Update(msg)
	return m, cmd
}

func (m scopesModel) updateApplying(msg tea.Msg) (scopesModel, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	// applyResultMsg is handled in Update directly.
	return m, nil
}

func (m scopesModel) updateApplyError(msg tea.Msg) (scopesModel, tea.Cmd) {
	if _, ok := msg.(tea.KeyMsg); ok {
		// Any key dismisses the error overlay.
		m.state = stateNormal
		m.applyErr = nil
	}
	return m, nil
}

func (m scopesModel) updateQuitConfirm(msg tea.Msg) (scopesModel, tea.Cmd) {
	keyMsg, isKey := msg.(tea.KeyMsg)
	if !isKey {
		return m, nil
	}
	switch keyMsg.String() {
	case "a":
		// Apply pending changes.
		added, removed := m.diff()
		if len(removed) > 0 {
			m.state = stateApplyError
			m.applyErr = fmt.Errorf("scope reduction lands in M4 (would remove %d scope(s)). "+
				"Untoggle removals to apply additions only", len(removed))
			return m, nil
		}
		_ = added
		m.state = stateApplying
		return m, m.applyCmd()
	case "d":
		// Discard pending changes; exit.
		return m, func() tea.Msg { return scopesQuitMsg{} }
	case "c", "esc":
		m.state = stateNormal
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func (m scopesModel) handleApplyResult(r applyResultMsg) scopesModel {
	if r.err != nil {
		m.state = stateApplyError
		m.applyErr = r.err
		return m
	}
	if r.cred != nil {
		// Update rows in place: realized = whatever Google says we have now,
		// target = realized (no pending changes after apply).
		granted := map[string]bool{}
		for _, s := range r.cred.Scopes {
			granted[s] = true
		}
		// Mark rows that match the new credential.
		for i := range m.rows {
			m.rows[i].realized = granted[m.rows[i].full]
			m.rows[i].target = m.rows[i].realized
			delete(granted, m.rows[i].full)
		}
		// Any granted scope still in `granted` is brand-new — append it.
		extras := make([]string, 0, len(granted))
		for s := range granted {
			extras = append(extras, s)
		}
		sort.Strings(extras)
		for _, s := range extras {
			m.rows = append(m.rows, scopeRow{
				short:       customShortName(s),
				full:        s,
				description: "(custom scope)",
				realized:    true,
				target:      true,
				custom:      true,
			})
		}
		m.recomputeFiltered()
		m.applyStatus = "Applied successfully."
	}
	m.state = stateNormal
	return m
}

// scopesQuitMsg signals the top-level model to exit the scope view.
type scopesQuitMsg struct{}

func (m scopesModel) View() string {
	var v string
	switch m.state {
	case stateAddCustom:
		v = m.viewAddCustom()
	case stateApplying:
		v = m.viewApplying()
	case stateApplyError:
		v = m.viewApplyError()
	case stateQuitConfirm:
		v = m.viewQuitConfirm()
	default:
		v = m.viewNormal()
	}
	lineCount := 1
	for _, r := range v {
		if r == '\n' {
			lineCount++
		}
	}
	debugf("View: state=%d focus=%d height=%d cursor=%d windowStart=%d visible=%d filtered=%d/total=%d -> rendered_lines=%d",
		m.state, m.focus, m.height, m.cursor, m.windowStart,
		m.visibleRowCount(), len(m.filtered), len(m.rows), lineCount)
	return v
}

func (m scopesModel) viewNormal() string {
	var b strings.Builder

	granted := 0
	for _, r := range m.rows {
		if r.realized {
			granted++
		}
	}
	header := fmt.Sprintf("google / %s — %d of %d granted", m.account, granted, len(m.rows))
	if m.pendingChanges() {
		added, removed := m.diff()
		header += fmt.Sprintf("   [%d pending: +%d -%d]", len(added)+len(removed), len(added), len(removed))
	}
	b.WriteString(titleStyle.Render(header))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", 60))
	b.WriteString("\n")

	b.WriteString(m.search.View())
	b.WriteString("\n\n")

	visible := m.visibleRowCount()
	end := m.windowStart + visible
	if end > len(m.filtered) {
		end = len(m.filtered)
	}
	// Always emit exactly one "↑" indicator line (empty when no rows above)
	// so every frame has the same line count and bubbletea's render diff
	// doesn't leave stale lines visible.
	if m.windowStart > 0 {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("  ↑ %d more above", m.windowStart)))
	}
	b.WriteString("\n")

	if len(m.filtered) == 0 {
		b.WriteString(mutedStyle.Render("  (no scopes match filter)"))
		b.WriteString("\n")
	}
	rendered := 0
	for visIdx := m.windowStart; visIdx < end; visIdx++ {
		rowIdx := m.filtered[visIdx]
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
		shortDisplay := r.short
		if r.required {
			shortDisplay = r.short + " (req)"
		}
		line := fmt.Sprintf("%s %s %-32s %s", check, badge, shortDisplay, r.description)
		styled := styleForRow(r, m.focus == focusList && visIdx == m.cursor).Render(line)
		b.WriteString(cursor)
		b.WriteString(styled)
		b.WriteString("\n")
		rendered++
	}
	// Pad row area to a constant `visible` lines so the frame size doesn't
	// vary as the user navigates or filters. (No filter case is the
	// exception: the "no matches" message above replaces this padding.)
	if len(m.filtered) > 0 {
		for ; rendered < visible; rendered++ {
			b.WriteString("\n")
		}
	}
	// Always emit exactly one "↓" indicator line (empty when no rows below).
	if end < len(m.filtered) {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("  ↓ %d more below", len(m.filtered)-end)))
	}
	b.WriteString("\n")

	b.WriteString("\n")
	if m.applyStatus != "" {
		b.WriteString(helpStyle.Render(m.applyStatus))
		b.WriteString("\n")
	}
	if m.focus == focusSearch {
		b.WriteString(helpStyle.Render("type to filter    ↓/enter: list    esc: quit"))
	} else {
		b.WriteString(helpStyle.Render("↑/↓: nav    space: toggle    enter: apply    a: add custom    /: search    q: quit"))
	}
	b.WriteString("\n")
	return b.String()
}

func (m scopesModel) viewAddCustom() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Add custom scope URL"))
	b.WriteString("\n\n")
	b.WriteString(m.custom.View())
	b.WriteString("\n\n")
	b.WriteString(helpStyle.Render("enter: add    esc: cancel"))
	b.WriteString("\n")
	return b.String()
}

func (m scopesModel) viewApplying() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Authenticating..."))
	b.WriteString("\n\n")
	b.WriteString("  A browser window should have opened for Google OAuth.\n")
	b.WriteString("  Complete the consent flow there. (ctrl+c to abort)\n")
	return b.String()
}

func (m scopesModel) viewApplyError() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Apply failed"))
	b.WriteString("\n\n")
	if m.applyErr != nil {
		b.WriteString("  ")
		b.WriteString(m.applyErr.Error())
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("press any key to dismiss"))
	b.WriteString("\n")
	return b.String()
}

func (m scopesModel) viewQuitConfirm() string {
	added, removed := m.diff()
	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("You have %d pending change(s)", len(added)+len(removed))))
	b.WriteString("\n\n")
	for _, s := range added {
		b.WriteString(rowAddStyle.Render("  + " + s))
		b.WriteString("\n")
	}
	for _, s := range removed {
		b.WriteString(rowDelStyle.Render("  - " + s))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("[a] apply    [d] discard    [c] cancel"))
	b.WriteString("\n")
	return b.String()
}
