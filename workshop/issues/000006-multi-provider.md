---
id: 000006
status: open
deps: [000005]
github_issue:
created: 2026-04-26
updated: 2026-04-26
---

# Multi-provider support (Dropbox, etc.)

## Problem

Charon currently speaks Google OAuth only. Adding a second provider
(Dropbox is the obvious next candidate) requires changes across multiple
layers that share design assumptions, so it's a coordinated effort, not a
local change.

## Spec

### Where Google-specific assumptions live today

- `internal/oauth/google.go` — Google OAuth flow (auth URL, token endpoint,
  ID token email extraction, refresh, revoke). Hard-coded URLs, hardcoded
  required scopes (openid + userinfo.email), Google-specific quirks
  (`include_granted_scopes`, OIDC scope rewriting).
- `internal/oauth/scope_catalog.go` — `GoogleScopeCatalog`, named
  specifically for Google. The `Required` field is Google-OAuth-shaped.
- `internal/proxy/routing.go` — host → provider mapping. Already has
  `Provider` abstraction; well-positioned.
- `internal/proxy/scope_tracker.go` — denial tracking is already
  provider-keyed, fine.
- `internal/tui/picker.go` — currently filters to `provider == "google"`
  and renders a flat list. Needs grouping per provider.
- `internal/tui/scopes.go` — `loadScopeRows` queries `cred = v.Get("google",
  account)`. Hard-coded. `oauth.GoogleScopeCatalog` is the only scope
  source.
- `cmd/charon/main.go` — `tuiCmd` constructs an `oauth.GoogleProvider` as
  the only authenticator.

### What changes

1. **Provider abstraction** in `internal/oauth/`:
   - Interface `OAuthProvider` with the methods charon's TUI needs:
     `Auth(account, scopes, existing, forceFresh)`, `Refresh(cred)`,
     `Revoke(refreshToken)`, plus metadata: `Name() string`,
     `ScopeCatalog() []ScopeInfo`, `RequiredScopes() []string`.
   - `GoogleProvider` implements it. New `DropboxProvider` (etc.)
     implements it.
2. **Scope catalogs become per-provider**: `oauth.GoogleScopeCatalog`
   stays as Google-specific data; add `DropboxScopeCatalog` etc. The
   abstraction returns the right one via `provider.ScopeCatalog()`.
3. **TUI picker grouping**:
   ```
   Google accounts
   ────────────────
   > lovchatvol@gmail.com  (4 scopes)
     xianxu@gmail.com      (3 scopes)
     + new account

   Dropbox accounts
   ────────────────
     someone@dropbox.com   (2 scopes)
     + new account
   ```
   - `pickerItem` gets a `provider string` field
   - Render groups by provider, emits a header row per group
   - Cursor navigation skips headers (only stops on accounts and `+ new account`)
4. **`scopesModel` accepts provider**: `loadScopeRows(v, provider, account, ...)`
   instead of hard-coded `"google"`. The selected provider determines which
   catalog to use, which OAuth provider to dispatch with, and which keychain
   namespace to look in.
5. **TUI entry point picks the right provider per account**: when user
   selects an account from the picker, we know its provider from the picker
   item; pass that to `newScopesModel`.
6. **CLI**: `charon auth` continues to work without args (picker covers all
   providers). `charon auth dropbox <email>` could be a per-provider shortcut
   if useful, but probably isn't needed since the picker handles it.

### Routing

The existing `internal/proxy/routing.go` already maps hosts to providers
via the `Provider` struct. Adding Dropbox means:
- Add `dropbox.com` (and any sub-hosts) to the routing table
- Wire `DropboxProvider` into `Server.Refreshers["dropbox"]` for token
  refresh on cached-expiry

### Storage

Keychain key format is `provider:account`. Already namespace-friendly.
`vault.Store.List()` returns all credentials across providers. The TUI
picker can group them.

## Plan

- [ ] **M1: Lift Provider interface**
  - Define `oauth.OAuthProvider` interface with the methods used by TUI
    and proxy
  - `GoogleProvider` implements it; verify all callers compile
- [ ] **M2: Generalize scope catalog**
  - Move `GoogleScopeCatalog` access behind `provider.ScopeCatalog()`
  - `loadScopeRows` accepts a provider and uses its catalog
- [ ] **M3: Picker grouping**
  - `pickerItem.provider` field
  - Render groups by provider with section headers
  - Cursor navigation skips headers
  - Tests
- [ ] **M4: First non-Google provider** (Dropbox or whatever's most useful)
  - `DropboxProvider` implements the interface
  - Scope catalog for Dropbox APIs
  - Routing entries
  - Auth + refresh + revoke endpoint URLs
- [ ] **M5: End-to-end test through proxy**
  - Real (or stubbed) request to Dropbox API via charon proxy
  - Scope enforcement works (X-Charon-Scope header)
  - `charon auth` picker shows Dropbox section, lets user grant scopes,
    successful agent call follows

## Log
