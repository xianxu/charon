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
`workshop/plans/000013-api-key-providers-openai-anthropic-plan.md`
(written after issue approval).

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
