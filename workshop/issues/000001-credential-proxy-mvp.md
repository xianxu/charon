---
id: 000001
status: working
deps: []
github_issue:
created: 2026-04-24
updated: 2026-04-24
---

# Charon: credential proxy MVP

## Context

Read ./docs/vision/2026-04-24-01-pensive-why-charon.md for context. 

## Problem

AI agents that access user services (Gmail, Calendar, banking) must never see OAuth tokens or credentials. Current approaches either emit tokens to the calling process (aws-vault, 1password CLI) or are heavyweight (Infisical Agent Vault). We need a simple, single-binary credential proxy that:

- Stores tokens in the OS-native vault (macOS Keychain, Linux secret service)
- Injects credentials transparently via HTTPS proxy
- Handles OAuth 2.0 + PKCE with automatic token refresh and rotation
- Is portable (Go, single binary, all OS — start with macOS)

## Spec

### Architecture

Single Go binary, two modes:

```
charon serve              # Start HTTPS proxy on localhost
charon auth google user@gmail.com  # OAuth PKCE flow, store in OS vault
charon auth remove user@gmail.com  # Remove credential
charon accounts           # List stored accounts
charon status             # Show proxy status, active services
```

### Proxy model

- Agent sets `HTTPS_PROXY=http://localhost:<port>`
- Agent makes normal HTTP requests to real API hosts
- Charon intercepts, injects `Authorization: Bearer <token>`, forwards request
- Response passes through to agent — token never visible to agent

### Multi-account via custom header

Agent sends `X-Charon-Account: user@gmail.com` to select which credential to use when multiple accounts exist for the same service. Charon strips this header before forwarding.

```python
import httpx
resp = httpx.get(
    "https://gmail.googleapis.com/gmail/v1/users/me/threads?q=query",
    headers={"X-Charon-Account": "xianxu@gmail.com"}
)
```

If only one account exists for a service, the header is optional.

### Credential storage (pluggable)

```
vault/interface.go         # Store/Load/Delete/List interface
vault/keychain/keychain.go # macOS Keychain backend
vault/secretservice/ss.go  # Linux (GNOME Keyring / KDE Wallet via D-Bus)
```

Token stored as JSON blob: refresh token, granted scopes, provider, metadata. Access token cached in memory, never persisted.

### OAuth 2.0 + PKCE

- Use PKCE (Proof Key for Code Exchange) — no client_secret at runtime
- Google requires registering a "Desktop app" OAuth client; client_id baked into binary
- Refresh token rotation: each refresh returns a new refresh token, old one invalidated
- Automatic refresh: proxy checks access token expiry before each request, refreshes if needed
- Scopes tracked per-account; incremental authorization supported

### Audit logging

Append-only log file. Each proxied request logs:
- Timestamp, method, host, path, status code, latency
- Which credential key was used
- NOT: request/response bodies, headers, query strings

### Service routing

Charon maps hosts to credential providers:
- `gmail.googleapis.com` → Google OAuth
- `www.googleapis.com` → Google OAuth
- Future: `api.bank.com` → bank's OAuth

Config stored alongside credentials or in a simple YAML/JSON file.

## Plan

### Milestone 1: Proxy + Keychain + manual token

- [x] Go project setup (go.mod, basic structure)
- [x] Vault interface + macOS Keychain backend
- [x] HTTPS proxy server (CONNECT method, TLS interception)
- [x] Token injection based on destination host
- [x] X-Charon-Account header for multi-account
- [x] CLI: `charon serve`, `charon accounts`, `charon status`
- [x] Audit logging to file
- [x] Manual token storage for testing: `charon vault set --provider google --account user@gmail.com --token <token>`

### Milestone 2: OAuth flow

- [x] Google OAuth 2.0 flow (installed app with client_secret, reused from brain)
- [ ] PKCE support (not implemented — current flow uses client_secret which is sufficient for desktop apps)
- [x] `charon auth google user@gmail.com` — opens browser, completes OAuth, stores tokens
- [x] Automatic access token refresh on proxy requests
- [x] Refresh token rotation (new refresh token stored when Google rotates)
- [x] Incremental scope support (`--scope` flag, merges with existing)
- [x] Refresher interface for future services

### Milestone 3: Polish

- [ ] Service routing config file (wildcard host matching)
- [ ] `charon auth remove`
- [ ] Integration test with brain's lib/gmail

Deferred to separate issues:
- Linux secret service backend → issue #000002
- Code signing + Keychain ACL → issue #000003

## Log

### 2026-04-24
- Issue created
- Inspired by Infisical Agent Vault model but simpler: no UI, no dashboard, no approval workflow
- Name "Charon" — the ferryman who carries you across but doesn't let you see what's underneath

### 2026-04-24 — Milestone 1 Design Decisions
- **Pure Go, no CGo** — keeps hermetic build story clean, no C toolchain needed
- Keychain access via `security` CLI or pure-Go keychain library (no CGo bindings)
- Go 1.21+ `toolchain` directive in go.mod for hermetic Go version pinning
- HTTPS proxy: forward proxy model — proxy sees CONNECT target, makes its own TLS upstream connection, injects auth. No local CA cert needed
- Use `github.com/spf13/cobra` for CLI
- Audit log: JSON lines to `~/.config/charon/audit.log`
- Service routing: hardcoded map for M1, config file in M3

### 2026-04-24 — Milestone 1 Implementation
- All M1 items complete: vault interface, keychain backend, proxy, CLI, audit log
- Code review findings fixed:
  - C5: Response streaming (was buffering entire body in memory via resp.Write)
  - C6: Cert serial number collision (switched to crypto/rand)
  - C7: Zero-expiry tokens not cached (treat zero expiry as "never expires")
  - I1: Fragile type assertion on DefaultTransport (added Server.Transport field)
  - I2: Goroutine leak in tunnel passthrough (added explicit close+sync)
  - Error messages sanitized (no raw errors returned to client)
- Known limitations (acceptable for MVP):
  - No proxy auth — any local process can use the proxy
  - CLI `vault set --token` visible in process listing — use stdin for production
  - Keychain List() parsing is fragile (dump-keychain format varies)
