---
id: 000014
status: working
deps: []
github_issue:
created: 2026-04-28
updated: 2026-05-01
estimate_hours: 5
estimate_method: estimate-logic-v2.md (Method A)
prior_estimate_hours: 25  # v1 estimate; superseded by v2 after #13 actuals
---

# Google AI providers: Gemini AI Studio + Vertex AI

## Problem

Google has two completely separate doors to the same Gemini models:

- **Google AI Studio** — API-key based. Endpoint
  `https://generativelanguage.googleapis.com`. Created via
  <https://aistudio.google.com/apikey>. Free tier; simpler;
  primarily for prototyping / hobbyist use.
- **Vertex AI** — GCP-native. Endpoint
  `https://{region}-aiplatform.googleapis.com`. OAuth or
  service-account-credential authenticated. Pay-as-you-go;
  enterprise features (private endpoints, IAM, regional control).

Both are accessible via OAuth-mediated APIs:

- AI Studio: keys can be **minted programmatically** via Google's
  API Keys API (`apikeys.googleapis.com`) when authenticated with
  appropriate Google Cloud OAuth scopes.
- Vertex: doesn't need a separate API key at all — the OAuth token
  itself authenticates requests to the Vertex endpoint.

This is convenient for charon because it already does Google OAuth
for the existing Gmail/Drive provider. Extend the scope set, get
both Gemini doors for free.

## Goal

Both Gemini paths usable through charon with the existing Google
OAuth account, no separate admin-key paste step (in contrast to
[#000013](000013-api-key-providers-openai-anthropic.md) where
OpenAI/Anthropic require a manual admin-key step).

The agent specifies which path via the upstream URL (charon routes
based on hostname); `X-Charon-Account` selects the Google account.

## Spec

### Google AI Studio path (programmatic key mint)

- Add the `https://www.googleapis.com/auth/cloud-platform` scope (or
  the narrower `https://www.googleapis.com/auth/cloud-platform.read-only`
  + a specific apikeys scope if Google offers one) to charon's
  Google scope catalog. User opts in via the existing TUI scope-
  selection flow.
- When the user adds an account or specifically requests an AI
  Studio key, charon calls the API Keys API to mint:

  ```
  POST https://apikeys.googleapis.com/v2/projects/{project_id}/locations/global/keys
  ```

  with restrictions in the request body limiting the key to
  `generativelanguage.googleapis.com` (the `apiTargets` field).
  Response contains the secret string; capture and store in Keychain
  alongside the OAuth refresh token.

- Per-host routing: when the agent calls
  `generativelanguage.googleapis.com`, charon attaches the minted
  AI Studio API key (as `?key=...` URL parameter or
  `Authorization: Bearer` — both work).

### Vertex AI path (OAuth tokens directly)

- Same `cloud-platform` scope grant covers Vertex.
- Per-host routing: when the agent calls
  `{region}-aiplatform.googleapis.com`, charon attaches the
  refreshed OAuth access token as `Authorization: Bearer <token>`.
  No API-key minting step — Google's OAuth token is the credential.

- Region selection: Vertex is region-scoped. charon can either
  default to a region (configurable) or let the agent specify via
  the URL it requests.

### TUI

Mostly free with the existing Google OAuth flow. Adds:

- A scope-picker entry for `cloud-platform` (or finer-grained
  scopes if Google publishes them) with help text explaining
  "enables Gemini API access via AI Studio + Vertex".
- Optional: an explicit `charon accounts add google:my-account ai-studio`
  subcommand that mints the AI Studio key + records its key_id for
  later revocation. For Vertex-only use, the OAuth token alone is
  sufficient and no extra step is needed.

### Storage shape

Per Google account (e.g. `xianxu@gmail.com`):

- `google:xianxu@gmail.com` — OAuth refresh token (existing)
- `google:xianxu@gmail.com:aistudio` — minted AI Studio API key +
  key_id metadata (NEW; only when AI Studio path is used)

Vertex needs no new entries; uses the existing OAuth token.

## Estimate

**Range: 2.4–7.3 hr. Best guess: ~4.5 hr.**

Produced via `brain/data/life/42shots/velocity/estimate-logic-v2.md` against `baseline-v2.md`. Method A only.

v2 supersedes the previous v1 estimate (17–36 hr range) because v1 mashed design and impl into one range; AI-paired impl collapsed 5–15× since the v1 baseline. The shipped charon #13 actual was ~5 hr against a v1 estimate of 41–78 hr.

This estimate **assumes #13 has shipped** — `internal/providers/` interface, `AdminKeyStore`, the TUI provider picker / entity-list patterns, and the threat-model admin-key asset class are all established. M3 (AI Studio key mint) is essentially a mirror of #13's `internal/providers/openai/` package against a different upstream API.

| Milestone | Primitive | Spec quality | Design (hr) | Impl (hr) | Total |
|---|---|---|---|---|---|
| M1 — Add `cloud-platform` scope to Google catalog | Atlas/docs + tiny code | spec pre-resolved | 0.05–0.2 | 0.05–0.2 | 0.1–0.4 |
| M2 — Vertex routing (per-host + OAuth bearer + smoke test) | Smaller Go module (mirror M5 from #13) | ×0.2 (mirror existing) | 0–0.06 | 0.2–0.5 | 0.2–0.56 |
| M3 — AI Studio key mint (API Keys API client + Keychain + project/API-enablement detection) | Greenfield Go module (mirror OpenAI provider shape) | ×0.2 (#13 set the pattern) | 0.1–0.4 | 0.3–0.8 | 0.4–1.2 |
| M4 — AI Studio routing (URL-param key attach) | Smaller Go module + new auth method | ×0.5 (URL-param is genuinely novel auth) | 0.1–0.5 | 0.2–0.5 | 0.3–1.0 |
| M5 — Revoke flow (API Keys DELETE on `accounts rm`) | Smaller Go module (mirror) | ×0.2 | 0–0.06 | 0.2–0.5 | 0.2–0.56 |
| M6 — Docs (README, agent-protocol, threat-model touch-up) | Atlas/docs ×3 | n/a | 0.15–0.6 | 0.15–0.6 | 0.3–1.2 |
| Code review × 1–2 chunks | Process overhead | n/a | 0–0.4 | 0.4–1 | 0.4–1.4 |
| Real-API discovery (1 external API: Google AI Studio API Keys) | NEW v2 primitive | n/a | 0 | 0.3–0.6 | 0.3–0.6 |
| **Subtotal (design / impl)** | | | **0.4–2.2** | **1.85–4.7** | **2.25–6.9** |
| **+30% on design subtotal** | | | +0.12–0.66 | n/a | +0.12–0.66 |
| **Total** | | | | | **2.4–7.6** |

Caveats:
- Assumes #13 shipped (it has — closed 2026-04-30). The mirror-pattern discount applies.
- M4's URL-param auth is genuinely novel — Google AI Studio uses `?key=` query param, not Bearer header. New `AuthMethod` const + injection codepath. Wider design range than other M-numbers.
- Open question on scope granularity (`cloud-platform` broad, narrower might exist) is bounded research — ~5 min, absorbed in M1.
- Region selection for Vertex: assumed config or per-request, not a TUI feature. If full TUI support added, +0.5 hr design + 0.3 hr impl on M2.

## Plan

Detailed plan in
`workshop/plans/000014-google-ai-providers-plan.md` after approval.

Sketch milestones:

1. **M1** — Add `cloud-platform` scope to Google scope catalog
   (`internal/oauth/google/scopes.go` or wherever scopes live).
   Existing TUI surfaces it for opt-in. **[done 2026-05-01]**
2. **M2** — Vertex routing. Add `*.aiplatform.googleapis.com` to
   the per-host routing table; attach OAuth bearer. Smoke test
   with a Gemini request via Vertex endpoint.
3. **M3** — AI Studio key mint flow. New `aistudio` subcommand or
   account-level mint trigger. Integrates with API Keys API.
4. **M4** — AI Studio routing. Add
   `generativelanguage.googleapis.com` to routing; attach the
   minted key (URL param or bearer).
5. **M5** — Revoke flow on `accounts rm`: if the account has an
   `:aistudio` key, call the API Keys API DELETE to revoke before
   deletion from Keychain.
6. **M6** — Docs: update README, agent-protocol, threat model.

## Relationship to #000013

- **#000013 (OpenAI + Anthropic)**: forced into the manual-admin-key
  pattern because those providers don't expose OAuth-for-admin.
- **#000014 (Google AI)**: uses the existing OAuth flow; AI Studio
  keys are minted via Google's standard cloud APIs.

The two issues share zero code paths but the same agent-facing
shape (`X-Charon-Account` + route by hostname). Implement
independently.

## Open questions

- **Scope granularity**: `cloud-platform` is broad (covers ~all
  GCP APIs). Worth investigating whether Google offers narrower
  scopes (e.g. `apikeys.admin` + `aiplatform.user`) and using
  those instead. The audit is more honest about what charon can
  do if scopes are minimized.
- **Service accounts vs OAuth user accounts**: Vertex commonly
  uses GCP service accounts (JSON key file) for production. For
  personal-use charon, OAuth user accounts are fine. If charon
  ever needs to support service accounts, that's a separate
  credential-storage shape (private key file content rather than
  refresh token).
- **Region defaults**: should the user pick a Vertex region at
  account-add time, or per-request? Probably account-add (sensible
  default; agent can override via URL).

## Notes

- AI Studio and Vertex have **separate quotas and pricing models**.
  AI Studio has a free tier; Vertex doesn't. Worth surfacing this
  in the TUI's account-add flow.
- AI Studio keys can be restricted to specific APIs at mint time
  via the `apiTargets` field. Charon should always scope the keys
  it mints to `generativelanguage.googleapis.com` only — defense
  in depth in case the key leaks.
- Google's `apikeys.googleapis.com` API has its own quotas and
  permission requirements; the user's GCP project needs the API
  Keys API enabled. charon should detect "API not enabled" errors
  and prompt the user with the enable URL.

## Log

- **2026-05-01 — M1 done.** Added `cloud-platform` scope (short
  `cloud-platform`) to `internal/oauth/scope_catalog.go`. Description
  surfaces the breadth tradeoff. Open question on narrower scopes
  resolved: Google does not publish narrower OAuth scopes for either
  Gemini API path — `cloud-platform` is the documented requirement
  for both Vertex AI and the API Keys API. TUI scope picker surfaces
  this automatically (catalog-driven). Test added in
  `scope_catalog_test.go`.
