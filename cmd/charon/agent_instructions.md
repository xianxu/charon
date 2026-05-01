# Charon — Agent Instructions

Charon is a credential proxy for AI agents. It injects OAuth tokens and
API keys into HTTPS requests at the proxy layer so you (the agent)
never see secrets in plaintext. This document tells you how to use it.

These instructions are emitted by `charon instructions` from the
charon binary itself — they always match the version installed on
this machine.

---

## Bootstrap (one-time at startup)

1. Confirm the proxy is running:
   ```
   charon status
   ```
   Should print `Proxy: ok on 127.0.0.1:8230`.

2. Discover what's available:
   ```
   charon manifest
   ```
   Returns one JSON document with everything you need: proxy address,
   CA cert URL, and per-account credentials with scopes / GCP metadata.

3. If you spawn child processes (curl, python, node, …), wrap them so
   `HTTPS_PROXY` and CA-trust env vars are set:
   ```
   charon run -- python my_agent.py
   ```
   This injects `HTTPS_PROXY`, `https_proxy`, `SSL_CERT_FILE`,
   `REQUESTS_CA_BUNDLE`, `CURL_CA_BUNDLE`, `NODE_EXTRA_CA_CERTS`,
   and `GRPC_DEFAULT_SSL_ROOTS_FILE_PATH`. If you're running directly
   inside an already-wrapped shell, those are already set.

---

## Manifest shape

```json
{
  "proxy": {
    "addr":       "127.0.0.1:8230",
    "url":        "http://127.0.0.1:8230",
    "ca_pem_url": "http://127.0.0.1:8230/ca.pem"
  },
  "permissions": {
    "google": {
      "user@gmail.com": {
        "scopes": ["openid", "https://www.googleapis.com/auth/gmail.readonly", ...],
        "gcp": {
          "project_id":      "my-charon-project",
          "project_name":    "My Charon Project",
          "vertex_region":   "us-central1",
          "billing_enabled": true
        },
        "aistudio": {
          "uid":          "abc-uid",
          "display_name": "charon-aistudio",
          "project_id":   "my-charon-project"
        }
      }
    },
    "openai":    { "<project>": { "scopes": [] } },
    "anthropic": { "<workspace>": { "scopes": [] } }
  }
}
```

- `proxy.url` is what to set as `HTTPS_PROXY` (already set if you ran
  under `charon run`).
- `proxy.ca_pem_url` is where your HTTPS client should fetch the CA
  cert it must trust.
- Each `permissions[provider][account]` is `{scopes, gcp?, aistudio?}`.
  - `scopes`: full URLs of granted OAuth scopes (Google) or empty list
    (admin-key providers).
  - `gcp`: present only on Google accounts that have run charon's
    project setup (`cloud-platform` granted + project picked).
    Carries `project_id` (used in Vertex URLs) and `vertex_region`
    (default region; you can override per-request).
  - `aistudio`: present when an AI Studio key exists for the account.
    Metadata only — `uid`, `display_name`, `project_id`. The actual
    key is **not** surfaced; charon's proxy attaches it to outbound
    AI Studio requests automatically (so agents never see it).

The manifest is the **single source of truth at runtime**. Read it
once at startup, cache, and re-read after any user-visible change
(scope grant, project setup, account add).

---

## Per-request signaling

### `X-Charon-Account` (always set when multiple accounts exist)

Picks which credential charon uses for this request:

```
X-Charon-Account: user@gmail.com
```

For single-account providers it's optional (auto-selected). For
multi-account, charon returns 407 if missing.

### `X-Charon-Scope` (optional, encouraged)

Declare what scopes the request needs. Charon validates up-front and
returns 407 with a structured error before forwarding:

```
X-Charon-Scope: gmail.readonly,calendar.readonly
```

Both short names (e.g. `gmail.readonly`) and full URLs work. Skip
this header if you don't want strict pre-validation; you'll just see
the upstream provider's 403 instead of charon's 407.

---

## Per-provider conventions

### Google Workspace APIs (Gmail, Drive, Sheets, Calendar, …)

Use the standard Google API hostnames. Charon attaches the OAuth
bearer token transparently:

```
GET https://gmail.googleapis.com/gmail/v1/users/me/profile

Headers:
  X-Charon-Account: user@gmail.com
  X-Charon-Scope:   gmail.readonly
```

### Vertex AI (Gemini via Google Cloud)

Build the URL from `manifest.permissions.google[account].gcp`:

```
POST https://{vertex_region}-aiplatform.googleapis.com
       /v1/projects/{project_id}
       /locations/{vertex_region}
       /publishers/google/models/gemini-2.0-flash:generateContent

Headers:
  X-Charon-Account: user@gmail.com
```

- Required scope: `cloud-platform` (`https://www.googleapis.com/auth/cloud-platform`).
  If missing, charon returns 407 — tell the user to run `charon auth`
  and grant it (which auto-launches project setup).
- The stored `vertex_region` is a default; you can use any region
  by putting it in the URL — charon doesn't constrain.
- The stored `project_id` is also a default; you can call into a
  different project the user has access to by changing the URL,
  same OAuth token works.

### AI Studio (Gemini, free-tier, key-based)

**Currently pending implementation (M5 in issue #14).** When done:

```
POST https://generativelanguage.googleapis.com/v1beta/models/gemini-2.0-flash:generateContent

Headers:
  X-Charon-Account: user@gmail.com
```

Charon will auto-attach the minted `?key=...` URL parameter for
the account's AI Studio API key. No project info required from you.

### OpenAI / Anthropic

Standard API hosts. Charon attaches the per-project admin-minted
key as `Authorization: Bearer`:

```
POST https://api.openai.com/v1/chat/completions

Headers:
  X-Charon-Account: my-project
```

`X-Charon-Account` here is the charon-side project label (visible in
manifest), not OpenAI's internal project ID.

---

## Error handling

- **407 Proxy Authentication Required** — charon's structured error
  response. JSON body, common shapes:
  ```json
  {"error":"missing_scope","scope":"gmail.readonly","account":"user@gmail.com"}
  {"error":"missing_account","provider":"google"}
  ```
  Tell the user what's missing and suggest running `charon auth`.

- **403 BILLING_DISABLED** (from Vertex) — the Google Cloud project
  has no billing account linked. AI Studio (free tier) still works;
  Vertex doesn't until billing is set up. Tell the user to run
  `charon auth` and follow the link from cloud-platform setup, or
  go directly to
  `https://console.cloud.google.com/billing/linkedaccount?project={project_id}`.

- **Other 4xx / 5xx** — passed through from upstream. Charon does
  not transform error bodies.

---

## Quick reference

| What you want                       | How                                              |
|-------------------------------------|--------------------------------------------------|
| Get proxy address + CA + accounts   | `charon manifest`                                |
| Get scope catalog (what's possible) | `charon scopes`                                  |
| Wrap a child process                | `charon run -- <cmd>`                            |
| Pick an account                     | `X-Charon-Account: <account>` header             |
| Declare required scopes (optional)  | `X-Charon-Scope: <short>,<short>` header         |
| Vertex URL                          | Build from `manifest.gcp.project_id` + region    |
| AI Studio URL                       | `generativelanguage.googleapis.com` (M5 pending) |

---

## What charon does *not* do

- It does not see, log, or transform request/response bodies.
- It does not retry. Failures are propagated.
- It does not cache responses. Every call hits upstream.
- It does not orchestrate multi-step flows. One request → one
  upstream call → one response.

If a credential is missing or expired, charon refreshes it (OAuth
refresh tokens) or returns 407 (admin-key providers). Either way the
agent's request is held until charon is done — no fan-out, no
silent dropping.
