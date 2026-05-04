# Providers

Charon supports three credential lifecycle models. Pick the one that
matches the provider you're integrating.

| Model | Provider auth shape | Charon manages | Examples today |
|---|---|---|---|
| **OAuth** | Bearer token via OAuth refresh-token dance | Mint, refresh, scope grants, revoke | Google (Gmail / Drive / Vertex / AI Studio) |
| **Admin-key** | Provider-issued admin key, charon mints per-account API keys via the provider's admin API | Full lifecycle: mint, list, revoke | OpenAI |
| **Catalog (paste-and-revoke)** | Static API key the user pastes; auth shape (bearer / custom header / query param) declared in YAML | Storage and routing; best-effort upstream revoke when the provider exposes a deactivate endpoint | Anthropic (seeded); long-tail expected to grow |

OAuth and Admin-key are first-class Go implementations because they
need provider-specific code (OAuth scope semantics, admin-API mint
flows). The **catalog** is data-driven: adding a new provider is a
one-file YAML PR, not a code change.

This document covers the catalog. For OAuth see
[`docs/agent-protocol.md`](agent-protocol.md). For admin-key see the
issue / atlas notes referenced from there.

---

## Catalog overview

The catalog lives at
[`internal/providers/catalog/catalog.yaml`](../internal/providers/catalog/catalog.yaml).
It's embedded into the binary at compile time via `go:embed`, so the
catalog ships with each charon release; users don't fetch a remote
manifest. (Catalog updates require a charon upgrade. See "Open
questions" in `workshop/issues/000015-provider-catalog.md` for the
remote-refresh path that's deferred.)

Each entry declares:

- **Identity** — `id`, `name`, `signup_url`, `key_url`, `console_url`.
- **Routing** — `hostname_patterns`: exact hostnames the proxy routes
  for this provider.
- **Auth shape** — `auth.style` (one of `bearer`, `header`, `query`)
  plus optional `header`, `prefix`, `param`, and `extra_headers`.
- **Revoke endpoint** (optional) — `revoke.method` + `revoke.url` for
  upstream deactivation. Two shapes supported:
  - **Direct**: `url` may include `{key_id}` filled from the pasted
    key itself (e.g. providers where the key string IS the id).
  - **List-then-revoke**: `revoke.list_endpoint` discovers the
    provider-internal id by matching the pasted key's
    `partial_key_hint` suffix, then substitutes it into `revoke.url`.
- **Verify endpoint** (optional) — `verify_url`: charon GETs this
  with the pasted key applied per `auth` after a successful paste,
  to give the user immediate feedback on whether the key works.
- **Notes** — free-form context for the catalog-author (rendered in
  the TUI; visible to humans).

## How routing works

When the proxy sees an HTTPS request whose hostname matches a catalog
entry's `hostname_patterns`, it:

1. Reads `X-Charon-Account: <name>` from the request.
2. Looks up the credential at `(catalog-entry-id, account)` in the
   vault.
3. Attaches the credential to the outbound request per the entry's
   `auth.style`:
   - `bearer` → `Authorization: <prefix><key>` (default prefix
     `Bearer `)
   - `header` → `<header>: <prefix><key>` (e.g. `x-api-key: <key>`)
   - `query` → appends `?<param>=<key>` (e.g. `?key=<key>` for AI
     Studio-style URL-param auth)
4. Adds any static `extra_headers` (e.g. Anthropic's
   `anthropic-version: 2023-06-01`).
5. Forwards. The agent never sees the key.

Compiled-provider routing (Google OAuth, OpenAI admin-key) takes
precedence on hostname overlap — a catalog entry whose
`hostname_patterns` collide with a compiled provider is rejected at
boot, so a careless catalog PR fails fast instead of silently
shadowing.

## Verify-on-paste

Entries with `verify_url` are probed when the user pastes a key:

- **2xx** — TUI confirms with "verified".
- **401 / 403** — TUI rejects the key and asks the user to retype.
  Provider explicitly said "this key is bad."
- **5xx / network** — TUI stores the key with a
  "verify inconclusive: …" status note. We can't get a verdict
  (the verify endpoint is having problems, not the key), and
  blocking would trap users on a transient outage.

Entries without `verify_url` skip verification entirely.

Use `/v1/models` (Anthropic), `/v1/me` (Slack-shaped APIs), or any
GET that hits the same auth surface as production traffic and is
free + fast. Don't pick something that counts against quota.

## Revoke posture

Catalog credentials are **handles** — charon's only way to touch
the upstream key. When the user invokes revoke:

- Provider has a `revoke` schema and the call succeeds (2xx) →
  upstream deactivated, local credential removed.
- Provider has a `revoke` schema and the call fails (401 because
  the pasted key isn't admin-scoped, transient outage, etc.) →
  TUI shows the upstream error and `[esc/n/enter]` cancels-and-
  preserves so the user can retry. `[d]` is an explicit force-
  delete-locally affordance for the cases where retry will never
  succeed (key already revoked at provider, provider deprecated).
- Provider has no `revoke` schema → local-delete with a
  `console_url` pointer telling the user where to clean up
  manually.

The default-preserve posture is intentional: throwing the local
credential away on transient failure forces the user to re-paste
to retry, which is hostile. See the design rationale in
`workshop/issues/000015-provider-catalog.md`'s `## Notes`.

## Adding a new provider

Open a PR with one new entry in
[`internal/providers/catalog/catalog.yaml`](../internal/providers/catalog/catalog.yaml).
Required fields: `id`, `name`, `signup_url`, `key_url`,
`hostname_patterns`, `auth`. Optional: `revoke`, `console_url`,
`verify_url`, `notes`. Validation runs at boot (the loader
refuses to start charon if the catalog is malformed) and
includes:

- IDs unique, lowercase, `[a-z0-9_-]+`
- Hostnames non-empty and well-formed; not colliding with
  compiled providers or other catalog entries
- `auth.style ∈ {bearer, header, query}` with the appropriate
  required sub-field
- All URLs parse as `https://`
- `revoke.method ∈ {POST, DELETE}` and `revoke.url` is `https://`
- `{key_id}` placeholder in `revoke.url` requires
  `revoke.list_endpoint` to be set (otherwise nothing to fill in)

Where to find each piece for a typical provider:

| Field | Where to look |
|---|---|
| `signup_url` | Top-level dashboard URL |
| `key_url` | "API keys" / "Credentials" page |
| `console_url` | Same as `key_url` typically — shown on local-delete fallback |
| `hostname_patterns` | Provider's API base URL host |
| `auth.style` | Provider's API docs — almost always bearer for OpenAI-compatible inference, custom header for Anthropic-style, query for legacy URL-param APIs |
| `verify_url` | Cheapest authenticated GET (`/v1/models`, `/v1/me`, etc.) |
| `revoke` | Provider's admin API docs — many don't expose programmatic key revoke; leave `revoke` empty if not available |

Test the entry before opening a PR:

```bash
go test ./internal/providers/catalog/   # validates YAML
make dev                                  # run TUI against dev vault
./bin/charon auth                         # navigate to + add provider
```

The `+ add provider` flow opens the catalog picker showing the
new entry. Pick it, paste a real key, exercise verify (if
declared), and try a request through `charon run -- curl …`
against one of `hostname_patterns`.

## Threat-model notes

- The catalog is **curated and embedded** at compile time. Charon
  doesn't fetch arbitrary URLs at runtime — the SSRF surface is
  bounded by what's reviewed in the YAML. A user-extensible
  catalog (`~/.config/charon/providers.yaml` merge) is deferred
  for that reason.
- Pasted catalog keys are **not lifecycle-managed**. Charon
  doesn't rotate them; revoke is best-effort. The user remains
  responsible for the upstream credential's lifecycle when
  charon's revoke can't reach the provider.
- Verify probes hit the provider's URL with the just-pasted key.
  This leaks the key to the provider (which already has it — they
  issued it), but no third party.

See [`docs/threat-model.md`](threat-model.md) for the broader
threat model.
