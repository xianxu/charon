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

- [ ] **M3: Toggle + apply (additive only)**
  - Space toggles target state
  - Pending-change indicator (e.g. `*`)
  - Enter:
    - target == realized → exit no-op
    - target ⊋ realized → run incremental OAuth via existing `Auth()` path
  - Custom scope URL via `a`
  - Tests: target/realized diff logic, no-op exit, incremental dance

- [ ] **M4: Reduction with warning gate**
  - Detect `target ⊊ realized` or mixed (any reduction)
  - Modal warning with continue/cancel
  - On continue: revoke endpoint call → re-auth with target → atomic
    credential replace on success
  - On re-auth failure: leave old credential intact, surface error (i.e. only
    revoke as part of a single committed flow; never revoke before the new
    auth completes)
  - `R` (revoke account entire): same primitive, target = ∅ + remove
    credential entirely
  - Tests: revoke + reauth happy path, reauth-cancel preserves old credential

- [ ] **M5: Cleanup of replaced commands + defaults**
  - Delete `scopes`, `grant`, `fix` cobra commands
  - `DefaultGoogleScopes = []` (kept var so external callers compile, but
    empty)
  - Update `atlas/charon.md`
  - Update help text on `charon auth google`
  - Tests for command removal (help no longer lists them)

## Log

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
