---
id: 000004
status: open
deps: []
github_issue:
created: 2026-04-25
updated: 2026-04-25
---

# Scope Management & Auth Flow Improvements

## Problem

1. **Account mismatch on auth:** `charon auth google test@gmail.com` doesn't validate that the browser-authenticated account matches the CLI argument. Tokens for `other@gmail.com` get stored under `test@gmail.com`.

2. **No scope visibility:** Users can't inspect what scopes are granted for a credential. `charon accounts` and `charon status` don't show scopes.

3. **No scope enforcement at proxy:** When a caller needs a scope that isn't granted, the upstream API returns a cryptic 403. Charon could detect the mismatch early and return actionable guidance.

4. **No self-healing flow:** When scopes are missing, there's no way to discover what's needed and fix it without manually knowing the exact scope URL.

## Spec

### Auth Flow Changes

- Make email argument optional: `charon auth google [email]`
- No email: user picks account in browser, charon extracts email from ID token (requires `openid email` scopes)
- With email: pass as `login_hint` (soft suggestion, not enforcement), still validate post-auth
- Always extract actual email from ID token and use as storage key
- If provided email doesn't match authenticated account, warn and use actual email

### Proxy Header Protocol

Callers declare required scopes via header:

```
X-Charon-Account: xianxu@gmail.com
X-Charon-Scope: calendar.readonly,drive.readonly
```

On scope mismatch, charon returns HTTP 407 with structured body:

```json
{
  "error": "scope_missing",
  "missing": ["calendar.readonly"],
  "account": "xianxu@gmail.com",
  "provider": "google",
  "fix": "charon auth google grant xianxu@gmail.com calendar.readonly"
}
```

### Scope Request Tracking

- Proxy tracks scope mismatches in a bounded ring buffer (last 100 entries, expire after 24h)
- Each entry: provider, account, scope, timestamp, count
- Ephemeral operational state, not persisted across restarts (or lightweight file)

### Scope State Model (per credential)

| State | Storage | Meaning |
|-------|---------|---------|
| Granted | Keychain (with credential) | User authorized this scope |
| Requested | Ring buffer (ephemeral) | Caller asked, not yet granted |
| Suppressed | Config file (persistent) | User explicitly denied, don't prompt |

### CLI Commands

```bash
# Auth flow
charon auth google                              # fresh auth, auto-detect email
charon auth google xianxu@gmail.com             # auth with login_hint

# Scope catalog & inspection
charon auth google scopes                       # catalog of known Google scopes
charon auth google scopes xianxu@gmail.com      # granted/pending/suppressed for account

# Grant specific scopes (triggers incremental OAuth)
charon auth google grant xianxu@gmail.com calendar.readonly

# Self-healing fix
charon auth fix                                 # all providers, all accounts
charon auth google fix xianxu@gmail.com         # one provider/account

# Unsuppress
charon auth google unsuppress xianxu@gmail.com drive.readonly
```

### Fix Command UX

- One browser interaction per provider×account pair, sequentially
- Within each interaction, all pending scopes batched into one consent screen
- Cross-provider grants are serialized with clear step indicators:

```
[1/2] google / xianxu@gmail.com (2 scopes: calendar.readonly, drive.readonly)
      Opening browser... [wait for completion]

[2/2] dropbox / xianxu@gmail.com (1 scope: files.content.read)
      Opening browser... [wait for completion]
```

- User can grant or suppress (deny) each provider×account batch
- Suppressed scopes won't appear in future `fix` prompts

### Scope Catalog

Charon maintains a static catalog of known scopes per provider (human-readable descriptions). Informational only, not enforcement:

```
gmail.readonly         Read Gmail messages
calendar.readonly      Read Google Calendar events
drive.readonly         Read Google Drive files
```

### Nous Integration (out of scope for charon, noted for context)

- Skills declare required scopes in manifests: `google: [calendar.readonly]`
- `nous doctor [email]` aggregates skill scopes, calls `charon auth <provider> scopes <email>`, reports gaps
- Proactive (before running) vs `charon auth fix` which is reactive (after failure)

## Plan

- [x] **M1: Auth flow — email from ID token**
  - Add `openid email` to default scopes
  - Extract email from ID token after token exchange
  - Make email arg optional (use as `login_hint` when provided)
  - Validate/warn on mismatch, store under actual email
  - Tests

- [x] **M2: Scope inspection CLI**
  - `charon auth google scopes` — static catalog
  - `charon auth google scopes user@gmail.com` — show granted scopes
  - `charon auth google grant user@gmail.com scope1 scope2` — grant specific scopes
  - Enhance `charon accounts` to show scopes
  - Tests

- [x] **M3: Proxy scope enforcement**
  - Parse `X-Charon-Scope` header
  - Check against granted scopes
  - Return 407 with structured error on mismatch
  - Tests

- [x] **M4: Scope request tracking**
  - Ring buffer for denied scope requests (bounded, ephemeral)
  - Track: provider, account, scope, timestamp, count
  - Expose via `/scopes/denied` API for `fix` command

- [x] **M5: Fix command**
  - `charon auth fix` — interactive, sequential per provider×account
  - `charon auth google fix user@gmail.com` — targeted
  - Suppress list deferred to follow-up (needs interactive UX design)

## Log

### 2026-04-25
- M1-M5 implemented in a single pass
- All tests passing (50+ tests across all packages)
- Suppress list (deny/unsuppress) deferred — the interactive selection UX needs more thought
  and a real terminal interaction library. The core fix command works for the common grant flow.
