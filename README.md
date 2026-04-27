# Charon

A credential proxy for AI agents. Single Go binary that acts as a transparent
HTTPS forward proxy and injects OAuth tokens into requests on the agent's
behalf — so the agent never sees credentials but can still call OAuth-protected
APIs (Gmail, Calendar, Drive, etc.) using real upstream URLs and no code
changes.

> Charon is one third of a layered architecture: **charon** (outbound
> capability), **[nous](https://github.com/xianxu/nous)** (task capability,
> public), **brain** (private state, personal). See the
> [trio overview](https://github.com/xianxu/nous#the-trio-charon-nous-brain)
> in nous's README for how the three fit together.

## What it does

```
your-agent ──HTTPS──> charon proxy ──HTTPS──> Google APIs
                       │
                       ├─ injects bearer token from keychain
                       ├─ refreshes expired tokens automatically
                       ├─ enforces declared scope set (HTTP 407 on mismatch)
                       └─ logs every request for audit
```

The agent uses `https://gmail.googleapis.com/...` as if calling Google
directly. Charon intercepts via TLS MITM, looks up the right account's
token in the OS keychain, attaches `Authorization: Bearer <token>`, and
forwards to Google. The token never appears in the agent's memory or logs.

## Quick start

```bash
# 1. Authenticate (opens browser, stores tokens in macOS Keychain).
charon auth                # interactive TUI: pick scopes, grant, revoke

# 2. Run any tool through the proxy. Charon sets HTTPS_PROXY and CA trust.
charon run -- python my_agent.py
charon run -- curl https://gmail.googleapis.com/gmail/v1/users/me/profile
```

That's it. The agent makes plain HTTPS calls; charon does the rest.

## Why it exists

If an AI agent has any access to your shell environment, scratch files, or
process memory, it has access to whatever credentials live there. Storing
OAuth tokens in `.env` files or per-tool credential stores keeps making
this attack surface bigger. The fix is to **never give credentials to the
agent at all** — keep them in a sidecar process the agent talks through.

Charon is that sidecar:

- Tokens live in the OS keychain (`security` CLI on macOS).
- Charon runs as a small local proxy on `127.0.0.1:8230`.
- Agent processes get `HTTPS_PROXY=http://127.0.0.1:8230` and a CA bundle
  via env vars set by `charon run`.
- The agent makes HTTPS requests to real URLs; charon attaches the right
  bearer token before forwarding.

The agent has no path to the underlying credential. If it's compromised, the
blast radius is bounded by the scopes the user granted — which are visible
and revocable in the TUI.

## Scope management TUI (`charon auth`)

Interactive UI for picking which OAuth scopes each account grants:

- Toggle scopes on/off with space; Enter to apply (browser OAuth dance).
- Reductive flow: removing a scope re-authorizes with `prompt=consent` and
  `include_granted_scopes=false` so the new token covers exactly what's
  requested, not the union of past grants.
- Search bar (substring filter), session markers (`+`/`-` for changes
  applied this session), color tinting for state.
- `Ctrl+R` revokes an account entirely (calls Google's revoke endpoint and
  removes the credential from the keychain).

See [`docs/agent-protocol.md`](docs/agent-protocol.md) for the full UI and
agent contract.

## Agent protocol

Tools that route through charon should set two headers:

```
X-Charon-Account: user@gmail.com
X-Charon-Scope: gmail.readonly
```

If the declared scopes are granted, charon forwards. If anything's missing,
charon short-circuits with **HTTP 407** and a structured fix command:

```json
{
  "error": "scope_missing",
  "missing": ["gmail.readonly"],
  "account": "user@gmail.com",
  "provider": "google",
  "fix": "charon auth google grant user@gmail.com gmail.readonly"
}
```

Tools surface the `fix` command (or just suggest `charon auth`) to the
user, who grants the scope, then the tool retries.

Full spec: [`docs/agent-protocol.md`](docs/agent-protocol.md).

## Introspection commands for agents

```bash
charon scopes                              # JSON: catalog of known scopes per provider
charon permissions                         # JSON: granted scopes per provider/account
charon permissions google user@gmail.com   # JSON: one account's granted scopes
charon accounts                            # plain list of stored accounts
```

## CLI reference

```
charon serve [-v] [--audit-log path]   # start the proxy (port 8230 by default)
charon run -- <cmd>                     # run a child process with proxy env set
charon auth                             # scope-management TUI
charon accounts                         # list stored credentials
charon scopes                           # catalog (JSON)
charon permissions [provider [account]] # granted scopes (JSON)
charon status                           # health-check the running proxy
charon vault set/delete                 # manual token management
charon service install/uninstall/...    # macOS launchd integration
```

## Security model

- **Tokens at rest**: macOS Keychain (`security` generic password) under
  service name `charon`, account key `<provider>:<email>`. Linux secret
  service support is tracked in #000002.
- **CA cert**: also in keychain, regenerated if expired. Used to MITM
  HTTPS traffic the agent makes. Trust roots are wired into the child via
  `SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE`, `NODE_EXTRA_CA_CERTS`, etc.
- **Process isolation**: charon runs as a separate process from the agent.
  The agent's only path to charon is the local HTTPS proxy port; tokens
  are never in the agent's address space.
- **Scope enforcement**: optional but recommended. Agents declare
  `X-Charon-Scope`; charon checks against granted scopes and returns 407
  preemptively. Without the header, charon forwards blindly and the
  upstream API will return its own 403.

## Project layout

```
cmd/charon/                    CLI entry points (cobra)
internal/proxy/                HTTPS proxy, MITM, scope enforcement, audit
internal/oauth/                Google OAuth: auth, refresh, revoke, scope catalog
internal/tui/                  bubbletea-based scope-management UI
internal/vault/                credential storage (Store interface)
  ├── keychain/                macOS Keychain backend
  └── memory/                  in-memory backend (for tests)
internal/service/              macOS launchd integration
docs/agent-protocol.md         canonical agent-side spec
atlas/charon.md                project map (terminology, design decisions)
workshop/issues/               active work items
workshop/history/              archived completed work
```

## Status

- ✅ M1 — proxy + keychain + manual token + `charon run`
- ✅ M2 — Google OAuth + auto-refresh + keep-alive
- ✅ M3 — wildcard routing + zero-config + integration tests
- ✅ M4 — scope management + auth flow improvements ([#000004](workshop/history/000004-scope-management.md))
- ✅ M5 — scope-management TUI replaces legacy auth subcommands ([#000005](workshop/issues/000005-scope-tui.md))
- 🔜 Multi-provider (Dropbox, Microsoft, …) — [#000006](workshop/issues/000006-multi-provider.md)
- 🔜 Scope catalog with categories + filter syntax — [#000007](workshop/issues/000007-scope-catalog-categories.md)
- 🔜 Synthesize denials from upstream 403s — [#000008](workshop/issues/000008-synthesize-denials-from-403.md)
- 🔜 Linux secret service — [#000002](workshop/issues/000002-linux-secret-service.md)
- 🔜 Code signing + Keychain ACL — [#000003](workshop/issues/000003-code-signing-keychain-acl.md)

## Building from source

```bash
git clone https://github.com/xianxu/charon
cd charon
go build ./cmd/charon          # produces ./charon
# or:
go install ./cmd/charon        # installs to $GOBIN
```

Pure Go, no CGo. Builds hermetically on macOS and Linux. Linux currently
lacks a credential store backend (in-memory only); see #000002.

## Documentation

- [`atlas/charon.md`](atlas/charon.md) — codebase map and design decisions
- [`docs/agent-protocol.md`](docs/agent-protocol.md) — agent-side spec
- [`workshop/issues/`](workshop/issues/) — active work
- [`workshop/history/`](workshop/history/) — completed work
- [`AGENTS.md`](AGENTS.md) — workflow conventions for AI agents working on
  this codebase
