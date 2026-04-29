---
id: 000015
status: open
deps: []
github_issue:
created: 2026-04-28
updated: 2026-04-28
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

- **Independent of #000013** (OpenAI/Anthropic). Those providers
  have richer admin-API integration; they don't go through the
  catalog. The catalog covers the static-key tail.
- **Independent of #000014** (Google AI). Google's path uses OAuth.
- **Conceptually unifies** with #000006 (multi-provider) — the
  catalog IS the multi-provider mechanism for Tier 3.

## Notes

- This is a UX play as much as a technical one. The metric of
  success is "how long does it take a new user to add provider X
  to charon for the first time." Aim for under 60 seconds from
  `charon auth → add new` to a working `X-Charon-Account` swap.
- Curating the catalog is ongoing work (#000012 item I called
  this out for KnownApps; same applies here). Bundle IDs and
  hostname patterns shift annually; PRs happen as needed.
