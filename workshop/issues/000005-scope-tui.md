---
id: 000005
status: working
deps: [000004]
github_issue:
created: 2026-04-26
updated: 2026-04-26
---

# Scope management TUI

Detailed implementation design: [000005-scope-tui-plan.md](../plans/000005-scope-tui-plan.md)

## Problem

`charon auth google` today bundles arbitrary defaults (`gmail.readonly`) into the
first-time consent screen — overreach for users who don't want Gmail access, and
muddies the M3 promise that "scopes are explicit, on-demand, scope-on-demand."

The current scope-management surface is also fragmented across three commands
that all manipulate the same underlying state from different angles:

- `charon auth google scopes [email]` — read catalog or granted scopes
- `charon auth google grant <email> <scope>` — add scopes via incremental OAuth
- `charon auth google fix [email]` — resolve denied scopes from proxy 407s

There's no canonical "open this account, look at what's granted, change what I
want" UX. There's also no way to *reduce* granted scopes. And the suppress/deny
sub-feature from #000004 was deferred specifically because the interactive UX
needed more thought.

This issue lands that thought: a single TUI as the canonical UX for scope
management, replacing all three commands above.

## Spec

### Two states, one signal

Each scope row is in one of two **states**:

- **on** — granted (matches `credential.Scopes` in keychain). Normal color.
- **off** — not granted. Muted color.

Layered orthogonally on either state: a **requested badge** (`!`) sourced from
the proxy ring buffer (`/scopes/denied`). 24h TTL — if the agent stops asking,
the badge fades. If it keeps asking, it keeps reappearing. Self-regulating
without persistent suppression.

There is **no deny/suppressed state**. The original #000004 spec had a third
state (Suppressed, persisted to a config file). It's dropped — adds complexity
without clear value once the badge has a TTL. If the user keeps seeing a badge
they don't want to grant, they ignore it; if it keeps reappearing across days,
that's signal worth surfacing, not silently swallowing.

### State model: realized vs target

- **Realized state** — `credential.Scopes` in keychain. Google's last word.
- **Target state** — the TUI's in-memory representation after user toggles.
  Ephemeral. Lost on quit-without-apply. Never persisted on its own.

**Apply** reconciles target → Google → on success, new credential.Scopes
becomes the new realized.

| target vs realized | action |
|---|---|
| target == realized | no-op (Enter exits cleanly, no browser) |
| target ⊋ realized (only additions) | incremental OAuth, single browser dance, additive |
| target has any reduction | revoke + re-auth flow with explicit warning gate (see below) |

### Reduction is asymmetric and must be explicit

Google's OAuth grant is unified per `(client_id, end_user)` — there's no API to
shed individual scopes. To actually reduce a grant:

1. Call Google's revoke endpoint → grant goes to ∅ for this app
2. Re-auth with the desired (smaller) scope set → user re-consents

Step 2 can fail (user closes the browser tab) → the account ends up logged out.
This is unavoidable. Mitigation is to surface it explicitly:

```
You're removing 1 scope (drive.readonly).
Google requires a full re-authorization to reduce scopes.
You'll be sent through the consent screen for the remaining scopes.
If you cancel, this account will be logged out.
[Continue] [Cancel]
```

Adding scopes = silent. Removing scopes = explicit gate. Asymmetry honest.

### Account picker

`charon auth google` opens a picker first:

```
  Google accounts
  ───────────────
  xianxu@gmail.com    (3 scopes)
  other@gmail.com     (1 scope)
> + new account
```

- Existing accounts listed at top
- `+ new account` always present, at the bottom (so existing accounts land
  under the cursor by default with arrow-up)
- Selecting an existing account → scope TUI for that account
- Selecting `+ new account` → OAuth with no `login_hint` → user picks Google
  account in browser → ID token gives email → email becomes account key →
  jumps to scope TUI with all `[ ]` (initial-auth = degenerate case of the
  general flow)

### Scope TUI (per account)

```
  google / xianxu@gmail.com — 2 of 12 granted
  ────────────────────────────────────────────
  [x] gmail.readonly         Read Gmail messages
  [x] calendar.readonly      Read Google Calendar events
  [ ] calendar               Manage Google Calendar events     !
  [ ] drive.readonly         Read Google Drive files
  [ ] drive                  Manage Google Drive files
  [ ] contacts.readonly      Read contacts
  ...
  [ ] + add custom scope URL

  space: toggle    enter: apply    a: add custom    R: revoke account    q: quit
```

- All catalog scopes listed
- Any non-catalog scopes from the keychain are also listed (so the TUI is
  never strictly less powerful than the keychain)
- `!` badge on rows currently in the proxy ring buffer
- Granted rows in normal color, ungranted rows muted
- Currently-targeted rows that differ from realized rendered subtly (e.g. `*`
  prefix) so the user can see pending changes before applying

### Key bindings

| Key | Action |
|---|---|
| `↑` / `↓` | Move cursor |
| `space` | Toggle current row (on ↔ off) — affects target only |
| `enter` | Apply target — runs the diff (no-op / incremental / revoke+reauth) |
| `q` / `esc` | Quit without applying — discards target |
| `a` (or `/`) | Add a custom scope URL not in the catalog |
| `R` | Revoke entire account (capital, scary, separate confirmation) |

### Read-only path

There is no separate read-only mode. If you opened it just to look, hit `q`.
Target gets discarded, keychain untouched, no browser. The "view" use case is
just "open it and quit."

### Custom scope URL

`a` prompts for a free-form scope URL. Appended to the list as `[x]` (target
on). If subsequently applied, the scope is granted; the URL is *not* added to
the static catalog, but it appears on subsequent TUI loads because the
keychain has it.

### Default scope behavior changes

- `requiredGoogleScopes = [openid, email]` — kept (still required for ID-token
  email extraction)
- `DefaultGoogleScopes = [gmail.readonly]` — **deleted**. First-time auth no
  longer presumes any data scope. The TUI is how users opt in.

### Commands replaced

`charon auth google` → opens the TUI (account picker → scope view).

The following commands are **deleted**:
- `charon auth google scopes [email]` — TUI is the canonical viewer
- `charon auth google grant <email> <scope>` — TUI is the canonical mutator
- `charon auth google fix [email]` — TUI shows badges from `/scopes/denied`
- `charon auth fix` — same; cross-account variant becomes the picker UX

The proxy-side ring buffer and `/scopes/denied` endpoint stay (they feed the
badge). `X-Charon-Scope` enforcement stays.

### Not in scope

- Persistent suppression / deny config (dropped per simplification above)
- Multi-provider (Dropbox, etc.) variants — design is provider-generic but v1
  ships Google only since that's all charon supports today
- Tabbed multi-account view — picker for v1

## Plan

- [x] **M1: TUI scaffolding + account picker**
  - Pick TUI library (likely `bubbletea` — well-supported, idiomatic Go)
  - Account picker that lists accounts from vault + `+ new account`
  - Selection wires into the OAuth flow when no `login_hint` is needed
  - Tests: picker renders, navigation, selection routes correctly

- [x] **M2: Scope TUI — view-only**
  - Render scope list per account (catalog + any keychain extras + any
    proxy-denied scopes not in catalog)
  - Realized state from keychain → `[x]` / `[ ]` rendering
  - Color matrix: muted grey (off, no badge), muted yellow (off, requested),
    normal (granted), green (off→on, M3+), red (on→off, M3+)
  - Always-on search input at top with focus model: search↔list via Down /
    Enter / Up at top / `/` / Esc
  - Substring filter on short+description, case-insensitive
  - Esc routes per focus (list→search, search→quit signal)
  - Tests: rendering, badge correctness, filter behavior, focus transitions

- [x] **M3: Toggle + apply (additive only)**
  - Space toggles target state
  - Pending-change indicator (header shows `[N pending: +A -R]`; rows
    light up green/red via the M2 color matrix)
  - Enter:
    - target == realized → exit no-op
    - target ⊋ realized → run incremental OAuth via existing `Auth()` path
    - any reduction → reject with M4 message (full reduction in M4)
  - Custom scope URL via `a`
  - Quit confirm modal when pending changes (`a` apply / `d` discard /
    `c` cancel)
  - Tests: target/realized diff logic, no-op exit, incremental dance

- [x] **M4: Reduction + revoke**
  - Detect `target ⊊ realized` or mixed (any reduction); route through
    `stateReduceConfirm` modal asking continue/cancel
  - On continue: re-auth with `forceFresh=true` (no revoke needed). Google
    issues a token scoped exactly to the target set via
    `include_granted_scopes=false`. Credential is atomically replaced via
    the existing applyResultMsg → vault.Set path. **Tradeoff**: the
    underlying Google grant on Google's side may still list the wider
    scope set (cosmetic; charon's stored token can't access it).
  - `R` from list: opens `stateRevokeConfirm` for full account nuking.
    Confirm → `Revoke(refreshToken)` + `vault.Delete` + exit.
  - Tests: reduction routes to modal (not auto-apply), continue
    dispatches with forceFresh=true, additive uses forceFresh=false,
    revoke key opens modal, revoke confirm emits message, top-level
    handles revoke (Revoke called, vault deleted, exit).

- [ ] **M5: Cleanup of replaced commands + defaults**
  - Delete `scopes`, `grant`, `fix` cobra commands
  - `DefaultGoogleScopes = []` (kept var so external callers compile, but
    empty)
  - Update `atlas/charon.md`
  - Update help text on `charon auth google`
  - Tests for command removal (help no longer lists them)

## Log

### 2026-04-26 — M4 complete

Decided against the "revoke + scary warning + lockout risk" path
originally specced. Simpler approach: re-auth with `forceFresh=true`
which sets `include_granted_scopes=false` so Google issues a token
scoped exactly to the requested (smaller) set. Old refresh_token is
discarded; charon can no longer exercise the dropped scopes. The
underlying grant on Google's side technically retains the wider set
(cosmetic). User confirmation modal explains "you'll see a fresh
consent screen" — not a lockout warning.

Separate `R` key for full revoke: calls Google's revoke endpoint and
deletes the credential. That's the explicit "I'm done with this app"
action, where lockout is the desired outcome.

State machine: stateNormal → (Enter) → stateReduceConfirm (if
reductive) or stateApplying (if additive). y/enter dispatches with
forceFresh, n/esc/c returns to normal. R key gives a parallel flow
through stateRevokeConfirm → revokeAccountMsg.

Authenticator interface gained Revoke(refreshToken). GoogleProvider
implements both. Scopes test added a stub recording forceFresh and
revoke calls. 8 new tests; 50+ TUI tests total; full suite green.

### 2026-04-26 — M3 follow-up: OIDC scope rewriting + required scopes

User testing surfaced two real issues with how `email` was handled:
1. Google rewrites the OIDC short scope `email` to its full URL form
   `https://www.googleapis.com/auth/userinfo.email` in token responses.
   The catalog had `email` as the Scope field, so after auth the rewritten
   URL didn't round-trip — gmail.readonly grants would render correctly,
   but the email row stayed unchecked and a phantom `userinfo.email`
   custom row appeared.
2. `requiredGoogleScopes` was always force-merged at the oauth layer, but
   the TUI knew nothing about it. Toggling openid/email off in the TUI
   was a lie: apply would re-include them via `mergeScopes` regardless.

Fixes:
- Catalog `email` entry uses the full `userinfo.email` URL (short name
  stays). `requiredGoogleScopes` updated to match — request and response
  now use the same string.
- `ScopeInfo` gains a `Required bool` field; openid + email marked.
- `scopeRow.required` propagates from the catalog. Required rows force
  `target = true` on load; space-key is a no-op with status message
  ("openid is required for charon to identify the account").
- Render: required rows display with `(req)` suffix.
- Apply path: `m.auth == nil` no longer panics — returns a clean error.

Cleaner UX consequence: on a fresh keychain, openid/email show as +2
pending — honest representation that the next apply will grant them.
After first auth, no diff. Tests updated to use a `vaultWithBase` helper
for the common "no pending" baseline.

35 TUI tests; full suite green.

### 2026-04-26 — M3 complete

- `Authenticator` interface lifted; production wires `*oauth.GoogleProvider`,
  tests inject a `stubAuth` recorder. `scopesModel` holds the auth and
  builds a `tea.Cmd` for OAuth dispatch on Enter.
- State machine: `stateNormal | stateAddCustom | stateApplying |
  stateApplyError | stateQuitConfirm`. State takes priority over focus —
  search/list focus only matters in `stateNormal`.
- Space toggles `target` on the cursor row. Header shows pending-change
  count when target ≠ realized.
- Enter dispatches:
  - no diff → quit signal
  - additive only → `applyCmd()` returns a `tea.Cmd` that runs OAuth in a
    goroutine and emits `applyResultMsg{cred, err}`
  - any reduction → synchronously rejects with stateApplyError + M4
    message (no auth call)
- Top-level model intercepts `applyResultMsg` for vault.Set side effect,
  then forwards to scopesModel. `handleApplyResult` updates rows in place
  (realized = new, target = realized) and appends any brand-new scopes
  Google returned that we didn't ask for.
- `a` enters add-custom mode: `bubbles/textinput` for the URL, Enter
  validates+appends row (target=true, custom=true), Esc cancels,
  duplicates rejected with stateApplyError.
- Quit confirm modal: Esc-from-search or q-from-list with pending changes
  goes to `stateQuitConfirm`. `a` applies (same code path), `d` discards
  and quits, `c` returns to normal. Without pending changes, quit is
  immediate (no prompt).
- 12 new tests (toggle, no-op enter, additive apply happy path,
  reduction-rejected, error dismiss, custom append/cancel/dup, all four
  quit-confirm paths, model forwarding + vault persist). 33 TUI tests
  total; full suite green.

### 2026-04-26 — M2 complete

- `scopesModel` renders catalog + keychain extras + denied-scope rows in
  one unified list. Loader handles all three sources and dedupes by full
  scope URL.
- Color matrix wired in `styleForRow`: muted grey, muted yellow (badge),
  normal granted; green/red diff styles defined but unused until M3.
- Always-on search at top via `bubbles/textinput`. Focus toggles between
  search and list with the keys we agreed: Down/Enter→list, Up at top of
  list→search, `/` from list→search, Esc list→search, Esc search→quit
  signal, q in list→quit signal, Ctrl+C anywhere→hard quit.
- `denialFetcher` is an injectable interface; production wires
  `httpDenialFetcher(addr)` against `/scopes/denied`, with 1s timeout and
  silent fall-through on any error so an unreachable proxy degrades to "no
  badges" rather than failure.
- Picker → scopes transition: `accountSelectedMsg` triggers row load and
  screen switch. `+ new account` in M2 surfaces a placeholder note and
  exits (full new-account flow lands in M3).
- 21 tests pass (15 new), full suite green. Binary still ~14M.
- **Manual verification deferred**: bubbletea TUIs need a TTY which the
  test sandbox doesn't have. User should run `charon tui <email>` to eyeball
  rendering, search, focus toggle, and color states.

### 2026-04-26 — M1 complete

- Added `internal/tui/` package with bubbletea + lipgloss deps (binary grew
  ~9M → ~14M, within plan estimate)
- `pickerModel` lists google accounts (sorted) + `+ new account` always last
- Top-level `model` routes to picker; selection emits `accountSelectedMsg` /
  `newAccountMsg` and quits the program (M2 wires these into the scope view)
- Hidden `charon tui [account]` command launches the TUI; existing `auth
  google` flow untouched until M5 cleanup
- Wrote pure unit tests against the model's `Update`/`View` directly (no
  `teatest` yet — overkill for the picker; will revisit at M3 if needed)
- All tests green (`go test ./...`)
