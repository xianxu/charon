---
id: 000015
status: working
deps: []
github_issue:
created: 2026-04-28
updated: 2026-05-03
estimate_hours: 12
estimate_method: estimate-logic-v2.md (Method A)
prior_estimate_hours: 45  # v1 estimate; superseded by v2 after #13 actuals
---

# Provider catalog + onboarding (Tier 3 generalized)

## Problem

Charon's value compounds as it manages more providers, but adding
each provider currently requires:

1. Hardcoding hostname → keychain-entry routing
2. Knowing the right place to create an API key (often deep in
   provider-specific dashboards)
3. Knowing the right auth header shape (`Authorization: Bearer
   <key>` vs `x-api-key: <key>` vs `?key=<key>` URL param)

For the long tail of LLM API providers — Groq, Cohere, Mistral,
xAI, Perplexity, Together, Fireworks, DeepInfra, Replicate, and
the next dozen that ship before this issue lands — there's no
appetite to write per-provider Go code. They all use static
"paste your API key" credentials.

Solution: a **data-driven provider catalog** that turns
"add a new provider" into "pick from a list, click through to the
provider's signup/key-creation page, paste the result". The
typical onboarding flow goes from "30 minutes of doc-hunting" to
"two clicks plus a paste".

This is a meaningful product surface, not just internal plumbing —
it's the discoverability layer that makes the agentic workbench
approachable.

## Goal

A curated, data-driven catalog of API-key providers with:

- Per-provider metadata: name, signup URL, key-creation URL,
  hostname pattern (for proxy routing), auth-header shape,
  optional notes (free tier, model list, etc.).
- TUI integration: fuzzy-search by name, surface the
  signup / key-creation links, accept the pasted key, configure
  routing automatically.
- Easy contribution: catalog lives as YAML/JSON in the repo;
  adding an entry is a one-file PR, no Go code.

## Spec

### Catalog format

`internal/providers/catalog/catalog.yaml` (or similar):

```yaml
- id: groq
  name: Groq
  signup_url: https://console.groq.com/signup
  key_url: https://console.groq.com/keys
  hostname_patterns:
    - api.groq.com
  auth:
    style: bearer
    header: Authorization
    prefix: "Bearer "
  notes: |
    Fast inference, free tier with rate limits. API-compatible with
    OpenAI's chat completions endpoint.

- id: cohere
  name: Cohere
  signup_url: https://dashboard.cohere.com
  key_url: https://dashboard.cohere.com/api-keys
  hostname_patterns:
    - api.cohere.ai
    - api.cohere.com
  auth:
    style: bearer
    header: Authorization
    prefix: "Bearer "

- id: mistral
  name: Mistral La Plateforme
  signup_url: https://console.mistral.ai
  key_url: https://console.mistral.ai/api-keys/
  hostname_patterns:
    - api.mistral.ai
  auth:
    style: bearer
    header: Authorization
    prefix: "Bearer "

# ... (xai, perplexity, together, fireworks, deepinfra, replicate,
#      openrouter, voyage, jina, anyscale, etc.)
```

`auth.style`:
- `bearer` — `Authorization: Bearer <key>` (most providers)
- `header` — custom header (e.g. Anthropic's `x-api-key`)
- `query` — URL parameter (e.g. Google AI Studio's `?key=`)

### TUI flow

```
charon auth
  ▶ Provider:
      [google]      OAuth — Gmail, Drive, Vertex AI, AI Studio
      [openai]      Admin-key managed
      [anthropic]   Admin-key managed
      [add new...]  ▶  fuzzy search

  → add new... → "groq"
    ↳ Groq
      Sign up:    https://console.groq.com/signup     [Open]
      Get key:    https://console.groq.com/keys       [Open]
      Hostname:   api.groq.com
      Auth:       Authorization: Bearer <key>
      Notes:      Fast inference, free tier with rate limits.

      Account name (X-Charon-Account value): _____
      API key: <paste>
      ✓ stored as `groq/<account-name>`.
```

The "[Open]" buttons fire `open <url>` (macOS) so the user can hop
to the dashboard, sign up if needed, mint a key, come back, paste.

### Routing integration

When an account is added under a catalog provider, charon's
per-host routing table picks up the entry automatically:

- Hostname pattern → "look up keychain entry for the Account
  matching `X-Charon-Account` under provider id `groq`, attach as
  per the auth.style".

This means no per-provider Go code gets written for Tier 3
providers. New providers ship by adding a YAML entry.

### Catalog seed

Initial entries to include:

| Provider | Notes |
|---|---|
| Groq | Fast Llama / Mixtral inference |
| Cohere | Cohere's models + RAG embeddings |
| Mistral La Plateforme | Mistral's hosted endpoints |
| xAI | Grok models |
| Perplexity | Sonar, online-search models |
| Together AI | Open-source model hosting |
| Fireworks AI | Function calling, structured output |
| DeepInfra | Cheap open-source hosting |
| Replicate | Image / video / niche models |
| OpenRouter | Aggregator — single key, many models |
| Voyage AI | Embeddings |
| Jina AI | Embeddings + reranking |
| Anyscale Endpoints | Open-source serving |

Tier 1 providers (OpenAI / Anthropic from #000013) already exist as
first-class implementations — they appear in the picker but route
to their own admin-key + mint code path, not through the catalog.
Same for Tier 2 (#000014 Google AI).

The catalog serves the long tail.

### Validation step (optional, gated behind `--verify`)

After the user pastes a key, charon could optionally hit a
provider-defined health endpoint (e.g. `GET /v1/models` for
OpenAI-compatible providers; provider-specific otherwise) to
confirm the key works. Defaults off (extra latency, sometimes
counts against quota); user opts in.

## Estimate

**Range: 6.1–18.5 hr. Best guess: ~12 hr.**

Produced via `brain/data/life/42shots/velocity/estimate-logic-v2.md` against `baseline-v2.md`. Method A only.

v2 supersedes the previous v1 estimate (30–64 hr range). #13's actuals showed v1 over-estimated by ~10×; v2 splits design + impl per primitive and applies a per-API discovery budget. #15's design density is higher than #14's because it introduces a new generic abstraction (catalog schema, metadata-driven router) and curates 13 providers with potentially novel auth shapes — so the v2 reduction is less aggressive (~5×).

This issue's scope expanded after #13 closed: **Anthropic was demoted here** (its Admin API can't programmatically create keys, breaking the mint flow). #15 now also implements the optional revoke-endpoint pathway for catalog providers that support it (Anthropic specifically: `POST /v1/organizations/api_keys/{id}` with `status: inactive`).

| Milestone | Primitive | Spec quality | Design (hr) | Impl (hr) | Total |
|---|---|---|---|---|---|
| M1 — Catalog schema + 13-provider seed YAML (URL/auth/revoke research per provider) | Atlas/docs + schema code | ×0.5 (schema sketched in Spec but per-provider details unknown) | 0.3–0.8 | 0.3–0.8 | 0.6–1.6 |
| M2 — Catalog loader + TUI fuzzy-search picker | Smaller Go + TUI screen | ×0.5 (fuzzy picker UX has open shape) | 0.3–1 | 0.3–1 | 0.6–2 |
| M3 — Generic metadata-driven per-host router | Greenfield Go module (single concern) | ×0.5 (auth.style dispatch design partially open) | 0.3–1.2 | 0.5–1.5 | 0.8–2.7 |
| M4 — TUI add-account flow ([Open] + paste + 3-provider e2e) | TUI screen + state machine | (full design — UX iteration like #13's M4) | 0.5–2 | 0.5–1.5 | 1.0–3.5 |
| M4b — Anthropic revoke pathway (catalog `revoke:` endpoint dispatch) | Smaller Go module (mirror) | ×0.2 (anthropic provider already shipped in #13's tree) | 0.1–0.3 | 0.2–0.5 | 0.3–0.8 |
| M5 — `--verify` flag (post-paste health check) | Smaller Go module | ×0.5 | 0–0.2 | 0.2–0.5 | 0.2–0.7 |
| M6 — Docs ×3 (README, `providers.md`, threat-model) | Atlas/docs ×3 | n/a | 0.15–0.6 | 0.15–0.6 | 0.3–1.2 |
| M7 — Onboarding polish (default to catalog when empty) | Smaller Go module | n/a | 0.1–0.3 | 0.2–0.5 | 0.3–0.8 |
| Code review × 2 chunks | Process overhead | n/a | 0–0.4 | 0.4–1 | 0.4–1.4 |
| Real-API discovery (~3 of 13 seed providers will surprise; ~25% hit rate per typical API) | NEW v2 primitive | n/a | 0 | 0.9–1.8 | 0.9–1.8 |
| **Subtotal (design / impl)** | | | **1.75–6.8** | **3.85–9.7** | **5.4–16.5** |
| **+30% on design subtotal** | | | +0.5–2 | n/a | +0.5–2 |
| **Total** | | | | | **6.1–18.5** |

Caveats:
- Assumes #13 has shipped (it has — closed 2026-04-30). `internal/providers/` interface, `AdminKeyStore`, TUI provider picker, and the credential-lifecycle principle ("manage what you mint; revoke what you touched") are all established.
- The 13 seed providers carry real research cost — per-provider docs, URL paths, auth shapes (header vs URL param vs basic), revoke endpoints if any. Captured in M1 + the discovery-budget primitive.
- M4 is the most design-bound (UX iteration with user, similar to #13's M4 actuals of ~1.5 hr).
- Anthropic's hybrid-paste-then-list-and-deactivate revoke pathway is the gnarliest catalog feature; isolated in M4b for visibility. Other catalog providers (Groq, Mistral, etc.) get the simpler "delete locally + dashboard URL" path documented in `atlas/charon.md`.
- Open questions on user-extensible catalog and catalog-update mechanism are explicitly deferred (Notes section). If pulled in, +1–2 hr for user-config merge + ~0.5–1 hr for refresh subcommand under v2 rates.
- The "60-second add-provider" UX target — M4 high end (3.5 hr) reflects polishing to that bar.

## Plan

Detailed plan in
`workshop/plans/000015-provider-catalog-plan.md` after approval.

Sketch milestones (post-2026-05-03 scope reduction — see `## Log`):

1. **M1** — Define catalog schema; commit initial YAML with
   **Anthropic only** as the seed entry.
2. **M2** — Catalog loader at startup; surface entries in the TUI
   provider picker with filterable list (`bubbles/list` filter is
   sufficient; designed to grow).
3. **M3** — Generic per-host router that consumes catalog entries
   for hostname → auth-shape → keychain-entry lookup. Adds
   `AuthHeader` and renames `AuthURLParamKey` → `AuthQuery`.
4. **M4** — TUI add-account flow: pick from catalog → "[Open]"
   buttons for signup / key URLs → paste → store. End-to-end
   acceptance against Anthropic.
5. **M4b** — Generic catalog revoke dispatcher (list_endpoint +
   revoke_endpoint). Anthropic is the first user.
6. **M5** — `--verify` flag (optional health-check post-paste).
7. **M6** — Docs: README mentions the catalog; new
   `docs/providers.md` frames it as a generic API-key paste-and-
   revoke mechanism (not LLM-specific); threat model notes the
   catalog is curated (not user-extensible to arbitrary URLs).
8. **M7** — Onboarding polish: when the user first runs `charon
   auth` with no providers configured, default to "show catalog"
   rather than the empty-list view.

## Open questions

- **User-extensible catalog**: should users be able to add their
  own catalog entries (e.g. for self-hosted providers,
  cloud-vendor LLM endpoints)? Trade-off: extensibility vs.
  charon vetting providers' auth shapes. Probably yes via a
  user-config YAML at `~/.config/charon/providers.yaml` that
  merges with the built-in catalog.
- **Catalog updates**: how do users get new providers without
  upgrading charon? Options: (a) ship a refresh subcommand that
  fetches the latest catalog from a charon-hosted URL,
  (b) bake-in only and require upgrade. (a) is more convenient
  but adds an update channel; (b) is simpler. Probably start (b)
  and revisit if the catalog churns.
- **Rate-limit / cost reporting**: out of scope for this issue
  but worth noting for follow-on. A "provider quota dashboard"
  inside the TUI would round out the agentic-workbench framing.
- **`auth_source: admin_key_ref` for revoke**: today the catalog
  revoke dispatcher always uses the pasted key itself as the
  bearer for both list and revoke calls. That's correct for
  Anthropic when the pasted key has admin permissions, but for a
  project-scoped pasted key, list returns 401 every time — not
  transient, structural. Schema should grow
  `revoke.auth_source: pasted_key | admin_key_ref` (and
  optionally `admin_key_ref: <provider_id>/<account>`) so the
  dispatcher can pull a separate admin credential from the vault
  for the revoke calls. Out of #15's MVP since the
  default-preserve + `[d]` posture handles the failure
  gracefully today. See chunk-2 review's load-bearing rec.

## Relationship to other issues

- **OpenAI from #000013** stays in #000013 — programmatic mint via
  service accounts works, full lifecycle handled there.
- **Anthropic from #000013 was DEMOTED to this issue** (2026-04-30):
  Anthropic's Admin API can list and deactivate keys but cannot
  create them, so charon's mint flow doesn't apply. Anthropic uses
  the catalog paste flow with the optional revoke endpoint
  (`POST /v1/organizations/api_keys/{id}` with `status: inactive`)
  for the best-effort revoke pathway. The shipped
  `internal/providers/anthropic/` package + admin-key store
  helpers remain available and should be re-used.
- **Independent of #000014** (Google AI). Google's path uses OAuth.
- **Conceptually unifies** with #000006 (multi-provider) — the
  catalog IS the multi-provider mechanism for Tier 3.

## Credential-lifecycle principle (locked in #000013)

> **Charon manages what it minted; charon revokes what it touched.**

Catalog-pasted keys are *not* lifecycle-managed by charon — deletion
in charon is local-only. **Exception**: the catalog declares an
optional revoke endpoint per provider. When present, charon uses it
on deletion (best-effort — failures don't block local deletion).
When absent, the deletion confirmation modal tells the user "removed
locally; please clean up at <provider URL>" with the per-provider
console URL from the catalog.

Schema (sketch):

```yaml
providers:
  - name: anthropic
    auth: { style: x-api-key, header: x-api-key, version_header: anthropic-version, version: "2023-06-01" }
    revoke:
      method: POST
      url: https://api.anthropic.com/v1/organizations/api_keys/{key_id}
      body: '{"status":"inactive"}'
      key_id_source: list_endpoint  # see below
    list_endpoint:
      url: https://api.anthropic.com/v1/organizations/api_keys
      key_match: partial_key_hint   # match pasted-key suffix to find key_id
    console_url: https://console.anthropic.com/settings/admin-keys
  - name: groq
    auth: { style: bearer }
    # no revoke entry → fall back to local-delete + console_url message
    console_url: https://console.groq.com/keys
```

For Anthropic, charon needs to first call the list endpoint to find
the `key_id` for a pasted key (matching by partial key hint), then
call the revoke endpoint. For providers without a list+revoke
combination, deletion is local-only with a user-facing pointer to
the dashboard.

See `atlas/charon.md` § "Credential lifecycle principle" for the
broader rationale.

## Notes

- This is a UX play as much as a technical one. The metric of
  success is "how long does it take a new user to add provider X
  to charon for the first time." Aim for under 60 seconds from
  `charon auth → add new` to a working `X-Charon-Account` swap.
- Curating the catalog is ongoing work (#000012 item I called
  this out for KnownApps; same applies here). Bundle IDs and
  hostname patterns shift annually; PRs happen as needed.
- **Revoke modal default posture: preserve credential on upstream
  failure.** Catalog credentials are *handles* — charon's only
  way to revoke or otherwise touch the upstream key. When the
  upstream revoke call fails (transient: rate limit, network;
  fixable: auth-scope error on the pasted key), the default
  action of any non-`[d]` key is to cancel and keep the local
  credential so the user can retry without re-pasting. `[d]` is
  the explicit "force local-delete anyway" affordance for cases
  where retry will never succeed (key already revoked at
  provider, provider deprecated, permanent auth-scope mismatch
  the user accepts). This inverts the original M4b posture
  ("any key falls back to local-delete") which mirrored the
  OAuth Google flow — but Google's flow has different shape
  (the user already wanted the OAuth account gone, AI Studio
  cleanup is opportunistic). For catalog the credential *is*
  the handle, so default-preserve is the correct tradeoff.

## Log

- **2026-05-04 — Chunk-2 review (M4b + 5 followups).**
  superpowers-code-reviewer subagent against `BASE=6f0baf4` →
  `HEAD=a7d31f4` (10 commits: M4b proper, design-fix to keep-on-fail
  with `[d]` force-delete, OSC 8 hyperlinks, action-hint visibility,
  enter-on-account no-op, in-session cursor memory across screens,
  Makefile dev-unsign). Verdict: ready to merge with fixes.
  0 Critical, 3 Important, 9 Minor.
  Important addressed in `<post-chunk2-commit>`:
  - **#1 vault.Delete dead-end on `[d]`**: when local-delete fails
    after an upstream-fail (keychain locked, ACL denial), the user
    was stuck on the upstream-failed overlay with `[d]` looping
    against the same failure. Now exits cleanly with a status note
    naming both the upstream cause and the local-delete error;
    user can retry from the account list once the underlying issue
    is fixed. Test
    `TestCatalogRevoke_DKey_LocalDeleteFailure_ExitsCleanlyNotStuck`.
  - **#2 dead `AuthSource` field**: `Revoke.AuthSource` was
    enforced by validator (`pasted_key` only) but ignored by the
    dispatcher. YAGNI cleanup — field, YAML, validator, comment,
    and 4 test fixtures dropped. When a real second auth_source
    arrives (likely `admin_key_ref` for non-admin pasted keys —
    see Open Questions), schema grows back with a properly-
    threaded enum and dispatcher branch. Posture decision pinned
    in `internal/providers/catalog/catalog.go:62-72`.
  - **#3 no http timeout backstop**: `lookupKeyID` and `callRevoke`
    were using `http.DefaultClient`. TUI passes a 30s ctx already
    so the bug is latent, but a future non-TUI caller (CLI, batch
    tool) using background context would hang on a slow upstream.
    Replaced with a package-local `httpClient` carrying a 30s
    timeout.
  Minors landed opportunistically:
  - parseResultPath error messages now include the supported shape
    (`<array>[].<field>`), so a catalog-author hitting validation
    sees what to write instead of just what's wrong.
  - Catalog-revoke upstream-failed overlay restricts cancel to
    explicit `[esc/n/enter]` instead of catch-all "any other key";
    a stray keystroke no longer silently abandons the flow.
  - `newCatalogAccountListModel` carries a cost note explaining
    why per-back-nav vault.List churn is intentional and where the
    caching seam is if it ever becomes a problem.
  Other Minors deferred (hyperlink URL escape comment, magic
  isAddNew check helper extraction, padOrTrunc rune-width).

  Recommendations from the review (architectural, not blocking):
  - **Latent failure mode for non-admin pasted keys**: when a user
    pastes a project-scoped Anthropic key, `RevokeKey` will 401
    on list every time — not transient, structural. Today's
    posture (best-effort revoke + `[d]` force-delete) handles
    this gracefully but the schema should grow `auth_source:
    pasted_key | admin_key_ref` so a separate admin credential
    can authenticate the revoke when present. Filed as future
    schema evolution; out of #15's MVP.
  - **Default-preserve revoke posture**: pin the reasoning
    ("catalog credential is charon's *handle* on the upstream
    key; throwing the handle away on transient failure forces
    re-paste") into the design notes here, not just code
    comments. (Done — see Notes section.)
  - **Picker rebuild on every refresh**: acceptable today; revisit
    if keychain churn shows up in profiling.

- **2026-05-03 — M4 e2e verified.** TUI add-account flow landed in
  `fd4daf2`: `catalogPasteModel` (account-name → masked key paste;
  ctrl+o launches `key_url` via `open` on darwin / `xdg-open` on
  linux). On done, the provider picker shows a ready-to-run
  `charon run -- curl -H "X-Charon-Account: <name>" https://<host>`
  hint pulled from the entry's first hostname pattern. 7 teatest
  cases (happy path, esc-from-account cancel, empty-name no-advance,
  esc-from-key preserves account + clears key, empty-key no-store,
  view URL/host/ctrl+o visibility, masking).
  E2e against production proxy: pasted real Anthropic key under
  `personal`, then `charon run -- curl https://api.anthropic.com/v1/messages`
  with `X-Charon-Account: personal` and a `claude-opus-4-7` request
  body → 200 with content payload. `x-api-key` + `anthropic-version`
  injected by the M3 catalog router as designed; no client-side
  auth headers needed. Picker integration for catalog credentials
  (visible row per provider with credential count) deferred to M4b
  since revoke selection needs that row anyway.

- **2026-05-03 — Chunk-1 review (M1+M2+M3).** `superpowers-code-reviewer`
  subagent; BASE=`9843ab4`, HEAD=`8bcf871`. Verdict: ready to merge with
  fixes. 0 Critical, 4 Important, 8 Minor.
  Important fixes addressed in `<post-review-commit>`:
  - Suffix-collision detection in `catalog.Register` (skip+log when a
    catalog hostname falls under a compiled `SuffixToProvider` rule);
    new `proxy.MatchingSuffix` helper exposes the lookup.
  - Duplicate-hostname-across-entries rejected at `catalog.validate`
    load time so PR conflicts surface at boot, not first request.
  - `atlas/charon.md` updated for `query` (was `url_param_key`); added a
    new "Auth methods" bullet documenting the three styles.
  - `TestRegister_DoesNotOverrideCompiledHosts` defensive `t.Cleanup`
    so the assertion stays meaningful if a future test mutates the map.
  Minors landed opportunistically: `{key_id}` placeholder in
  `revoke.url` now requires `list_endpoint` at validate time;
  AI-Studio-vs-catalog disambiguation locked by a new table-driven
  test in `internal/proxy/proxy_test.go`. Other Minors (cosmetic
  picker render duplication, defensive nil-URL guard, DRY of
  cred-resolve branches, 2x `catalog.Load()` at startup) deferred.

- **2026-05-03 — Scope reduction.** Catalog mechanism preserved;
  seed shrunk from 13 LLM-inference providers to **Anthropic only**.
  Reframed as a generic API-key paste-and-revoke catalog (not LLM-
  specific) — API-key auth covers many use cases beyond inference,
  and the data-driven mechanism is worth building even with one
  initial entry. The 12 dropped LLM providers (Groq, Cohere, Mistral,
  xAI, Perplexity, Together, Fireworks, DeepInfra, Replicate,
  OpenRouter, Voyage, Jina, Anyscale) become aspirational fodder —
  added as YAML PRs when actually needed. Revised estimate ~7–8 hr
  (vs. v2 best-guess of 12 hr). Detailed plan at
  `workshop/plans/000015-provider-catalog-plan.md`.
