---
id: 000015
status: open
deps: []
github_issue:
created: 2026-04-28
updated: 2026-04-28
estimate_hours: 45
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

**Range: 30–64 hr (~3–6.5 working days). Best guess: ~45 hr (~4.5 days).**

Produced via `brain/data/life/42shots/velocity/estimate-logic-v1.md` against `baseline-v1.md`. Method A only.

This estimate **assumes #13 ships first** — `internal/providers/` interface, the keychain-storage convention, and the TUI provider-picker scaffolding are reused. The catalog flow integrates as another provider-type on top of that scaffolding. If #15 ships before #13, add ~5–10 hr for picker + basic provider abstractions.

| Milestone | Primitive match | Base hr | Familiarity × | Adjusted |
|---|---|---|---|---|
| M1 — Catalog schema + 13-provider seed YAML (URL/auth research per provider) | Atlas/docs + schema code | 2–5 | ×1.0 | 2–5 |
| M2 — Catalog loader + TUI fuzzy-search picker | Smaller Go module | 3–6 | ×1.0 | 3–6 |
| M3 — Generic metadata-driven per-host router (the meat) | Greenfield Go module (single concern) | 8–14 | ×1.0 | 8–14 |
| M4 — TUI add-account flow ([Open] buttons + paste + e2e test against 3 providers) | Greenfield Go module (TUI) | 6–12 | ×1.0 | 6–12 |
| M5 — `--verify` flag (optional health-check post-paste) | Smaller Go module | 2–4 | ×1.0 | 2–4 |
| M6 — Docs (README, `providers.md`, threat-model notes) | Atlas/docs ×3 | 1–3 | ×1.0 | 1–3 |
| M7 — Onboarding polish (default to catalog for empty-state) | Smaller Go module | 1–3 | ×1.0 | 1–3 |
| Code review × 2 chunks | Process overhead | 2–6 | ×1.0 | 2–6 |
| **Subtotal** | | | | **25–53** |
| **+20% unknown-unknowns buffer** | | | | **30–64** |

Caveats:
- Assumes baseline calibration (10hr/day focused, solo founder + AI, current-baseline polish; see `baseline-v1.md`).
- Assumes #13 ships first (see above). Independent in spec, coupled in scaffolding.
- M1's range is higher than typical "Atlas/docs maintenance" because per-provider URL/auth-shape research is real curation work, not just writing prose.
- Open questions on user-extensible catalog and catalog-update mechanism are explicitly deferred (Notes section), so they don't add to this estimate. If pulled in, +5–10 hr for the user-config merge + ~3–5 hr for a refresh subcommand.
- The "60-second add-provider" UX target in the Notes is a real success criterion — under-shooting M4's polish budget would miss it. The high end of M4 (12 hr) reflects polishing to that bar.

## Plan

Detailed plan in
`workshop/plans/000015-provider-catalog-plan.md` after approval.

Sketch milestones:

1. **M1** — Define catalog schema; commit initial YAML with the
   13 providers above.
2. **M2** — Catalog loader at startup; surface entries in the TUI
   provider picker with fuzzy search.
3. **M3** — Generic per-host router that consumes catalog entries
   for hostname → auth-shape → keychain-entry lookup.
4. **M4** — TUI add-account flow: pick from catalog → "[Open]"
   buttons for signup / key URLs → paste → store. Test against
   ~3 providers end-to-end.
5. **M5** — `--verify` flag (optional health-check post-paste).
6. **M6** — Docs: README mentions the catalog; new
   `docs/providers.md` lists the curated set; threat model notes
   the catalog is curated (not user-extensible to arbitrary URLs
   for security).
7. **M7** — Onboarding polish: when the user first runs `charon
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
