# Charon

A credential proxy for AI agents. Single Go binary that acts as a transparent
HTTPS forward proxy and injects OAuth tokens into requests on the agent's
behalf — so the agent never sees credentials but can still call OAuth-protected
APIs (Gmail, Calendar, Drive, etc.) using real upstream URLs and no code
changes, except setting two headers, one mandatory and one advisory.

> Charon is one third of a layered architecture: 
>   **[charon](https://github.com/xianxu/charon)**: (this) outbound capability to access cloud services 
>   **[nous](https://github.com/xianxu/nous)**: task capability, and agentic infrastructure, it use the main user of `charon`
>   **brain**: private state, personal. 
> See the [trio overview](https://github.com/xianxu/nous#the-trio-charon-nous-brain) in nous's README for how the three fit together.

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

First, [install charon](#installation) (one-time: bootstraps a self-signed
code-signing identity and produces a signed `~/.local/bin/charon`). Then:

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

- **Tokens at rest**: macOS Keychain generic password under service name
  `charon` (or `charon-dev` for unsigned dev binaries; see below).
  Account key is `<provider>:<email>`. Linux secret service support is
  tracked in #000002.
- **Keychain ACL** ([#000003](workshop/issues/000003-code-signing-keychain-acl.md)):
  the signed `make install` binary writes entries with a `SecAccess` ACL
  pinned to its codesign designated requirement. Any other reader —
  including `security find-generic-password`, an AI agent that learned
  about Keychain APIs, or another developer logged in as the same user —
  triggers the macOS "Allow / Deny" dialog. **This is the actual
  security boundary**: an agent that exfiltrates `security`-CLI knowledge
  still cannot read charon's tokens silently.
- **CA cert**: same keychain, same ACL treatment as tokens. The CA
  private key is at least as sensitive as OAuth tokens — owning it forges
  HTTPS for any host.
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

## Installation

For day-to-day use, `make install` builds, code-signs, and installs to
`~/.local/bin/charon`. Code signing is what makes the keychain ACL meaningful:
an unsigned binary's entries don't get the ACL, and reads from outside the
signed binary won't trigger the Allow/Deny dialog.

First-time setup (one shell session):

```bash
git clone https://github.com/xianxu/charon
cd charon
make signing-identity   # creates a self-signed code-signing cert (10y)
                        # in your login keychain. idempotent; one-time.
make install            # build → sign → copy to ~/.local/bin/charon
```

On the **first run** of any `charon` command after this, macOS will pop
a Keychain Access dialog asking whether `codesign` may use the new
private key. Click **Always Allow**. Subsequent installs are silent.

To verify the installed binary is properly signed:

```bash
codesign -dv ~/.local/bin/charon
# expect: Authority=Charon Self-Signed
#         Identifier=com.charon.cli
```

### Dev builds (unsigned, fast iteration)

```bash
make build              # produces ./bin/charon, unsigned, uses
                        # service "charon-dev" so it doesn't collide
                        # with the signed install's keychain entries
go test ./...           # full unit suite
go test -tags integration ./internal/vault/keychain/   # hits real Keychain
```

`make build` is the fast inner loop; nothing is signed and the binary
writes to a separate `charon-dev` keychain namespace. The signed install
at `~/.local/bin/charon` and your dev rebuilds never overwrite each
other's state.

### Build prerequisites

- Go 1.26+
- macOS with Xcode Command Line Tools (for cgo + the Security framework)
- Linux: cross-compile works with `CGO_ENABLED=0`, but the keychain
  backend currently has no Linux implementation; see #000002.

### Apple Developer ID upgrade (later)

The self-signed identity is sufficient for personal use. Migrating to an
Apple Developer ID is a Makefile-variable swap (`SIGN_IDENTITY=Developer
ID Application: ...`) and a one-time `charon migrate-acl` to re-write
existing entries under the team-anchored predicate; see
[#000003](workshop/issues/000003-code-signing-keychain-acl.md).

## Documentation

- [`atlas/charon.md`](atlas/charon.md) — codebase map and design decisions
- [`docs/agent-protocol.md`](docs/agent-protocol.md) — agent-side spec
- [`workshop/issues/`](workshop/issues/) — active work
- [`workshop/history/`](workshop/history/) — completed work
- [`AGENTS.md`](AGENTS.md) — workflow conventions for AI agents working on
  this codebase
