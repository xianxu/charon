---
id: 000013
status: open
deps: []
github_issue:
created: 2026-04-28
updated: 2026-04-28
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

## Notes

- This expands charon's threat-model assets. The admin key is
  arguably the highest-value credential charon holds — owning it
  lets an attacker mint keys with full org access, see usage data,
  modify rate limits, etc. M4 ACL is sufficient (same boundary as
  OAuth refresh tokens), but the threat model should call it out.
- Per-account API keys are functionally similar to OAuth refresh
  tokens — long-lived, revocable upstream. Same storage path.
