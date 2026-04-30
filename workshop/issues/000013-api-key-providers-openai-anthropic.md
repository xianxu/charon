---
id: 000013
status: working
deps: []
github_issue:
created: 2026-04-28
updated: 2026-04-30
estimate_hours: 55
---

# API-key providers: OpenAI + Anthropic

## Problem

Charon currently supports only OAuth-based providers (Google for
Gmail/Drive/etc.). The agent uses `X-Charon-Account` to pick which
account's bearer token gets injected into the upstream request.

LLM API providers (OpenAI, Anthropic) don't have OAuth flows for
API access — they use static API keys. To bring them under charon's
"agent never sees the credential" model, charon needs a different
credential lifecycle:

1. User does a one-time setup: pastes a provider **admin key** into
   charon. This is a higher-stakes credential than per-account keys
   (it can mint/revoke keys org-wide), but it lives in macOS
   Keychain under M4 ACL same as everything else.
2. For each account the user adds, charon calls the provider's
   Admin API to **mint a fresh project key**. The secret is captured
   on creation and stored in Keychain. The user never sees it.
3. Agent uses `X-Charon-Account: my-project`; charon swaps in the
   real key transparently.
4. `charon accounts rm openai my-project` calls the Admin API to
   **revoke** the key, then deletes from Keychain. Real revocation,
   not just "delete and hope".

## Goal

Ship OpenAI and Anthropic as first-class providers with the
admin-key + programmatic-mint lifecycle. Same `X-Charon-Account`
agent UX as OAuth providers; different lifecycle inside charon.

## Spec

### Per-provider concrete shape

**OpenAI**:
- Admin key: <https://platform.openai.com/settings/organization/admin-keys>.
  Recommend the user scope it to "API Key Management" only when
  creating.
- Project: required parent of any API key. Charon can use existing
  projects or create new ones.
- Endpoints:

  ```
  GET    /v1/organization/projects                                # list projects
  POST   /v1/organization/projects                                # create project
  POST   /v1/organization/projects/{id}/api_keys                  # mint key (capture-once)
  DELETE /v1/organization/projects/{id}/api_keys/{key_id}         # revoke
  ```

  All use admin key as `Authorization: Bearer <admin_key>`.

**Anthropic**:
- Admin key: <https://console.anthropic.com/settings/admin-keys>.
- Workspace: Anthropic's equivalent of OpenAI projects.
- Endpoints:

  ```
  GET    /v1/organizations/{org_id}/workspaces                    # list
  POST   /v1/organizations/{org_id}/workspaces                    # create
  POST   /v1/organizations/{org_id}/workspaces/{ws_id}/api_keys   # mint
  DELETE /v1/organizations/{org_id}/workspaces/{ws_id}/api_keys/{id}  # revoke
  ```

  Auth: `x-api-key: <admin_key>` header (Anthropic's convention).

### TUI flow

Extending `charon auth`'s provider picker:

```
charon auth
  ▶ Provider:
      [google]   OAuth — Gmail, Drive, ...
      [openai]   API key — OpenAI Admin
      [anthropic] API key — Anthropic Admin

  → openai
    ▶ One-time admin key not yet configured.
       Email/label for this admin key (informational): _____
       Admin key: <pasted opaquely>
       [Stored in Keychain.]

    ▶ Account:
       [add new]
       (none configured yet)

  → openai → add new
    ▶ Account name (X-Charon-Account value): work-project
    ▶ Project: [select existing | create new]
       Existing: <list from API>
       New:      Project name? _____
    ▶ Minting API key... ✓ stored as `openai/work-project`.
```

### Storage shape

Three kinds of items in Keychain (all under `charon` service for
prod, `charon-dev` for dev):

- `_openai:admin` — the admin key (one per provider)
- `_openai:label` — informational email tied to the admin key
- `openai:work-project` — the per-account minted key (and metadata:
  project_id, key_id, created_at)

Same shape for Anthropic with prefix `_anthropic:` / `anthropic:`.

The leading underscore convention follows the existing `_ca:cert` /
`_ca:key` pattern for non-account internal entries.

### Agent-facing protocol

Unchanged. Agent sets:

```
X-Charon-Account: work-project
```

Charon's per-host routing table needs entries for OpenAI / Anthropic
upstreams (`api.openai.com`, `api.anthropic.com`) with the
appropriate auth-header injection (`Authorization: Bearer <key>`
for OpenAI, `x-api-key: <key>` for Anthropic).

Existing `X-Charon-Scope` doesn't apply (these providers don't
have OAuth scopes); should be silently ignored when present.

### Account-level rm at the TUI list

Independent of new-provider work but called out: the current TUI
requires entering an account to revoke it. Bubble revoke up to the
account-list level — same affordance applies to OAuth and API-key
accounts. Track as a sub-task here or split off; either way it's
related UX scope.

## Estimate

**Range: 41–78 hr (~4–8 working days). Best guess: ~55 hr (~5.5 days).**

Produced via `brain/data/life/42shots/velocity/estimate-logic-v1.md` against `baseline-v1.md`. Method A only.

All-Go, all-familiar territory in a mature charon codebase — no novel stacks. The biggest single chunk is M2 (OpenAI) at 10–16 hr; M3 (Anthropic) is a near-mirror.

| Milestone | Primitive match | Base hr | Familiarity × | Adjusted |
|---|---|---|---|---|
| TUI design sketch (cross-cutting, upfront) | Pensive | 1–2 | ×1.0 | 1–2 |
| M1 — `internal/providers/` interface skeleton | Smaller Go module | 2–4 | ×1.0 | 2–4 |
| M2 — OpenAI provider impl + threat-model amendment | Greenfield Go module (single concern) | 10–16 | ×1.0 | 10–16 |
| M3 — Anthropic provider (mirror of M2) | Smaller Go module (pattern reuse) | 4–8 | ×1.0 | 4–8 |
| M4 — TUI: provider picker + admin-key + account flows | Greenfield Go module | 8–14 | ×1.0 | 8–14 |
| M5 — Proxy per-host routing for OpenAI/Anthropic | Smaller Go module | 4–8 | ×1.0 | 4–8 |
| M6 — Account-level rm refactor (lift to list) | Smaller Go module | 2–4 | ×1.0 | 2–4 |
| M7 — Docs (agent-protocol, README, threat-model) | Atlas/docs ×3 | 1–3 | ×1.0 | 1–3 |
| Code review × 2 chunks | Process overhead | 2–6 | ×1.0 | 2–6 |
| **Subtotal** | | | | **34–65** |
| **+20% unknown-unknowns buffer** | | | | **41–78** |

Caveats:
- Assumes baseline calibration (10hr/day focused, solo founder + AI, current-baseline polish; see `baseline-v1.md`).
- M3's "pattern reuse" assumption depends on M2 being clean and well-factored. If OpenAI's quirks force pattern-breaking in M2, M3 grows toward 8 hr or higher.
- This is the first of three provider-related issues (#13, #14, #15). The TUI design sketch and threat-model amendments here set patterns that #14/#15 inherit — overhead absorbed in this estimate, savings show up in those.

## Plan

Detailed plan in
[`workshop/plans/000013-api-key-providers-openai-anthropic-plan.md`](../plans/000013-api-key-providers-openai-anthropic-plan.md).

Progress:

- [x] **TUI design sketch** (cross-cutting) — locked 2026-04-30. See
  plan doc § "TUI design sketch" for the three-screen flow, per-type
  entity-list dispatch, admin-key-as-list-row pattern, multi-org
  schema with single-org UI, and `Credential` tagged-tuple shape.
- [x] **M1 — provider interface skeleton** — landed 2026-04-30.
  `internal/providers/` package + `Fake` for tests; `vault.Credential`
  extended with `Type` discriminator and `AdminKey`/`Catalog`
  payloads. OAuth payload kept flat for backward compat (concession
  documented in plan doc).
- [x] **M2 — OpenAI provider impl + threat-model amendment** — landed
  2026-04-30. `internal/providers/openai/` with httptest-backed
  end-to-end coverage of the 5 Admin API endpoints and all sentinel
  error paths. Shared `internal/providers/AdminKeyStore` for
  admin-key + meta keychain storage. Threat-model gains admin-key
  asset class, cross-org orphan-key caveat, and adversary entry A11
  (provider admin key abuse).
- [x] **M3 — Anthropic provider** — landed 2026-04-30.
  `internal/providers/anthropic/` mirrors M2 with `x-api-key`
  auth, mandatory `anthropic-version: 2023-06-01` header, and
  workspace-shaped path (org id in URL). Internal `orgIDCache`
  memoizes the `/v1/organizations/me` lookup so non-discovery
  calls remain single-round-trip. Tests cover all the M2
  scenarios plus the version-header requirement and cache
  single-discovery / lazy-discovery semantics.
- [x] **Code review chunk 1 (M1+M2+M3)** — completed 2026-04-30.
  Critical + Important findings addressed (see Log).
- [ ] M4 — TUI provider/admin-key/account flows
  - [x] **Phase 1** (provider picker + entity-list rendering +
    dispatch infrastructure) — landed 2026-04-30
  - [ ] **Phase 2** (admin-key paste + mint + replace modal flows)
  - [ ] **Phase 3** (admin-key detail screen + catalog stub)
- [ ] M5 — proxy per-host routing
- [ ] M6 — account-level rm refactor
- [ ] M7 — docs
- [ ] code review chunk 1 (after M1+M2+M3)
- [ ] code review chunk 2 (after M4+M5+M6+M7)

Sketch milestones:

1. **M1** — `internal/providers/` package skeleton. New `Provider`
   interface separate from existing `internal/oauth/`. Interface
   methods: `SetupAdmin`, `ListProjects`, `CreateProject`,
   `MintKey`, `RevokeKey`. OAuth providers don't implement this;
   they continue to live in `internal/oauth/`.
2. **M2** — OpenAI provider implementation (Admin API client +
   keychain storage of admin key + per-account minted keys).
3. **M3** — Anthropic provider implementation (mirror of M2).
4. **M4** — TUI: provider picker → admin-key setup → account
   add/rm flows for both providers.
5. **M5** — Proxy injection: per-host routing for `api.openai.com`
   and `api.anthropic.com` with the right header shape per provider.
6. **M6** — Account-level rm in the TUI for ALL providers (OAuth
   + API key). Could ship independently if M4 is delayed.
7. **M7** — Docs: update `agent-protocol.md` for the API-key provider
   notes; update README "What it does" section to mention LLM
   providers; update threat-model assets table to include admin
   keys explicitly.

## Open questions

- **Admin key rotation**: should charon offer an interactive
  rotation flow, or punt to "delete the old admin key in the
  provider UI, paste a new one"? Punting is simpler; rotation is
  rarely-used.
- **Multi-org under one provider**: a user might have personal +
  work OpenAI orgs. Should charon support multiple admin keys per
  provider? Probably yes (`_openai:admin:<label>`); design the
  storage shape with that flexibility from M2.
- **What if an admin key gets revoked upstream**: charon's per-
  account keys still work (independent secrets) until they expire
  or are revoked separately. Charon can detect via failed admin
  API calls and surface a warning.

## Cross-cutting concerns (since #13 is first)

#13 ships before #14 and #15, which means it sets the patterns
those follow. Three things to get right here so they're not
re-litigated later:

### 1. Agent-protocol updates

[`docs/agent-protocol.md`](../../docs/agent-protocol.md) currently
describes the `X-Charon-Account` and `X-Charon-Scope` headers
assuming an OAuth-shaped provider model. With API-key providers:

- `X-Charon-Account` semantics extend cleanly — still names a
  charon-side credential, regardless of OAuth-vs-API-key backing.
- `X-Charon-Scope` doesn't apply (API-key providers have no scope
  concept). The proxy must **silently ignore** the header when set
  on a request routed to an API-key provider — don't 407 on
  irrelevance. Document the behavior explicitly so agent
  implementations don't break when retargeting between provider
  types.

Action: update the doc as part of #13, before #14/#15 land. Add a
"provider type" subsection clarifying which headers apply per
provider type.

### 2. Threat model: admin key is a new asset class

The provider admin keys (OpenAI, Anthropic) are arguably the
**highest-blast-radius credentials charon holds**:

- Owning an OpenAI admin key lets the attacker mint API keys with
  full org access, see usage data, modify rate limits, change
  billing — anything the dashboard can do.
- Same picture for Anthropic admin keys at the workspace level.

Compare to OAuth refresh tokens (limited to granted scopes) or per-
account API keys (limited to one project's quota). Admin keys are
the meta-credential.

M4 keychain ACL is sufficient as the storage boundary (same as
everything else), but the threat-model **assets table** should
gain a row before this issue lands:

```
| Provider admin keys | macOS Keychain (`charon` service, `_<provider>:admin*`) | **Highest** | Mint per-account keys with full org access; see all usage; modify rate limits |
```

And probably a separate adversary entry for "admin key abuse" as
a sibling of A10 (signing key abuse), since the impact shape is
the same: one credential = silent persistence.

Action: amend `docs/threat-model.md` as part of #13's M2, before
the OpenAI implementation lands.

### 3. TUI scaling sketch upfront

The current `charon auth` TUI is OAuth-shaped: it shows accounts
with scopes, lets you grant/revoke individual scopes. With three
provider types (OAuth / admin-key / catalog Tier 3), the existing
shape doesn't compose cleanly. A short design sketch before
implementation prevents painting into a corner.

Sketch (to validate during M4):

```
charon auth
  ┌─────────────────────────────────────────────┐
  │  Provider                                   │
  │    ▶ Google           OAuth (3 accounts)    │
  │    ▶ OpenAI           Admin (1 org)         │
  │    ▶ Anthropic        Admin (1 org)         │
  │    ▶ Add provider…                          │
  └─────────────────────────────────────────────┘

→ pick provider, then provider-type-dispatched account list:

  OAuth provider:
    Google
    ▶ xianxu@gmail.com  [scopes: gmail.readonly, calendar]
    ▶ work@example.com  [scopes: gmail.readonly]
    [add account] [revoke account]

  Admin-key provider:
    OpenAI (admin: xianxu@gmail.com / acme-inc)
    ▶ work-project   [project: acme-prod, key: …xyz]
    ▶ personal       [project: side-projects, key: …abc]
    [add account] [revoke account] [rotate admin key]

  Catalog (Tier 3, ships in #15):
    Groq
    ▶ default        [paste key]
    [add account] [delete account]
```

Key UX decisions to lock in for #13:
- **Account-level revoke at list level** (not nested inside
  account) — applies to OAuth and admin-key providers alike.
  Lift this from the current "go into account → revoke" flow.
- **Provider-type-dispatched account flows** — the operations
  available depend on provider type. The TUI can render this with
  a uniform "add / revoke / type-specific" affordance per row;
  type-specific actions (rotate admin key, etc.) appear
  conditionally.
- **Single global setup state** — admin keys and OAuth refresh
  tokens both live in the same keychain namespace; the TUI just
  presents them by provider type.

Action: produce the design sketch as the first deliverable of #13's
plan doc; validate before writing code.

---

## Notes

- Per-account API keys are functionally similar to OAuth refresh
  tokens — long-lived, revocable upstream. Same storage path.

## Log

- **2026-04-30** — TUI design sketch landed in
  [`plans/000013-…-plan.md`](../plans/000013-api-key-providers-openai-anthropic-plan.md).
  Key locks:
  - Three-screen flow: provider picker → entity list → detail
  - Per-provider local naming: Google/account, OpenAI/project,
    Anthropic/workspace, catalog/account
  - Admin-key state as a list row (red `○` / green `●`) on the
    project/workspace screen, not a separate setup screen
  - Single-tier revoke semantics: same delete-shell behavior at
    entity-list `r` and detail-screen `r`
  - `Credential` is a tagged tuple (Type discriminator + nested
    `OAuth`/`AdminKey`/`Catalog` payload structs)
  - Multi-org schema (N admin keys per provider keyed by `OrgID`)
    with single-org UI invariant for MVP
  - Same-org admin-key replace = silent rotate; different-org =
    confirm-then-cascade-wipe
  - Discovery: one API call at admin-key paste time to capture
    `OrgID` + `OrgName`
  - `OrgID`/`OrgLabel`/`OrgName` three-field shape: opaque
    upstream id, user mnemonic, discovered display name

- **2026-04-30** — M1 (provider interface skeleton) landed. Concession
  vs the plan doc's strict "fully-nested OAuth": kept OAuth fields
  top-level on `vault.Credential` to avoid migrating 39 unrelated call
  sites in OAuth/proxy/TUI/keychain. Admin-key and catalog payloads
  are properly nested. `Type` discriminator added with empty-string
  legacy handling via `CredType()`. Pre-#13 keychain entries
  deserialize unchanged. New `internal/providers/` package with the
  `Provider` interface and a concurrency-safe `Fake` for tests.

- **2026-04-30** — M2 (OpenAI provider + threat-model amendment)
  landed.
  - `internal/providers/openai/` — Provider impl over the 5 Admin
    API endpoints (GET org, list/create projects, mint/revoke keys).
    httptest-backed tests cover happy paths, invalid admin key →
    `ErrInvalidAdminKey`, double-revoke and unknown-key →
    `ErrAlreadyRevoked`, upstream-error message preservation,
    network-error wrapping, and `name` vs `title` org-name fallback.
  - `internal/providers/AdminKeyStore` — shared keychain helper for
    `_<provider>:admin` + `_<provider>:meta` (single-org MVP layout;
    documented future migration path to per-OrgID keying for
    multi-org UI). Tested with injectable IO callbacks — no real
    keychain access in unit tests.
  - `internal/vault/keychain.DeleteRaw` added (kv.go + kv_darwin.go)
    with idempotent semantics — missing entry is not an error.
  - `docs/threat-model.md` — admin-key + minted-key rows added to
    the assets table; "Posture in one page" intro mentions provider
    admin keys; new "Admin keys: cross-org orphaning on replace"
    section documents the user-facing caveat; new adversary entry
    **A11 — Provider admin key abuse** (renumbered prior A11 to
    A12). Same M4 ACL defense as OAuth refresh tokens; failure
    modes (A10, B1, C) cross-referenced.
  - Discovery shape decision: `GET /v1/organization` returns
    `{id, name, title}`; `name` preferred, `title` fallback.
    Defensive — accommodates the two response shapes seen in
    third-party docs without locking either in.
  - Pagination decision: ListProjects single-page only for MVP.
    Personal-gateway scope (<100 projects) makes this safe; future
    work plumbs `has_more` if needed.

- **2026-04-30** — M3 (Anthropic provider) landed.
  - `internal/providers/anthropic/provider.go` — Provider impl
    over the 5 endpoints under `/v1/organizations/{org_id}/`. Auth:
    `x-api-key` (not Bearer) + mandatory `anthropic-version:
    2023-06-01` on every request.
  - URL-path org id resolved via lazy-discovery cache: first
    non-DiscoverOrg call triggers `GET /v1/organizations/me`, result
    cached on a per-admin-key `sync.Map`. Subsequent calls are
    single-round-trip.
  - Wire-shape decisions vs OpenAI: Anthropic's mint response uses
    `key` (not `value`) for the secret material, and the error
    envelope is `{"type":"error","error":{...}}` rather than
    OpenAI's flat `{"error":{...}}`. Both encoded in package-local
    types so the providers.Provider abstraction stays clean.
  - Tests: 12 cases covering happy paths for all 5 endpoints,
    invalid-key + empty-key sentinel errors, idempotent revoke,
    archived-workspace filter, anthropic-version header
    enforcement (server rejects requests missing it; charon must
    always emit), single-discovery cache hit, lazy-discovery on
    first non-DiscoverOrg call, upstream-error message
    preservation, and network-error wrapping.

- **2026-04-30** — Chunk 1 code review (M1+M2+M3) completed via
  superpowers-code-reviewer subagent against `7b4b197..b38609b`.
  Outcome: production-ready scaffolding with one Critical and seven
  Important findings. All Critical + Important findings fixed in
  the same session before proceeding to M4:
  - **Critical**: `keychain.DeleteRaw` (CLI fallback path,
    `kv.go`) was masking ALL non-zero `*exec.ExitError` exits as
    "not found" via a tautological check. Fixed to match exit
    code 44 (errSecItemNotFound) explicitly; other exits surface
    as wrapped errors. Brings CLI semantics in line with the
    cgo path's `gokeychain.ErrorItemNotFound` check.
  - **Important #2**: Anthropic `Provider.orgIDCache` lifecycle
    was implicit — added `InvalidateAdminKey(adminKey)` method
    + test (`TestProvider_InvalidateAdminKey_ForcesRediscovery`)
    + doc comment naming the TUI's call-site contract on
    rotation. Full migration of OrgID resolution out of Provider
    deferred to M4 per reviewer recommendation.
  - **Important #3**: `Type` string `"admin-key"` was duplicated
    as a local constant in three places. Replaced with imports
    of `vault.TypeAdminKey` from openai, anthropic, and Fake.
    Single source of truth.
  - **Important #4**: 429 + 5xx test coverage was missing.
    Added `TestProvider_RateLimit_NotMappedToSentinel` and
    `TestProvider_5xx_NotMappedToSentinel` to both providers
    asserting that rate-limit and server-error responses don't
    accidentally map to ErrInvalidAdminKey or ErrAlreadyRevoked
    and that upstream messages survive the wrap.
  - **Important #5**: Context cancellation propagation was
    untested. Added `TestProvider_ContextCancellation_Propagates`
    to both providers using a hanging httptest server.
  - **Important #6**: `AdminKeyStore.Set` write order flipped to
    meta-first/admin-second. Half-failure now leaves the store
    with no admin entry → `Get` returns `ErrAdminKeyNotSet`
    cleanly (not corruption), retry-Set overwrites both. Added
    `TestAdminKeyStore_Set_HalfFailureLeavesRecoverable`.
  - **Important #7**: AdminKeyStore.Delete idempotency
    self-resolved by Critical #1 fix — once DeleteRaw correctly
    returns nil only for missing entries, the chained Delete
    naturally yields the right semantics.
  - **Important #8**: `Fake.Now` / `Fake.timeNow()` dead code
    + misleading `nolint:unused` comment removed.
  - **Deferred (Minor)**: paginated-list warning, `WorkspaceID`
    sanity check, namespace reorganization, ReadAll error
    handling. Tracked for post-M4 cleanup; not blocking.

- **2026-04-30** — M4 Phase 1 (provider picker + entity-list
  rendering) landed.
  - `internal/tui/provider_picker.go` + tests — top-level
    `screenProvider` lists Google + admin-key providers + "+ add
    provider" stub. Per-row `●`/`○` glyph for admin-key state;
    summary shows account count (Google) or project count
    (configured admin-key) or "admin key not set" (red).
  - `internal/tui/admin_key_list.go` + tests — entity-list
    screen for OpenAI projects / Anthropic workspaces. Admin-key
    row at top (red/green), project rows in the middle
    (alphabetically sorted), "+ new project"/"+ new workspace"
    affordance at the bottom (muted when admin key not set).
    Phase 1 has navigation + render only; enter/r flash a
    "(action coming in M4 phase 2)" status. Key material is
    redacted to a `sk-…xyz` hint.
  - `internal/tui/model.go` — `screen` enum gains
    `screenProvider` (new top-level) and `screenAdminKeyList`.
    `newModel` routes through provider picker by default;
    initial-account argument still short-circuits to scope view
    (pre-#13 escape hatch). New options:
    `WithAdminKeyProvider(p)` registers a provider + auto-pairs
    an `AdminKeyStore`. Navigation: `esc` from any sub-screen
    returns to the provider picker; `q` from anywhere quits.
  - `internal/tui/picker.go` — OAuth account picker `esc` now
    emits `pickerBackMsg` (back to provider picker) instead of
    quitting; `q`/`ctrl+c` keep program-quit semantics.
  - `cmd/charon/main.go` — `charon auth` wires `openai.New()`
    and `anthropic.New()` providers into the TUI.
  - Test coverage: provider picker rendering with all
    configured-state combinations; navigation; enter dispatches
    correct message types; entity-list rendering for both
    OpenAI/Anthropic; per-provider local naming
    (Projects/Workspaces); key-material redaction; alphabetical
    account sort; cross-provider filtering; full
    provider→adminList→back navigation cycle.
  - Phase 1 deliberately does NOT include actions (paste, mint,
    revoke, replace modal, detail screen). Those are Phase 2/3.
