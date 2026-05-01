---
id: 000014
status: working
deps: []
github_issue:
created: 2026-04-28
updated: 2026-05-01
estimate_hours: 7.5
estimate_method: estimate-logic-v2.md (Method A)
prior_estimate_hours: 5  # v2 pre-amendment estimate; superseded after M3 added (project management)
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

- `google:xianxu@gmail.com` — OAuth refresh token (existing). When
  the user has run M3's project setup, this credential also carries
  a `gcp` sidecar payload: `{project_id, project_name, parent,
  vertex_region, created_by_charon, billing_enabled, updated_at}`.
  Required by Vertex (URL contains project + region) and by the AI
  Studio key-mint URL. `parent` is `{type: "organization"|"folder",
  id: "..."}` or `null`; MVP always writes `null` (no UI to pick
  org), but the field is present so a later org-aware flow needs
  only UI changes, no schema migration.

  *Design note:* GCP metadata lives on the same credential as the
  OAuth tokens — same Google account, same lifecycle, same ACL.
  Earlier draft proposed a sibling keychain entry
  `google:<account>:gcp`; reverted because that pattern requires
  fold-into-parent logic in `charon manifest`, two-write
  coordination on creation, and two-delete on revoke for no
  meaningful benefit when both pieces share a lifecycle.

- `google:xianxu@gmail.com:aistudio` — minted AI Studio API key +
  key_id metadata (NEW; only when AI Studio path is used). Stays
  in a sibling entry because the API key itself is a distinct
  secret with its own lifecycle (mint / revoke independent of
  OAuth refresh, separately revocable upstream).

The `:gcp` entry is the bridge: OAuth gives us *who you are*, but
Vertex's URLs are `/v1/projects/{PROJECT_ID}/locations/{REGION}/...`
and the AI Studio mint URL is
`apikeys.googleapis.com/v2/projects/{project_id}/...`. Without it
agents have to discover the project out-of-band, defeating the
manifest-as-onestop goal.

### Manifest impact

The new `:gcp` entry surfaces in `charon manifest` so agents can
construct Vertex URLs without environment variables. Permissions
shape extends from `{account: [scopes]}` to
`{account: {scopes: [...], gcp: {project_id, project_name, vertex_region}}}`
when `cloud-platform` is granted. Backward shape (just a scope list)
is preserved for accounts without GCP setup.

### Lifecycle of GCP artifacts

Following the same "if charon didn't create it, charon doesn't delete
it" principle from #13, **and additionally: charon does not delete
projects it did create**. Rationale:

- Projects can hold non-charon resources the user added (Vertex
  models, datasets, keys, billing data). Bulk-deleting on
  `accounts rm` would be destructive beyond charon's scope.
- Google Cloud Console offers safer deletion semantics
  (30-day soft delete, billing review, dependency check).
- The AI Studio key minted under the project still gets
  revoked on `accounts rm` (M6) — that's a charon-owned
  artifact and follows the standard rule.

Charon's project actions (M3) are create-and-track only:
- create project (recorded so future revoke flows can reference it),
- enable required APIs in it (idempotent),
- store project_id alongside the credential.

When the user removes the `cloud-platform` scope or revokes the
account, charon deletes the `:gcp` keychain entry but leaves the
GCP project standing. Logged in audit so the user can find it
later if they want to clean up.

## Estimate

**Range: 3.9–11.0 hr. Best guess: ~7.5 hr.** (Pre-amendment: 2.4–7.6
hr / ~4.5 hr; the new M3 milestone for GCP project management adds
~2 hr to the midpoint and a third external API to the v2 discovery
budget.)

Produced via `brain/data/life/42shots/velocity/estimate-logic-v2.md` against `baseline-v2.md`. Method A only.

v2 supersedes the previous v1 estimate (17–36 hr range) because v1 mashed design and impl into one range; AI-paired impl collapsed 5–15× since the v1 baseline. The shipped charon #13 actual was ~5 hr against a v1 estimate of 41–78 hr.

This estimate **assumes #13 has shipped** — `internal/providers/` interface, `AdminKeyStore`, the TUI provider picker / entity-list patterns, and the threat-model admin-key asset class are all established. M3 (AI Studio key mint) is essentially a mirror of #13's `internal/providers/openai/` package against a different upstream API.

| Milestone | Primitive | Spec quality | Design (hr) | Impl (hr) | Total |
|---|---|---|---|---|---|
| M1 — Add `cloud-platform` scope to Google catalog | Atlas/docs + tiny code | spec pre-resolved | 0.05–0.2 | 0.05–0.2 | 0.1–0.4 |
| M2 — Vertex routing (per-host + OAuth bearer + smoke test) | Smaller Go module (mirror M5 from #13) | ×0.2 (mirror existing) | 0–0.06 | 0.2–0.5 | 0.2–0.56 |
| M3 — GCP project management (list + create + API-enable + region picker, TUI integration, store `:gcp` entry, manifest surface) | Greenfield Go module + new TUI flow | ×0.5 (some pattern reuse from #13 entity-list, but cross-API orchestration is new) | 0.3–0.8 | 0.5–1.4 | 0.8–2.2 |
| M4 — AI Studio key mint (API Keys API client + Keychain) | Greenfield Go module (mirror OpenAI provider shape; project_id now resolved by M3) | ×0.2 (#13 + M3 set the pattern) | 0.1–0.3 | 0.3–0.7 | 0.4–1.0 |
| M5 — AI Studio routing (URL-param key attach) | Smaller Go module + new auth method | ×0.5 (URL-param is genuinely novel auth) | 0.1–0.5 | 0.2–0.5 | 0.3–1.0 |
| M6 — Revoke flow (API Keys DELETE on `accounts rm`; `:gcp` entry deleted, project preserved) | Smaller Go module (mirror) | ×0.2 | 0–0.06 | 0.2–0.5 | 0.2–0.56 |
| M7 — Docs (README, agent-protocol, threat-model touch-up) | Atlas/docs ×3 | n/a | 0.15–0.6 | 0.15–0.6 | 0.3–1.2 |
| Code review × 1–2 chunks | Process overhead | n/a | 0–0.4 | 0.4–1 | 0.4–1.4 |
| Real-API discovery (3 external APIs: API Keys, Cloud Resource Manager, Service Usage) | NEW v2 primitive | n/a | 0 | 0.9–1.8 | 0.9–1.8 |
| **Subtotal (design / impl)** | | | **0.7–2.92** | **2.95–7.2** | **3.65–10.12** |
| **+30% on design subtotal** | | | +0.21–0.88 | n/a | +0.21–0.88 |
| **Total** | | | | | **3.86–11.0** |

Caveats:
- Assumes #13 shipped (it has — closed 2026-04-30). The mirror-pattern discount applies.
- **M3 is the heaviest add since the original spec.** It orchestrates three APIs (Cloud Resource Manager for list/create, Service Usage for API enablement, and the Service Usage operation polling for create completion) plus a new TUI flow. Triggered when the user grants `cloud-platform` — until that point the cost is zero.
- M5's URL-param auth is genuinely novel — Google AI Studio uses `?key=` query param, not Bearer header. New `AuthMethod` const + injection codepath. Wider design range than other M-numbers.
- Open question on scope granularity (`cloud-platform` broad, narrower might exist) is bounded research — ~5 min, absorbed in M1.
- Vertex region selection is now first-class (part of M3) rather than a config or per-request decision. Default `us-central1`; user picks at project-setup time.

## Plan

Detailed plan in
`workshop/plans/000014-google-ai-providers-plan.md` after approval.

Sketch milestones:

1. **M1** — Add `cloud-platform` scope to Google scope catalog
   (`internal/oauth/google/scopes.go` or wherever scopes live).
   Existing TUI surfaces it for opt-in. **[done 2026-05-01]**
2. **M2** — Vertex routing. Add `*.aiplatform.googleapis.com` to
   the per-host routing table; attach OAuth bearer. Smoke test
   with a Gemini request via Vertex endpoint. **[done 2026-05-01;
   no code change needed — existing `.googleapis.com` suffix rule
   already routes Vertex regional hosts. Added regression test.]**
3. **M3** — Google Cloud project management. Triggered when the user
   grants `cloud-platform` in the TUI. After the OAuth grant
   completes, charon:
   1. Calls `cloudresourcemanager.googleapis.com/v1/projects` to
      list projects the user has access to.
   2. Presents a TUI picker:
      - existing projects (id + display name + lifecycle state),
      - "+ Create new project" → prompts for display name; charon
        generates a project id (`charon-gemini-<random>` or
        user-typed); calls `projects.create`; polls the returned
        long-running operation until ACTIVE.
   3. Region picker: list of supported Vertex regions (`us-central1`
      default, `us-east1`, `europe-west4`, `asia-northeast1`, etc.);
      can be edited later.
   4. Enable required APIs in the chosen project via
      `serviceusage.googleapis.com/v1/projects/{id}/services:batchEnable`:
      `aiplatform.googleapis.com` (Vertex data plane),
      `apikeys.googleapis.com` (M4 key mint),
      `generativelanguage.googleapis.com` (AI Studio data plane).
      Idempotent.
   5. Detect billing state. GET
      `cloudbilling.googleapis.com/v1/projects/{id}/billingInfo`.
      If `billingEnabled: false`, print a non-fatal warning to
      the TUI: "AI Studio (free tier) will work, but Vertex calls
      will return 403 BILLING_DISABLED until you link a billing
      account at
      https://console.cloud.google.com/billing/linkedaccount?project={id}".
      Charon does not attempt to attach billing.
   6. Stores `{project_id, project_name, parent, vertex_region,
      created_by_charon, billing_enabled, updated_at}` as the
      `gcp` sidecar on the existing `google:<account>` OAuth
      credential (same keychain entry, same ACL — see Storage
      shape for the design note). `parent` is `null` for MVP
      (no UI to pick org); the field exists in the schema so a
      future org-aware flow is a UI-only change.
      `created_by_charon` and `billing_enabled` are informational
      — see Lifecycle of GCP artifacts (charon never deletes
      projects regardless).
   7. Surfaces in `charon manifest` under
      `permissions.google.<account>.gcp` (sibling to `scopes`).

   Recovery flow: if the user already has a `:gcp` entry but it's
   stale (project deleted upstream, or APIs disabled), `charon auth`
   detects the failure on next OAuth refresh / Vertex call and
   offers to re-run the project picker.

4. **M4** — AI Studio key mint flow. New `aistudio` subcommand or
   account-level mint trigger. Uses the project_id from the `:gcp`
   entry (set by M3). Integrates with API Keys API.
5. **M5** — AI Studio routing. Add
   `generativelanguage.googleapis.com` to routing; attach the
   minted key (URL param or bearer).
6. **M6** — Revoke flow on `accounts rm`: if the account has an
   `:aistudio` key, call the API Keys API DELETE to revoke before
   deletion from Keychain. The `:gcp` entry is also deleted from
   Keychain; the GCP project itself is preserved (see Lifecycle
   of GCP artifacts).
7. **M7** — Docs: update README, agent-protocol, threat model.

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
  do if scopes are minimized. **[resolved during M1: Google does
  not publish narrower OAuth scopes for Vertex AI or the API Keys
  API. `cloud-platform` is the documented requirement for both.]**
- **Service accounts vs OAuth user accounts**: Vertex commonly
  uses GCP service accounts (JSON key file) for production. For
  personal-use charon, OAuth user accounts are fine. If charon
  ever needs to support service accounts, that's a separate
  credential-storage shape (private key file content rather than
  refresh token).
- **Region defaults**: should the user pick a Vertex region at
  account-add time, or per-request? **[resolved: account-add time,
  via M3's region picker. Stored in `:gcp` entry. Agents can still
  override per-request via URL — that takes precedence.]**
- **Project under organization vs no-org**: **[resolved for MVP:
  personal use, no parent org. UI defers org listing/picking. But
  the backend data shape stores an optional `parent` field
  (`{type, id}` or null) on the `:gcp` entry from day one so a
  later org-aware UI doesn't require a migration.]** Cross-cutting
  follow-up: charon-wide single-org assumption (also present in
  #13's OpenAI/Anthropic admin-key flows) will get a unified
  multi-org treatment in a separate issue; until then individual
  providers each keep room in their data shape but don't surface
  org pickers.
- **Quota / billing**: **[resolved: M3 detects, doesn't fix.]**
  After project create or selection, charon calls
  `cloudbilling.googleapis.com/v1/projects/{id}/billingInfo` and
  inspects `billingEnabled`. If false, surfaces a clear message
  with the actionable link
  `https://console.cloud.google.com/billing/linkedaccount?project={id}`.
  Charon never auto-attaches billing — that decision belongs in
  Cloud Console where the user can review pricing, set budgets,
  and pick the right billing account. AI Studio (free tier) still
  works regardless; the warning specifically calls out that Vertex
  calls will fail with `BILLING_DISABLED` until linked.

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

- **2026-05-01 — TUI polish.** Granted scopes float to top in the
  scope picker (stable sort by realized; preserves catalog order
  within each group). At load and after Apply only — toggling a
  checkbox does not reflow.

- **2026-05-01 — M2 done. Zero code.** Routing was already there:
  `internal/proxy/routing.go` has a `.googleapis.com` suffix rule that
  routes all Google hosts to `{google, bearer}`. Vertex's regional
  hosts (`{region}-aiplatform.googleapis.com`), AI Studio's host
  (`generativelanguage.googleapis.com`), and the API Keys mint host
  (`apikeys.googleapis.com`) all match the existing rule and get the
  user's OAuth access token attached as `Authorization: Bearer`.
  Added explicit hosts to `routing_test.go` as a regression guard.
  Atlas updated. Smoke test deferred to user (requires real account
  with `cloud-platform` granted + a real Gemini call).

- **2026-05-01 — Spec amended: new M3 = GCP project management.**
  Smoke-testing M2 surfaced that OAuth tokens alone are insufficient
  for Vertex (URLs need project+region) and for AI Studio key minting
  (mint URL needs project_id). Added a full project-management
  milestone: list/create projects, enable APIs, pick region, store
  metadata as a sidecar on the `google:<account>` OAuth credential,
  surface in `charon manifest`.
  Old M3–M6 renumbered to M4–M7. Storage shape, manifest impact, and
  GCP-artifact lifecycle (charon doesn't delete projects, even ones
  it created) documented. Estimate revised from 2.4–7.6 to 3.86–11.0
  hr; the additional ~2 hr is M3 itself plus two new external APIs
  (Cloud Resource Manager, Service Usage) for v2-method discovery.

- **2026-05-01 — M3 chunk-1: GCP API client landed.**
  `internal/providers/gcp/` package shipped with `Client`, paginated
  `ListProjects` (filters non-ACTIVE), `CreateProject` +
  `WaitOperation`, idempotent `BatchEnableServices` (sync + async
  paths), and `GetBillingInfo`. Auth via `TokenSupplier` callback
  so the package stays free of refresh logic. PollInterval is a
  Client field (default 2s, tests override to 1ms). httptest-mocked
  tests for: token-supplier failure, error-body preservation,
  pagination + ACTIVE filtering, create-then-poll, operation error
  propagation, context cancel during poll, sync vs async batch
  enable, both billing states.

- **2026-05-01 — M3 chunk-2: vault schema + manifest shape.**
  Added `vault.GCPData` and `vault.GCPParent` types; `Credential`
  gets an optional `GCP *GCPData` sidecar (augments TypeOAuth, does
  not displace it). Spec updated: GCP metadata lives **inline on
  the OAuth credential**, not in a sibling `:gcp` keychain entry.
  Reasoning: same Google account, same lifecycle, same ACL — sibling
  entry would force fold-into-parent logic in manifest, two-write
  coordination on creation, and two-delete on revoke for no
  benefit. AI Studio key (M4) stays separate because that's a
  distinct secret with its own lifecycle.

  `manifest`'s permissions shape evolves from `[scopes]` to
  `{scopes, gcp?}` per account. Backward-compat for accounts
  without GCP setup is automatic (`omitempty` on the gcp field).
  Tests cover: GCP surfaces when present, omitted when absent,
  JSON shape with both branches.

- **2026-05-01 — M3 chunk-3: orchestration + CLI driver.**
  `gcp.Setup(ctx, client, picker)` runs the end-to-end M3 flow:
  list projects → ask picker → maybe create + WaitOperation →
  enable RequiredServices → GetBillingInfo (non-fatal on
  permission error) → ask picker for region → return Result.
  Project ID generated as `charon-gemini-<8 hex>` when picker
  doesn't pre-supply one. `Picker` interface keeps the
  orchestrator UI-agnostic.

  CLI: `charon gcp setup <account>`. Wires a stdin Picker into the
  orchestrator and persists the Result onto the existing
  `google:<account>` credential as `cred.GCP`. OAuth fields are
  preserved (verified by test). Prereq checks: account must
  exist and have cloud-platform granted.

  Test split: orchestrator tested via httptest+stub picker;
  CLI orchestration via httptest+memory vault+stub picker;
  stdin picker tested via in-memory readers (covers existing
  pick by number, new-project with name, default region,
  numeric region, free-form region, out-of-range rejection).
