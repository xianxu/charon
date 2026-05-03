# Charon Agent Protocol

This is the **canonical** spec for how an agent (or any HTTP client) talks
to charon. Charon is a forward HTTP proxy that injects OAuth credentials
into outbound requests on your behalf — your agent never sees tokens, but
needs to declare what it's about to do so charon can route, authorize,
and surface useful errors.

If you're writing a tool that calls a third-party API through charon, this
is the contract. It is provider-agnostic; per-provider notes (Google,
Dropbox, etc.) are at the bottom.

## Setup

Run your tool through `charon run`:

```bash
charon run -- your-tool ...
```

`charon run` sets `HTTPS_PROXY`, `HTTP_PROXY`, and the trust roots
(`SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE`, etc.) so your HTTPS calls go
through charon transparently. Your agent code uses real upstream URLs
(`https://gmail.googleapis.com/...`); charon handles the rest.

## Provider types

Charon supports two credential lifecycle models, and the headers behave
slightly differently for each:

| Provider type | Examples | `X-Charon-Account` semantics | `X-Charon-Scope` semantics |
|---|---|---|---|
| **OAuth** | Google (Gmail, Drive, Calendar, …) | account email | scope check applies — 407 on missing |
| **Admin-key** (#13) | OpenAI | charon-side **key alias** (the `name` field from `charon auth`) | **silently ignored** — no scope concept |
| **Catalog** (#15, future) | Anthropic, Groq, Mistral, … | charon-side key alias | silently ignored |

`X-Charon-Scope` is only consumed for OAuth-provider routes. On
admin-key and catalog routes charon strips the header before
forwarding (same as OAuth) but doesn't check it against the
credential — admin-key keys aren't bound by scope grants the way
OAuth tokens are. Setting `X-Charon-Scope` on an OpenAI request is
harmless; the header just gets dropped.

## The two headers

When making HTTP requests through charon, set:

### `X-Charon-Account: <name>`

**Required when more than one credential exists for the provider.**
Selects which credential to inject. The value is:

- **OAuth providers**: the account email (e.g. `user@gmail.com`)
- **Admin-key providers**: the charon-side key alias you set in
  `charon auth` (e.g. `image-gen`)

With a single credential in the vault charon auto-resolves and the
header is optional, but always setting it is safer.

### `X-Charon-Scope: <comma-separated short names or full URLs>`

**Strongly recommended on OAuth routes.** Declares which OAuth scopes
your call needs. Charon checks these against what's actually granted
for the account before forwarding. If anything's missing, charon
short-circuits with an **HTTP 407** that tells you exactly what's
missing and the command to fix it. Without this header, charon can't
preempt — you'll get a provider-specific 403 instead, with worse
error messages.

On **admin-key** and **catalog** routes charon silently ignores the
header (admin-key credentials have no scope semantics — the key
either has access at the provider's side or it doesn't). Charon
still strips the header before forwarding so the upstream provider
never sees it.

Both short names and full URLs are accepted:

```
X-Charon-Scope: gmail.readonly,calendar.readonly
X-Charon-Scope: https://www.googleapis.com/auth/gmail.readonly
```

Charon strips both headers before forwarding upstream, so the API
provider never sees them.

## The 407 dance

When `X-Charon-Scope` declares a scope the credential lacks, charon
returns:

```http
HTTP/1.1 407 Proxy Authentication Required
Content-Type: application/json

{
  "error": "scope_missing",
  "missing": ["gmail.readonly"],
  "account": "user@gmail.com",
  "provider": "google",
  "fix": "charon auth google grant user@gmail.com gmail.readonly"
}
```

### How to react

1. **Don't retry blindly** — the missing scope won't appear by itself.
2. **Surface the message to the user.** The `fix` field has the literal
   command they should run. Or, if they prefer interactive: `charon auth`
   opens the TUI and the missing scope shows with a `!` badge so they
   can grant it without copy-pasting.
3. **After the user grants**, retry the original request — same headers,
   should succeed now.

In practice, an agent might:

```python
resp = httpx.get(url, headers={
    "X-Charon-Account": account,
    "X-Charon-Scope": "gmail.readonly",
})
if resp.status_code == 407:
    err = resp.json()
    print(f"Missing scope. Run: {err['fix']}")
    print("Or: charon auth")
    sys.exit(1)
```

## Discovering required scopes

Knowing *what* to put in `X-Charon-Scope` is the harder problem. A few
strategies:

### Programmatic: query charon's manifest and scope catalog

```bash
charon manifest    # what charon offers right now: proxy addr + granted scopes
charon scopes      # what scopes EXIST per provider (catalog of grantables)
```

`charon manifest` is the single call an agent needs to start using
charon. Shape:

```json
{
  "proxy": {
    "addr":       "127.0.0.1:8230",
    "url":        "http://127.0.0.1:8230",
    "ca_pem_url": "http://127.0.0.1:8230/ca.pem"
  },
  "permissions": {
    "google": {"a@gmail.com": ["scopes..."], "b@gmail.com": ["scopes..."]},
    "dropbox": {"...": [...]}
  }
}
```

The `proxy` section tells the agent where to send traffic (use `url`
as `HTTPS_PROXY`) and where to fetch the trust root (`GET ca_pem_url`).
The `permissions` section tells it which accounts exist and what each
one currently has granted.

`charon scopes` outputs a JSON object keyed by provider name. Each
value is the provider's scope catalog with `short` (e.g.
`gmail.readonly`), `full` (the URL the provider uses), `description`,
and `required` (always-granted by charon). Use it at code-write time
or runtime to map "I want to read Gmail" to a concrete scope short
name.

```bash
$ charon scopes | jq 'keys'
["google"]

$ charon scopes | jq '.google[] | select(.short | startswith("gmail"))'
{"full":"https://www.googleapis.com/auth/gmail.readonly","short":"gmail.readonly","description":"Read Gmail messages","required":false}
{"full":"https://www.googleapis.com/auth/gmail.send","short":"gmail.send","description":"Send Gmail messages","required":false}
{"full":"https://www.googleapis.com/auth/gmail.modify","short":"gmail.modify","description":"Read, send, and manage Gmail","required":false}
```

This is the **canonical machine-readable source** — the table later in
this document is a snapshot for human convenience but `charon scopes`
won't go stale when the catalog grows.

### Best: read the API's scope reference

Every OAuth-protected API documents the scopes its endpoints require.
Use the narrowest scope that covers what you're doing.

### Programmatic: per-provider Discovery (Google)

Google's [Discovery API](https://developers.google.com/discovery/v1/reference/apis)
exposes per-method `auth.oauth2.scopes` arrays for every API. You can
fetch the discovery doc once at agent startup and look up scopes by
HTTP method + path. (See "Google" section below for endpoint mapping.)

### Heuristic: read-only first

For most providers, scopes follow a pattern: `<api>.readonly` covers
GET-style operations, the unscoped `<api>` covers writes. When in doubt,
declare the read-only variant — if your call is actually a write, charon
will tell you via 407.

### Pragmatic: declare what you intend, let charon check

If you intend to *search* email, declare `gmail.readonly`. If you intend
to *send* email, declare `gmail.send`. The header is documentation of
intent, and charon enforces.

## Provider-specific notes

### Google

Common scope short names (charon's catalog covers these; full list at
[Google's OAuth scopes reference](https://developers.google.com/identity/protocols/oauth2/scopes)):

| Operation | Scope |
|---|---|
| Read Gmail | `gmail.readonly` |
| Send mail | `gmail.send` |
| Read+send+manage Gmail | `gmail.modify` |
| Read calendar events | `calendar.readonly` |
| Read+write calendar | `calendar` |
| Read Drive | `drive.readonly` |
| Read+write Drive | `drive` |
| Read Sheets | `spreadsheets.readonly` |
| Read+write Sheets | `spreadsheets` |
| Read Docs | `docs.readonly` |
| Read+write Docs | `docs` |
| Read Slides | `slides.readonly` |
| Read+write Slides | `slides` |
| Read Tasks | `tasks.readonly` |
| Read+write Tasks | `tasks` |
| Read Contacts | `contacts.readonly` |
| Read YouTube | `youtube.readonly` |

Required and always granted:
- `openid` and `email` (so charon can identify which account
  authenticated; charon force-includes these in every OAuth flow)

For scopes not in this list, the `a` key in the TUI lets the user grant
arbitrary URLs. Pass the full URL form in `X-Charon-Scope`.

#### Sample: Gmail search

```http
GET /gmail/v1/users/me/threads?q=from%3Aalice HTTP/1.1
Host: gmail.googleapis.com
X-Charon-Account: user@gmail.com
X-Charon-Scope: gmail.readonly
```

#### Sample: Sending mail

```http
POST /gmail/v1/users/me/messages/send HTTP/1.1
Host: gmail.googleapis.com
X-Charon-Account: user@gmail.com
X-Charon-Scope: gmail.send
```

#### Sample: 407 on missing scope

If the user hasn't granted `gmail.send` yet:

```http
HTTP/1.1 407 Proxy Authentication Required
Content-Type: application/json

{
  "error": "scope_missing",
  "missing": ["gmail.send"],
  "account": "user@gmail.com",
  "provider": "google",
  "fix": "charon auth google grant user@gmail.com gmail.send"
}
```

#### Note on rewriting

Google rewrites the OIDC short scope `email` to its full URL
`https://www.googleapis.com/auth/userinfo.email` in token responses. The
catalog uses the full URL for round-trip consistency. You can use either
form in `X-Charon-Scope`.

### Google AI providers — Vertex AI and AI Studio (Gemini)

Two distinct paths to the same Gemini models, both authenticated via
the user's existing Google OAuth credential. Both require the
`cloud-platform` scope and a charon-managed GCP project (set up via
`charon auth` → enter on the cloud-platform row → walk through
project picker / API enable / billing check / region pick).

The setup populates two manifest blocks per Google account:

```json
"google": {
  "user@gmail.com": {
    "scopes":    [..., "https://www.googleapis.com/auth/cloud-platform"],
    "vertex":    {"project_id": "...", "region": "us-central1"},
    "ai-studio": {"project_id": "..."}
  }
}
```

Read these blocks at agent startup. Their **presence** signals the
path is available; **absence** means the user hasn't completed
project setup — surface "run `charon auth`" to them and stop.

#### Vertex AI

URL pattern includes both `region` and `project_id`. Build it from
the manifest's `vertex` block:

```http
POST /v1/projects/{project_id}/locations/{region}/publishers/google/models/gemini-flash-latest:generateContent HTTP/1.1
Host: {region}-aiplatform.googleapis.com
X-Charon-Account: user@gmail.com
Content-Type: application/json

{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}
```

Charon attaches `Authorization: Bearer <oauth_access_token>` to the
upstream request. Region and project are agent-controlled — you can
override per-request by changing the URL; charon doesn't constrain.

#### AI Studio

Same prompt models, different endpoint, different auth. URL is just
the model path; charon attaches `?key=<minted_api_key>` to the URL
transparently. Agent never sees the key.

```http
POST /v1beta/models/gemini-flash-latest:generateContent HTTP/1.1
Host: generativelanguage.googleapis.com
X-Charon-Account: user@gmail.com
Content-Type: application/json

{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}
```

The `ai-studio.project_id` from the manifest is **informational** —
the agent doesn't put it in the URL — but it's the project consuming
quota/billing for the call. Surface it to the user when calls return
`RESOURCE_EXHAUSTED` or `BILLING_DISABLED` so they know which project
to act on.

#### Common error patterns

- **`403 BILLING_DISABLED` (Vertex)** — project has no billing
  account linked. Charon's setup flow blocks at this state and
  surfaces a console link; if the agent receives it at runtime the
  user must have skipped the block (`[c] continue without billing`)
  or billing got removed after setup. Tell them to link billing at
  `https://console.cloud.google.com/billing/linkedaccount?project=<vertex.project_id>`.

- **`429 RESOURCE_EXHAUSTED` with `limit: 0` on free-tier metrics
  (AI Studio)** — charon-created projects don't get free-tier quota
  by default; the project needs billing linked to use paid tier.
  Same fix URL as above, with `<ai-studio.project_id>`.

- **No `vertex` / `ai-studio` block in manifest** — user hasn't
  completed setup. Tell them to run `charon auth` and toggle on
  `cloud-platform` (the row's enter key launches setup).

#### Scope semantics

`cloud-platform` is the only scope these paths need. Charon validates
it against `X-Charon-Scope` if you set the header; otherwise the
upstream returns 403 if missing. The scope check applies to the
Vertex path; AI Studio routes have `HasScopes:false` so the header is
ignored on those (the API key carries the authorization).

### OpenAI

Admin-key provider (#13). User pastes a one-time admin key in
`charon auth`; charon mints per-account keys via service-account
creation against OpenAI's Admin API. The agent uses the local key
alias (the `name` field, e.g. `image-gen`) in `X-Charon-Account`;
charon swaps in the actual `sk-…` value before forwarding to
`api.openai.com`. `X-Charon-Scope` is ignored.

Routed host: **`api.openai.com`** (data plane). The Admin API
(`/v1/organization/…`) shares the host but is only used by the TUI
during mint/revoke flows; agents talk to the data-plane endpoints.

#### Sample: Image generation

```http
POST /v1/images/generations HTTP/1.1
Host: api.openai.com
X-Charon-Account: image-gen
Content-Type: application/json

{"model":"gpt-image-1","prompt":"a smiling capybara","n":1,"size":"1024x1024"}
```

Charon forwards the request with `Authorization: Bearer sk-…`
filled in from the keychain. The agent never sees the key.

#### Sample: 407 on unknown account

```http
HTTP/1.1 407 Proxy Authentication Required

charon: credential required
```

Returned when `X-Charon-Account` names a key that doesn't exist in
charon's vault, or when no admin key is configured yet for the
provider.

### Other providers (future)

When charon adds catalog (Tier 3) providers via #15 — Anthropic,
Groq, Mistral, etc. — this document gains a section per provider.
Catalog providers use the same `X-Charon-Account` semantics as
OpenAI (key-alias lookup, no scope semantics). The cross-cutting
protocol (headers, 407, fix command) stays the same.

## Backward fallback (no `X-Charon-Scope`)

If your agent doesn't set `X-Charon-Scope` (legacy code, gradual
adoption), charon forwards the request to upstream and you'll get
whatever the API itself returns — typically HTTP 403 with a generic
"insufficient permissions" message. **You won't see a 407 from charon.**
The badge UX in the TUI also won't surface the missing scope (because
charon never saw the requirement).

There's an issue tracking the gap (#000008): synthesize denials from
upstream 403 responses using `WWW-Authenticate` headers and per-provider
URL→scope tables. Until that lands, the explicit `X-Charon-Scope`
contract is the only reliable way to get good error messages.

## Quick reference

```
On every request through charon:
  X-Charon-Account: <email>
  X-Charon-Scope: <scope1,scope2,...>

If response is 407:
  body.fix has the command to run (or run `charon auth` for the TUI)
  retry after user grants

If response is 403 from the upstream API:
  charon couldn't preempt because X-Charon-Scope wasn't set
  surface the upstream error and consider adding the header
```
