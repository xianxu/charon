# Plan: Scope management TUI (#000005)

This plan elaborates the *how* for #000005. The *what* lives in the issue.

## Architecture overview

### New package: `internal/tui/`

```
internal/tui/
├── tui.go              # entry point: Run(account string) error
├── model.go            # top-level model + screen routing
├── picker.go           # account picker sub-model
├── scopes.go           # scope view sub-model (the main screen)
├── modal.go            # warning gate + revoke confirm + custom scope input
├── styles.go           # lipgloss style definitions (granted, muted, badge)
├── apply.go            # diff logic (target vs realized → action)
└── *_test.go           # teatest-based interaction tests
```

Why a new package: the TUI is a self-contained UX module. Keeping it out of
`cmd/charon` lets the CLI stay thin (cobra command → call into `tui.Run`).

### Dependencies to add

```
github.com/charmbracelet/bubbletea
github.com/charmbracelet/bubbles
github.com/charmbracelet/lipgloss
```

All pure Go. Confirmed compatible with single-binary principle.

## Bubbletea model design

### Top-level model

```go
type screen int
const (
    screenPicker screen = iota
    screenScopes
)

type Model struct {
    current  screen
    picker   pickerModel
    scopes   scopesModel
    width    int
    height   int
    quitting bool
    err      error
}
```

`Update` routes messages to the active sub-model and listens for transition
messages (e.g. `accountSelectedMsg{email string}`, `backToPickerMsg`).

### pickerModel (account picker)

- Built on `bubbles/list` with custom item renderer
- Items: existing accounts (rendered with scope count) + `+ new account`
  always at bottom
- Enter on existing account: emits `accountSelectedMsg{email}`
- Enter on `+ new account`: emits `newAccountMsg{}` → triggers OAuth with no
  `login_hint`, on success emits `accountSelectedMsg{discoveredEmail}`

### scopesModel (the main screen)

```go
type scopeRow struct {
    short       string   // e.g. "calendar.readonly"
    full        string   // e.g. "https://www.googleapis.com/auth/..."
    description string
    realized    bool     // currently granted in keychain
    target      bool     // user-toggled state in TUI
    requested   bool     // present in /scopes/denied ring buffer
    custom      bool     // not in static catalog (added at runtime)
}

type scopesModel struct {
    account  string
    rows     []scopeRow
    cursor   int
    state    scopesState  // normal | confirmReduction | addCustom | revokeAll
    input    textinput.Model     // for custom scope URL
    err      error
}
```

Actions and key bindings (per issue):

| Key | Action |
|---|---|
| `↑` / `↓` | Move `cursor` |
| `space` | Toggle `rows[cursor].target` |
| `enter` | Apply target (transition to `confirmReduction` or run dance) |
| `q` / `esc` | Quit without applying |
| `a` / `/` | Enter `addCustom` state, focus textinput |
| `R` | Enter `revokeAll` state for confirmation |

### Modals as state, not separate models

Bubbletea convention: model an "overlay" by adding a state enum to the
parent and switching `View()` to render the overlay when active. Cleaner than
nested sub-models for short-lived prompts.

## State management: realized vs target

### Loading

When `scopesModel` is initialized for an account:

1. Load `cred, _ := vault.Get("google", account)` → `cred.Scopes` is **realized**
2. `target` starts as a copy of realized for each catalog row
3. Catalog rows that aren't in keychain: `realized = false, target = false`
4. Keychain scopes that aren't in catalog: append as `custom = true,
   realized = true, target = true`
5. Fetch `/scopes/denied?provider=google&account=<email>` → mark matching
   rows with `requested = true`. If proxy unreachable: skip silently, no
   badges.

### Diff and apply paths (`apply.go`)

```go
type Action int
const (
    ActionNoop Action = iota
    ActionIncremental
    ActionReduction
)

func Diff(realized, target []string) (action Action, added, removed []string) {
    rs, ts := setOf(realized), setOf(target)
    added   = ts.minus(rs)
    removed = rs.minus(ts)
    switch {
    case len(added) == 0 && len(removed) == 0:
        return ActionNoop, nil, nil
    case len(removed) == 0:
        return ActionIncremental, added, nil
    default:
        return ActionReduction, added, removed
    }
}
```

- **Noop**: exit cleanly, no browser, no keychain write
- **Incremental**: call existing `oauth.GoogleProvider.Auth(account, target,
  realized)`. The current `Auth()` already handles incremental via
  `include_granted_scopes=true`. Then `vault.Set(newCred)`.
- **Reduction**: see next section.

## The reduction flow

### Constraint

Google maintains *one* authorization grant per `(client_id, end_user)`. There
is no API to "remove" individual scopes from a grant. To reduce:

1. Revoke the existing token (kills the grant)
2. Re-auth with the desired smaller set

### Open question — must verify in M4

Does running `Auth` with `prompt=consent` and a *subset* of granted scopes
create a fresh grant, or extend the existing one? Behavior matters for
ordering:

- **If fresh grant**: re-auth first, then revoke old → safer (failure of
  re-auth leaves old credential intact)
- **If extends existing**: revoke first, then re-auth → only path that
  actually reduces the grant. Failure of re-auth = user logged out.

**Plan**: build a small experiment in M4 to determine empirically (auth with
[A,B,C], then auth with [A,B] + `prompt=consent` while keeping the old token
— check whether the old token still works). Document the answer in the
issue's `## Log` section.

### Recommended sequence (assuming "extends existing")

```
1. Modal warning gate. User confirms.
2. Revoke old refresh_token via https://oauth2.googleapis.com/revoke
3. Run Auth(account, target, nil /*no merge*/) with prompt=consent
4. On success: vault.Set(newCred)
5. On failure: surface error; user must re-run charon auth google to recover
```

We accept the failure window as the documented cost (per issue's warning
gate text).

### If "fresh grant" turns out to be true

Reorder: re-auth first → on success, atomically replace credential → revoke
old token in the background (best-effort; ignore failure). Much safer.

### Implementation

New method on `oauth.GoogleProvider`:

```go
// Revoke calls Google's revoke endpoint. Safe to call with an expired token.
func (g *GoogleProvider) Revoke(refreshToken string) error
```

Modify `Auth` to accept a `prompt` parameter (or expose a `forceConsent`
flag), default to current behavior. Plumbed only from the reduction path.

## Custom scope input

### UX

Press `a` → cursor switches to a textinput at the bottom:

```
  Add custom scope URL:
> https://www.googleapis.com/auth/_______________
  enter: add    esc: cancel
```

On Enter:
- Validate it looks like a URL (rough regex: starts with `https://` or is a
  bare token like `openid`)
- Append a new `scopeRow{custom: true, short: <last path segment>, full:
  <input>, target: true}` to `rows`
- Move cursor to the new row
- Return to normal mode

### Persistence

Custom scopes aren't persisted in any catalog. They live only in the
keychain credential's `Scopes` field after grant. On next TUI launch, loader
appends them as custom rows (step 4 of state loading).

## Account picker integration with new-account OAuth

### `+ new account` flow

1. User selects `+ new account` in picker
2. TUI sends `tea.Cmd` that calls `oauth.NewGoogleProvider().Auth("", []string{}, nil)`
   — empty account, empty scopes (only `openid email` from `requiredGoogleScopes`)
3. Browser opens, user picks account, consents to `openid email`
4. Auth returns credential with `cred.Account = <discovered email>`
5. TUI receives `accountSelectedMsg{email: cred.Account}` and transitions to
   scope view with realized = `[openid, email]`
6. User toggles desired scopes in scope view → applies → another OAuth dance

This means new-account onboarding takes 2 browser interactions: first to
identify the account (openid+email only), second to grant chosen scopes.

### Alternative: combine into one flow

Could instead show the scope TUI *before* the first OAuth, let the user
choose scopes, then a single dance asks for `openid email + chosen`.
Tradeoff: until OAuth completes we don't know the account email, so the
header would say "google / (new account)" until success. Probably fine.

**Decision**: combine. One browser dance per new-account flow. Header just
says "google / new account" until the email comes back.

## Testing strategy

### `teatest` for interaction tests

`github.com/charmbracelet/x/exp/teatest` lets us drive a model with synthetic
key presses and assert on rendered output.

```go
func TestScopeToggle(t *testing.T) {
    m := scopes.New("test@gmail.com", fakeVault, fakeProxyClient)
    tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))
    tm.Send(tea.KeyMsg{Type: tea.KeyDown})
    tm.Send(tea.KeyMsg{Type: tea.KeySpace})
    tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
    // Assert: incremental Auth was called with [calendar.readonly]
}
```

### Test categories

1. **Diff logic** (pure, no TUI): table-driven tests for `Diff()` function
2. **Picker rendering**: empty / one account / many accounts, cursor on `+ new`
3. **Scope view rendering**: realized states, badges, muted off, custom rows
4. **Toggle**: space changes target, doesn't touch realized, exit-without-apply discards
5. **Apply paths**: noop exits cleanly, incremental calls Auth, reduction transitions to modal
6. **Custom scope input**: `a` opens prompt, Enter appends row, Esc cancels
7. **Modal flows**: reduction warning continue/cancel, revoke-all confirm

### Mocks

- `oauth.Provider` interface (already exists conceptually; may need to lift
  it explicitly to inject fakes)
- Proxy client for `/scopes/denied`: small interface with one method
- Vault: existing `vault.Store` interface, use `memory` backend

## Integration points

### Files modified

| File | Change |
|---|---|
| `cmd/charon/main.go` | Rewrite `authGoogleCmd` to launch TUI; **delete** `authGoogleScopesCmd`, `authGoogleGrantCmd`, `authGoogleFixCmd`, `authFixCmd` |
| `internal/oauth/google.go` | Empty `DefaultGoogleScopes`; add `Revoke()`; add `forceConsent` plumbing |
| `internal/oauth/google_test.go` | Update default-scopes test; add Revoke test |
| `atlas/charon.md` | Replace CLI section + scope management section to reflect TUI |

### Files added

| File | Purpose |
|---|---|
| `internal/tui/*.go` | New package per architecture above |
| `go.mod` / `go.sum` | bubbletea + lipgloss + bubbles |

### Files deleted

None — `internal/oauth/scope_catalog.go` stays (TUI consumes the catalog).

## Risks and unknowns

1. **Grant semantics for reduction** (M4 investigation needed). Outcome
   determines whether the reduction sequence is revoke-first or re-auth-first.
2. **`teatest` ergonomics** — first time using it. May need an integration
   test fallback if assertions on rendered output prove brittle.
3. **Terminal compatibility** — bubbletea is mature here, but charon may run
   in non-TTY contexts (e.g. piped, in CI). Need to detect non-TTY and
   either error cleanly or fall back to a non-interactive path. Probably
   error: `charon auth google` is inherently interactive.
4. **Race between user toggle and proxy badge update** — badges fetched once
   at TUI launch; if the agent makes a new request while the TUI is open,
   the badge state is stale. Acceptable; refresh on `r` key as polish later.

## Milestones (matches issue Plan)

Plan ordering trades off "minimum viable demo" against "deliverable quality":

- **M1**: TUI scaffolding + picker only — runs, navigates, exits. No scope
  view. Validates bubbletea integration, teatest setup, single-binary build.
- **M2**: Scope view rendering, view-only — load realized + badge, render.
  No mutations yet. Validates state loading, lipgloss styling.
- **M3**: Toggle + apply (additive paths only). Reduction not yet allowed —
  if user attempts, show "not yet implemented" message. Most user value
  ships here: `auth google` is functional.
- **M4**: Reduction flow with grant-semantics investigation, modal warning,
  revoke endpoint, full reduction path.
- **M5**: Cleanup: delete replaced commands, empty `DefaultGoogleScopes`,
  update atlas, update help text. **Code review subagent at this boundary**
  per AGENTS.md §3.

Each milestone closes with the issue file's `## Plan` checkbox flipped and
a `## Log` entry. Code review is mandatory at M5 close.
