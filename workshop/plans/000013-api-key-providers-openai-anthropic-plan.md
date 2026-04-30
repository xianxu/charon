---
issue: 000013
created: 2026-04-30
updated: 2026-04-30
---

# Plan — API-key providers: OpenAI + Anthropic

Companion to `workshop/issues/000013-api-key-providers-openai-anthropic.md`.
This plan is the source of truth for the cross-cutting TUI sketch and
storage shape that #14 (Google AI) and #15 (catalog) will inherit.

## TUI design sketch (cross-cutting)

The current TUI (`internal/tui/`) is hardcoded to one provider type:
the picker filters `Provider == "google"`, the scope view assumes an
OAuth-shaped credential, and revoke lives inside the scope view. To
land #13/#14/#15 cleanly, the TUI grows from two screens to three and
dispatches per-provider-type for the entity list and detail screens.

### Three-screen flow

```
provider picker  ──▶  entity list (per provider)  ──▶  detail (per type)
```

The detail screen may be one or more screens depending on provider
type. OAuth keeps today's scope view. Admin-key is a single info
screen. Catalog is a single info screen with a "replace key" affordance.

### Provider naming convention

Each provider type uses its **upstream-native vocabulary** in screen
titles, action labels, and ID prefixes. Users already know these
terms from the provider's dashboard.

| Provider type | Entity term | Add action      | ID prefix |
|---------------|-------------|-----------------|-----------|
| Google (OAuth) | account    | + new account   | email     |
| OpenAI        | project     | + new project   | `proj_…`  |
| Anthropic     | workspace   | + new workspace | `ws_…`    |
| Catalog (Tier 3) | account  | + new account   | (none)    |

### Screen 1 — Provider picker

Entry point for `charon auth`. Replaces today's "+ new account" flow
that runs straight to the Google scope view.

```
┌─ Charon ─────────────────────────────────────────────────┐
│ Provider                                                 │
│                                                          │
│ > Google         OAuth         3 accounts                │
│   OpenAI         Admin key     ● 2 projects              │
│   Anthropic      Admin key     ○ admin key not set       │
│   Groq           API key       1 account                 │
│   + add provider                                         │
│                                                          │
│ ↑↓ nav   enter select   q quit                           │
└──────────────────────────────────────────────────────────┘
```

- `●` (green) — admin-key configured and entities present
- `○` (red) — admin-key required but not set (admin-key types only)
- `+ add provider` — opens the catalog picker (#15 territory; stub
  in #13 to a "not yet implemented" state)

### Screen 2 — Entity list (provider-type dispatched)

#### 2a. Google › Accounts (OAuth)

```
┌─ Charon › Google ────────────────────────────────────────┐
│ Accounts                                                 │
│                                                          │
│ > xianxu@gmail.com       gmail.readonly, calendar (+2)   │
│   work@example.com       gmail.readonly                  │
│   + new account                                          │
│                                                          │
│ ↑↓ nav   enter open   r revoke   esc back   q quit       │
└──────────────────────────────────────────────────────────┘
```

`r` revokes upstream (OAuth revoke) + deletes the vault entry —
same as today's inner Ctrl+R, just lifted to the list level.

#### 2b. OpenAI › Projects (admin-key)

The admin-key row sits at the top of the same list, visually offset
by a blank line. **Single screen** — no separate admin-key
configuration screen. This is the key UX call for admin-key providers:
admin-key state is a list row, not a modal.

```
┌─ Charon › OpenAI ────────────────────────────────────────┐
│ Projects                                                 │
│                                                          │
│ > ● Admin key   xianxu@gmail.com / acme-inc              │
│                                                          │
│   work-project        proj_aB3…   key sk-…xyz            │
│   personal            proj_X9z…   key sk-…abc            │
│   + new project                                          │
│                                                          │
│ ↑↓ nav   enter open   r revoke   esc back   q quit       │
└──────────────────────────────────────────────────────────┘
```

`r` on admin-key row — confirms with a warning that names the
minted keys that will be removed.

When admin-key is unset:

```
┌─ Charon › OpenAI ────────────────────────────────────────┐
│ Projects                                                 │
│                                                          │
│ > ○ Admin key   not set — press enter to configure       │
│                                                          │
│   + new project   (admin key required — see above)       │
│                                                          │
│ ↑↓ nav   enter set up admin key   esc back               │
└──────────────────────────────────────────────────────────┘
```

`+ new project` is muted; pressing it flashes "set the admin key
first" in the status line and pulses the red row.

#### 2c. Anthropic › Workspaces (admin-key)

Same shape as 2b. Title says **Workspaces**, action is `+ new
workspace`, ID column shows `ws_…`. All other behavior identical.

#### 2d. Groq › Accounts (catalog Tier 3)

```
┌─ Charon › Groq ──────────────────────────────────────────┐
│ Accounts                                                 │
│                                                          │
│ > default              key gsk_…abc   added 2026-04-30   │
│   + new account                                          │
│                                                          │
│ ↑↓ nav   enter open   r revoke   esc back   q quit       │
└──────────────────────────────────────────────────────────┘
```

No admin-key row. `+ new account` opens the catalog add-flow —
covered in detail in #15's plan.

### Screen 3 — Detail (provider-type dispatched)

#### 3a. Google › <account> — scope view (existing)

Today's scope view stays as-is. **Behavioral changes:**

- `esc` returns to Screen 2a (account list), not exit
- `q` exits the program
- Inner `r` (revoke) keeps current behavior — revokes upstream + deletes
  the vault entry, same as outer `r`. (No "wipe and keep" semantics.)

#### 3b. OpenAI › <project> — admin-key detail

```
┌─ Charon › OpenAI › work-project ─────────────────────────┐
│ Project info                                             │
│                                                          │
│ Local name:  work-project                                │
│ Project ID:  proj_aB3xY9z                                │
│ Key ID:      key_QqW…                                    │
│ Key prefix:  sk-…xyz                                     │
│ Created:     2026-04-30 14:21                            │
│                                                          │
│ Key material is in keychain and never shown after mint.  │
│ To rotate, revoke and re-mint.                           │
│                                                          │
│ r revoke   esc back   q quit                             │
└──────────────────────────────────────────────────────────┘
```

Anthropic detail mirrors this with `ws_…` and "Workspace info".

#### 3c. Catalog › <account> — minimal detail with replace

```
┌─ Charon › Groq › default ────────────────────────────────┐
│ Account info                                             │
│                                                          │
│ Local name:  default                                     │
│ Key prefix:  gsk_…abc                                    │
│ Added:       2026-04-30                                  │
│ Endpoint:    https://api.groq.com                        │
│                                                          │
│ p replace key   r revoke   esc back   q quit             │
└──────────────────────────────────────────────────────────┘
```

`p` opens a paste prompt — catalog has no mint API, so rotation
means pasting a new key.

### Add-account flows

- **OAuth (Google):** existing — `+ new account` fires browser; the
  discovered email becomes the vault key.
- **Admin-key first-time setup:** triggered by `enter` on the `○ Admin
  key` row.
  1. Show admin-key URL with `[Open]` button (subprocess `open <url>`)
  2. Prompt: "Email/label for this admin key (informational): _____"
     → stored as `OrgLabel`
  3. Prompt: "Paste admin key: _____" (opaque field)
  4. Discover `OrgID` and `OrgName` via `GET /v1/organization` (or
     equivalent for Anthropic) — see "Discovery" below.
  5. Store admin key + meta. Return to entity list (now `●`).
- **Admin-key replace:** triggered by re-running setup on a
  configured `●` row, or by `enter` after `r` confirms an admin-key
  removal. Discovery → compare `OrgID`:
  - **Same `OrgID`** — silent rotate. Existing minted credentials
    untouched.
  - **Different `OrgID`** — confirm modal naming the credentials that
    will be removed (see "Same-org vs different-org replace" below).
- **Admin-key `+ new project`/`+ new workspace`:**
  1. Prompt: "Local name (X-Charon-Account value): _____"
  2. Prompt: "Project: [select existing] [create new]"
  3. If existing — list from API. If new — prompt for upstream name.
  4. Mint via Admin API. Capture key material at mint time (it's
     never shown again).
  5. Store as `Credential{Type:"admin-key", Provider, Account,
     AdminKey:{OrgID, ProjectID, KeyID, KeyMaterial, …}}`.
- **Catalog `+ new account`:** detailed in #15's plan. Roughly:
  catalog pick → `[Open]` provider URL → paste key → store.

### Revoke semantics (single-tier, same at both levels)

Revoke is identical at the entity-list level (Screen 2) and the
detail-screen level (Screen 3): **revoke upstream + delete the vault
entry**. No "wipe and keep" mode. Confirms in a modal first.

| Row revoked       | Upstream call                          | Vault effect                      |
|-------------------|----------------------------------------|-----------------------------------|
| OAuth account     | `oauth.Revoke(refresh_token)`          | delete entry                      |
| Admin-key project | `DELETE /…/projects/{p}/api_keys/{k}`  | delete entry                      |
| Admin-key admin   | (no upstream — local only)             | delete admin entry + cascade-delete all minted keys tagged with that `OrgID` |
| Catalog account   | (no upstream)                          | delete entry                      |

The admin-key cascade matters: when the admin-key row is revoked,
all minted credentials under that `OrgID` lose their ability to be
upstream-revoked through charon. The confirmation modal makes this
explicit and lets the user pre-revoke them individually first if
they want clean upstream state.

## Vault `Credential` shape (tagged-tuple, with OAuth-flat concession)

Discriminated by `Type`, with type-specific payload structs for the
*new* provider types. Wrong fields can't accidentally co-exist for
admin-key vs catalog. Adding a new provider type means adding one new
payload struct.

**Pragmatic concession — OAuth payload kept flat in M1.** The
originally-discussed fully-nested shape (`OAuth *OAuthData`) would
have required migrating 39 call sites across `internal/oauth/`,
`internal/proxy/`, `internal/tui/`, and `internal/vault/keychain/`.
That balloons M1 from "interface skeleton" into a cross-cutting
refactor. M1 keeps the OAuth fields top-level so existing call sites
compile unchanged; admin-key and catalog get proper nested payloads.
Future cleanup (post-#13) can lift OAuth into a nested struct in its
own issue when motivated.

```go
type Credential struct {
    Type     string // TypeOAuth | TypeAdminKey | TypeCatalog; "" = TypeOAuth (legacy)
    Provider string // "google" | "openai" | "anthropic" | "groq" | …
    Account  string // X-Charon-Account value

    // OAuth payload (flat for backward compat).
    AccessToken  string
    RefreshToken string
    Expiry       time.Time
    Scopes       []string

    // Type-specific payloads.
    AdminKey *AdminKeyData `json:"admin_key,omitempty"`
    Catalog  *CatalogData  `json:"catalog,omitempty"`
}

type AdminKeyData struct {
    OrgID, OrgLabel, OrgName       string
    ProjectID, ProjectName, KeyID  string
    KeyMaterial                    string
    CreatedAt                      time.Time
}

type CatalogData struct {
    KeyMaterial string
    AddedAt     time.Time
}
```

`Credential.CredType()` normalizes empty `Type` to `TypeOAuth` so
callers can switch on a single canonical value without juggling the
legacy empty-string case.

## Keychain layout

Extends the existing `_<service>:<key>` underscore convention for
non-account internal entries. Service is `charon` for prod,
`charon-dev` for dev (existing convention).

```
# OAuth (existing)
google:xianxu@gmail.com           → Credential w/ OAuth payload

# Admin-key (new)
_openai:admin:org-aB3cD4…         → admin key material
_openai:meta:org-aB3cD4…          → {"label":"xianxu@gmail.com","name":"acme-inc"}
openai:work-project               → Credential w/ AdminKey.OrgID="org-aB3cD4…"
openai:personal                   → Credential w/ AdminKey.OrgID="org-aB3cD4…"

_anthropic:admin:<uuid>           → admin key material
_anthropic:meta:<uuid>            → {"label":"...", "name":"..."}
anthropic:work-workspace          → Credential w/ AdminKey.OrgID="<uuid>"

# Catalog (#15)
groq:default                      → Credential w/ Catalog payload
```

Admin-key entries are keyed by `OrgID`, not `OrgLabel`. `OrgID` is
opaque but stable; `OrgLabel` is human-readable but mutable.

### Multi-org: schema yes, UI no (MVP)

The schema permits N admin-key entries per provider (one per
distinct `OrgID`). The MVP UI enforces "exactly one" by:
- Provider screen shows a single admin-key row per provider
- `+ new project` flow uses the singular admin-key entry without
  prompting "which org"

When multi-org UI ships (future issue), the only changes are:
- Provider entity list shows N admin-key rows (one per `OrgID`)
- Display becomes `<OrgLabel> / <OrgName>:<project_name>` for
  rows in mixed-org provider lists
- `+ new project` prompts which org

No schema migration. No keychain layout change.

## Discovery (admin-key → OrgID)

At admin-key paste time, charon needs `OrgID` and (best-effort)
`OrgName`. One API call per setup, results stored in `_<provider>:meta:<OrgID>`.

- **OpenAI:** `GET /v1/organization` returns `{id, name, ...}`.
  Auth: `Authorization: Bearer <admin_key>`.
- **Anthropic:** workspaces-list response carries the org id, or a
  dedicated org endpoint if available. Confirmed in M3.

If discovery fails (network down, 401 on bad key) the admin-key is
not stored — surface error in the TUI and let user retry.

## Same-org vs different-org replace

When the user re-pastes an admin key on an already-configured
provider:

1. Discover new key's `OrgID`.
2. Look up stored `OrgID`.
3. **Same** — replace `_<provider>:admin:<OrgID>` material in place.
   Existing minted credentials keep working (their key material is
   independent of admin-key material).
4. **Different** — show confirm modal:

   ```
   ┌─ Replace admin key ──────────────────────────────────────┐
   │ The new admin key is for a different organization.       │
   │                                                          │
   │   Current:  xianxu@gmail.com / acme-inc                  │
   │   New:      work@example.com / corp-llc                  │
   │                                                          │
   │ Charon will remove these minted keys (their underlying   │
   │ API keys keep working until you revoke them at the       │
   │ provider's dashboard):                                   │
   │                                                          │
   │   work-project        proj_aB3…   sk-…xyz                │
   │   personal            proj_X9z…   sk-…abc                │
   │                                                          │
   │ [y/enter] proceed    [n/esc] cancel                      │
   └──────────────────────────────────────────────────────────┘
   ```

   On `y`: cascade-delete all `<provider>:*` credentials with
   `AdminKey.OrgID` = old OrgID, delete `_<provider>:admin:<old>` and
   `_<provider>:meta:<old>`, then store the new admin key + meta.

This semantics is documented as a **caveat in the threat-model
amendment** (M2): replacing the admin key with one from a different
org leaves orphaned API keys live at the provider — they must be
cleaned up at the provider's dashboard.

## Provider interface

The `Provider` interface — implemented per upstream — abstracts the
mint/revoke/list operations the TUI invokes. Lives in
`internal/providers/`. OAuth providers (existing `internal/oauth/`)
do not implement this; they continue to live separately and the TUI
dispatches by `Type` in the credential.

```go
package providers

type Provider interface {
    Name() string                                                 // "openai" | "anthropic" | …
    Type() string                                                 // "admin-key" | "catalog"

    // Admin-key ops (admin-key Type only)
    DiscoverOrg(ctx, adminKey string) (orgID, orgName string, err error)
    ListProjects(ctx) ([]Project, error)
    CreateProject(ctx, name string) (Project, error)
    MintKey(ctx, projectID, keyName string) (KeyID, KeyMaterial string, err error)
    RevokeKey(ctx, projectID, keyID string) error
}

type Project struct {
    ID, Name string
}
```

Catalog providers don't implement `Provider` — they're driven by
catalog YAML in #15 and don't have admin-key operations.

## Milestones

### M1 — `internal/providers/` package skeleton (2–4h) — **DONE 2026-04-30**

Landed:

- `internal/providers/provider.go` — `Provider` interface, `Project`
  type, sentinel errors `ErrAlreadyRevoked` and `ErrInvalidAdminKey`
- `internal/providers/fake.go` — in-memory `Fake` Provider with
  configurable identity (`OrgID`/`OrgName`), gateable admin-key
  validation (`ValidAdminKey`), seedable projects, and a `Snapshot`
  helper for assertions. Concurrency-safe.
- `internal/providers/fake_test.go` — covers DiscoverOrg accept/reject,
  full mint/revoke happy path, idempotent revoke (returns
  `ErrAlreadyRevoked` on the second call), unknown-project rejection,
  seeded list, and `WithName` identity override
- `internal/vault/vault.go` — `Type` discriminator (`TypeOAuth`,
  `TypeAdminKey`, `TypeCatalog`), `AdminKeyData` and `CatalogData`
  payload structs, `CredType()` accessor that normalizes empty Type
  to `TypeOAuth`
- `internal/vault/vault_test.go` — covers legacy JSON deserialize
  (no Type field, flat OAuth fields), admin-key round-trip
  (including wrong-payload guard: admin-key creds reject OAuth
  fields), catalog round-trip, OAuth round-trip with explicit Type
- `internal/vault/keychain/common.go` — `storedCredential` extended
  with `Type`/`AdminKey`/`Catalog`; pre-#13 keychain entries
  deserialize unchanged

`go test ./... && go vet ./...` all green. No upstream provider
implementations yet (M2/M3).

### M2 — OpenAI provider impl + threat-model amendment (10–16h) — **DONE 2026-04-30**

Landed:

- `internal/providers/openai/provider.go` — `Provider` impl over the
  5 Admin API endpoints. Auth via `Authorization: Bearer <admin_key>`.
  Status-code mapping: 401 → `ErrInvalidAdminKey`, 404 on DELETE →
  `ErrAlreadyRevoked`, 2xx → decode, anything else → upstream-error-
  message preserved. Network errors wrapped without sentinel mapping.
- `internal/providers/openai/provider_test.go` — httptest-backed
  fake server covering all 5 endpoints with auth checks; tests cover
  happy paths, both auth-failure paths, double-revoke + unknown-key
  → already-revoked, upstream-error message preservation, network
  failure, archived-project filter, and `name`/`title` field
  fallback.
- `internal/providers/keychain.go` — shared `AdminKeyStore` for
  admin-key + meta storage. MVP layout is single-org per provider:
  `_<provider>:admin` (raw admin key) + `_<provider>:meta` (JSON
  with `org_id`/`org_label`/`org_name`). The OrgID lives inside the
  meta blob rather than in the keychain key itself. Multi-org-UI
  future migration: rename to `_<provider>:admin:<OrgID>` /
  `_<provider>:meta:<OrgID>` and add an index entry; trivial
  migration since the meta blob already carries OrgID. AdminKeyData
  on minted credentials already carries OrgID for cascade-revoke
  logic, so no schema gap on the credential side.
- `internal/providers/keychain_test.go` — covers round-trip,
  per-provider namespacing, idempotent delete, validation
  (empty admin key / OrgID rejected), and corruption case where
  admin entry is present but meta is missing (explicit error,
  not silent ErrAdminKeyNotSet).
- `internal/vault/keychain.DeleteRaw` — added to both kv.go (CLI)
  and kv_darwin.go (cgo) with matching idempotent semantics.
- `docs/threat-model.md`:
  - Assets table — admin keys (Highest, blast radius "anything the
    dashboard can do") and minted per-account keys (High, project-
    scoped, independent of admin key)
  - "Posture in one page" intro — admin keys + minted keys called
    out as protected
  - New section "Admin keys: cross-org orphaning on replace" —
    documents the user-facing caveat with the confirm-modal flow
    and the bound (charon's M4 ACL gates the abuse path)
  - New adversary entry **A11 — Provider admin key abuse** with
    M4 ACL defense and cross-references to A10 (signing key
    abuse), B1 (FDA), C (local root). Prior A11 ("Charon's own
    bugs") renumbered to A12.

Decisions documented in the issue's Log:
- Discovery uses `GET /v1/organization` returning `{id, name, title}`;
  `name` preferred, `title` fallback for response-shape robustness.
- ListProjects single-page only for MVP (personal-gateway scope).

### M3 — Anthropic provider (mirror of M2) (4–8h)

- `internal/providers/anthropic/` — `x-api-key` auth header
- Workspaces in place of projects (`ws_…` IDs)
- Same shape as M2; pattern reuse drives the smaller estimate

### M4 — TUI provider/admin-key/account flows (8–14h)

- Provider picker (Screen 1)
- Per-type entity list dispatch (Screens 2a / 2b / 2c)
- Admin-key detail (Screen 3b) and OAuth scope view's `esc`
  back-to-list change (Screen 3a)
- Add-account flows per type
- Same-org / different-org replace confirm modal
- Render-dump tests against snapshot views per provider type

### M5 — Proxy per-host routing (4–8h)

- Per-host routing for `api.openai.com` and `api.anthropic.com`
- Auth header injection: `Authorization: Bearer <key>` for OpenAI;
  `x-api-key: <key>` for Anthropic
- `X-Charon-Scope` silently ignored on routes to admin-key providers
- Integration test: agent → proxy → mock upstream

### M6 — Account-level rm refactor (2–4h)

- Lift revoke from inside scope view to entity list level for OAuth
- Same affordance for admin-key entity list (already shipping in M4)
- Single `r` codepath through `internal/tui` regardless of provider type
- Cascade-delete logic for admin-key revoke at the row level

### M7 — Docs (1–3h)

- `docs/agent-protocol.md` — provider-type subsection clarifying
  which `X-Charon-*` headers apply per type; `X-Charon-Scope` is
  silently ignored on admin-key/catalog routes
- `README.md` — "What it does" mentions LLM providers
- `docs/threat-model.md` — reconcile admin-key asset class (already
  amended in M2); cross-reference orphan-key caveat

### Code review (2 chunks)

- **Review chunk 1** — after M1 + M2 + M3 (interface + both providers
  but pre-TUI). Validates provider abstractions before they're
  consumed.
- **Review chunk 2** — after M4 + M5 + M6 + M7 (TUI + routing + docs).
  Validates the user-facing surface and routing correctness.

Both via `superpowers:requesting-code-review` →
`superpowers:code-reviewer` subagent. Address Critical and Important
findings before next chunk.

## Open questions resolved by this plan

| Issue's open question                          | Resolution                                                              |
|------------------------------------------------|-------------------------------------------------------------------------|
| Admin key rotation flow                        | Same-org replace = silent in-place rotate. Different-org = wipe-warn.  |
| Multi-org under one provider                   | Schema supports N. MVP UI enforces 1. Future issue lifts UI invariant. |
| Admin key revoked upstream                     | Charon detects on next admin-API call, surfaces in TUI. Minted keys keep working until separately revoked. (M2 surfaces this; M4 displays it.) |

## Atlas updates (concurrent with milestones)

- `atlas/charon.md` — add provider-types section (OAuth / admin-key /
  catalog) with the three-screen TUI shape
- `atlas/index.md` — link any new atlas pages
- `atlas/security-audit.md` — admin-key asset class reference
