package tui

import (
	"errors"
	"fmt"
	"net/http"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/charon/internal/oauth"
	"github.com/xianxu/charon/internal/providers"
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
	screenProvider     screen = iota // top-level provider picker (post-#13 entry point)
	screenPicker                      // OAuth account picker (Google)
	screenScopes                      // OAuth scope view
	screenAuthing                     // OAuth in flight from the picker; ignore picker keys
	screenAdminKeyList                // admin-key entity list (admin row + projects)
)

// model is the top-level bubbletea model.
type model struct {
	current        screen
	providerPicker providerPickerModel
	picker         pickerModel
	scopes         scopesModel
	adminList      adminKeyListModel

	vault       vault.Store
	auth        Authenticator
	fetchDenied denialFetcher
	proxyAddr   string // for cache-clear notify after vault writes; "" disables

	// adminProviders is keyed by provider name ("openai", "anthropic").
	// Empty map means no admin-key providers registered — the provider
	// picker still renders, just without those rows.
	adminProviders map[string]providers.Provider
	adminStores    map[string]*providers.AdminKeyStore

	// activeAdminProvider is the provider whose entity list is on
	// screen when current==screenAdminKeyList. Used for re-rendering
	// the list after a vault/store mutation lands.
	activeAdminProvider string

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

// WithAdminKeyProvider registers an admin-key provider (OpenAI,
// Anthropic). The provider's Name() determines the keychain namespace
// and the picker label; all admin-key providers are auto-paired with
// an AdminKeyStore for the same name. Multiple calls are additive —
// register one provider per call.
func WithAdminKeyProvider(p providers.Provider) Option {
	return func(m *model) {
		if m.adminProviders == nil {
			m.adminProviders = make(map[string]providers.Provider)
			m.adminStores = make(map[string]*providers.AdminKeyStore)
		}
		m.adminProviders[p.Name()] = p
		m.adminStores[p.Name()] = providers.NewAdminKeyStore(p.Name())
	}
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
	m := model{vault: v}
	for _, opt := range opts {
		opt(&m)
	}

	// initialAccount short-circuits the provider picker: it's a
	// pre-#13 escape hatch for "open scope view directly for this
	// google account" used by Run(v, "user@gmail.com", …). Implies
	// the OAuth/Google flow.
	if initialAccount != "" {
		rows, err := loadScopeRows(v, initialAccount, m.fetchDenied)
		if err != nil {
			return model{}, err
		}
		m.scopes = newScopesModel(initialAccount, rows, m.auth)
		m.current = screenScopes
		return m, nil
	}

	pp, err := newProviderPickerModel(v, m.adminStores)
	if err != nil {
		return model{}, err
	}
	m.providerPicker = pp
	m.current = screenProvider
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

	case providerSelectedMsg:
		return m.handleProviderSelected(msg)

	case addProviderMsg:
		// Phase 1 stub. Catalog (#15) will wire the catalog picker
		// here. Today: re-render the provider picker with a status
		// hint baked into the label (no separate flash mechanism yet
		// — keeping the picker stateless until #15 motivates one).
		return m, nil

	case adminKeyListBackMsg:
		return m.refreshProviderPicker()

	case pickerBackMsg:
		return m.refreshProviderPicker()

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
		// scopesQuitMsg used to terminate the program. With the
		// provider picker as the new top-level, exiting the scope
		// view returns to the OAuth account picker. The user has to
		// `q` from the provider picker (or chain `q`s up the stack)
		// to actually exit. Initial-account mode (skipped the picker)
		// still terminates here.
		if m.current == screenScopes && m.picker.items != nil {
			m.current = screenPicker
			return m, nil
		}
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
		// Track whether Google actually revoked the token here, vs. the
		// token being already-invalid on Google's side. Either way we
		// proceed to delete the local entry — the user wants this
		// account *gone* — but the exit note is honest about what
		// happened upstream.
		alreadyRevoked := false
		if m.auth != nil {
			err := m.auth.Revoke(cred.RefreshToken)
			switch {
			case err == nil:
				// Revoked at Google.
			case errors.Is(err, oauth.ErrAlreadyRevoked):
				// Token was already revoked or never valid (e.g. user
				// revoked via myaccount.google.com/permissions before
				// reaching us). Local cleanup still wanted.
				alreadyRevoked = true
			default:
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
		if alreadyRevoked {
			m.exitNote = "Removed " + msg.account + " (already revoked on Google's side)"
		} else {
			m.exitNote = "Revoked and removed " + msg.account
		}
		return m, tea.Quit
	}

	switch m.current {
	case screenProvider:
		var cmd tea.Cmd
		m.providerPicker, cmd = m.providerPicker.Update(msg)
		return m, cmd
	case screenPicker:
		var cmd tea.Cmd
		m.picker, cmd = m.picker.Update(msg)
		return m, cmd
	case screenAdminKeyList:
		var cmd tea.Cmd
		m.adminList, cmd = m.adminList.Update(msg)
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

// handleProviderSelected routes from the provider picker to the
// per-type screen for the chosen provider. OAuth → existing
// pickerModel; admin-key → adminKeyListModel.
func (m model) handleProviderSelected(msg providerSelectedMsg) (tea.Model, tea.Cmd) {
	switch msg.provType {
	case vault.TypeOAuth:
		// Today only Google has an OAuth flow registered. Build (or
		// rebuild) the OAuth account picker.
		p, err := newPickerModel(m.vault)
		if err != nil {
			m.err = err
			return m, tea.Quit
		}
		m.picker = p
		m.current = screenPicker
		return m, nil
	case vault.TypeAdminKey:
		store := m.adminStores[msg.name]
		l, err := newAdminKeyListModel(msg.name, m.vault, store)
		if err != nil {
			m.err = err
			return m, tea.Quit
		}
		m.adminList = l
		m.activeAdminProvider = msg.name
		m.current = screenAdminKeyList
		return m, nil
	}
	return m, nil
}

// refreshProviderPicker rebuilds the provider picker (state may have
// changed: an OAuth account was added, an admin key was set, etc.)
// and returns to that screen. Called when navigating back from any
// per-provider sub-screen.
func (m model) refreshProviderPicker() (tea.Model, tea.Cmd) {
	pp, err := newProviderPickerModel(m.vault, m.adminStores)
	if err != nil {
		m.err = err
		return m, tea.Quit
	}
	m.providerPicker = pp
	m.current = screenProvider
	return m, nil
}

func (m model) View() string {
	switch m.current {
	case screenProvider:
		return m.providerPicker.View()
	case screenPicker:
		return m.picker.View()
	case screenAdminKeyList:
		return m.adminList.View()
	case screenAuthing:
		return "\nAuthenticating with Google...\n\n" +
			"  A browser window should have opened for OAuth.\n" +
			"  Complete the consent flow there. (ctrl+c to abort)\n"
	case screenScopes:
		return m.scopes.View()
	}
	return ""
}
