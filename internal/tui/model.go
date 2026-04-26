package tui

import (
	"fmt"
	"net/http"

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
	screenAuthing // OAuth in flight from the picker; ignore picker keys
)

// model is the top-level bubbletea model.
type model struct {
	current screen
	picker  pickerModel
	scopes  scopesModel

	vault       vault.Store
	auth        Authenticator
	fetchDenied denialFetcher
	proxyAddr   string // for cache-clear notify after vault writes; "" disables

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

// WithProxyAddr lets the model notify the running charon proxy to flush
// its credential cache after vault writes (apply, revoke). Without this,
// the proxy continues serving stale tokens whose scope set predates the
// most recent OAuth dance, and agents see 407s for scopes the user just
// granted. Empty addr is fine — caller may not be running the proxy.
func WithProxyAddr(addr string) Option {
	return func(m *model) { m.proxyAddr = addr }
}

// notifyProxyCacheClear pings the proxy at proxyAddr to flush its
// in-memory token + account cache. Best-effort; failure means the proxy
// isn't running locally, which is fine.
func (m model) notifyProxyCacheClear() {
	if m.proxyAddr == "" {
		return
	}
	url := fmt.Sprintf("http://%s/cache/clear", m.proxyAddr)
	resp, err := http.Post(url, "", nil)
	if err == nil {
		resp.Body.Close()
	}
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
		if m.current == screenAuthing {
			// A second newAccountMsg can fire if the user mashes Enter
			// before the first OAuth completes — picker's Update is still
			// running. Drop the duplicate.
			return m, nil
		}
		if m.auth == nil {
			m.err = fmt.Errorf("no authenticator configured")
			return m, tea.Quit
		}
		auth := m.auth
		m.current = screenAuthing
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
		//
		// Guard against account drift: if the user authenticated as a
		// different Google account than what the scope view is editing,
		// surface as an error rather than silently writing a new vault
		// entry under a different key (which would leave the original
		// untouched and confusingly leak rows from the wrong account into
		// the displayed view).
		if msg.err == nil && msg.cred != nil {
			if m.current == screenScopes && m.scopes.account != "" && msg.cred.Account != m.scopes.account {
				msg.err = fmt.Errorf("authenticated as %s, expected %s — original credential left untouched",
					msg.cred.Account, m.scopes.account)
			} else if err := m.vault.Set(msg.cred); err != nil {
				msg.err = err
			} else {
				// Flush the proxy's token cache so the next request uses
				// the freshly-stored credential (with the just-granted
				// scopes). Otherwise the proxy keeps serving the cached
				// pre-grant token and agents see 407 for scopes the user
				// already granted.
				m.notifyProxyCacheClear()
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
		m.notifyProxyCacheClear() // proxy must drop the now-revoked token
		m.exitNote = "Revoked and removed " + msg.account
		return m, tea.Quit
	}

	switch m.current {
	case screenPicker:
		var cmd tea.Cmd
		m.picker, cmd = m.picker.Update(msg)
		return m, cmd
	case screenAuthing:
		// Block all picker/scopes input while OAuth is in flight; only
		// ctrl+c reaches us here so the user can still abort the program.
		if k, ok := msg.(tea.KeyMsg); ok && k.String() == "ctrl+c" {
			return m, tea.Quit
		}
		return m, nil
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
	case screenAuthing:
		return "\nAuthenticating with Google...\n\n" +
			"  A browser window should have opened for OAuth.\n" +
			"  Complete the consent flow there. (ctrl+c to abort)\n"
	case screenScopes:
		return m.scopes.View()
	}
	return ""
}
