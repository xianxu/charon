---
id: 000001
status: open
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

- [ ] Go project setup (go.mod, basic structure)
- [ ] Vault interface + macOS Keychain backend
- [ ] HTTPS proxy server (CONNECT method, TLS interception)
- [ ] Token injection based on destination host
- [ ] X-Charon-Account header for multi-account
- [ ] CLI: `charon serve`, `charon accounts`, `charon status`
- [ ] Audit logging to file
- [ ] Manual token storage for testing: `charon vault set --service google --account user@gmail.com --token <token>`

### Milestone 2: OAuth PKCE flow

- [ ] Google OAuth 2.0 + PKCE flow
- [ ] `charon auth google user@gmail.com` — opens browser, completes OAuth, stores tokens
- [ ] Automatic access token refresh on proxy requests
- [ ] Refresh token rotation
- [ ] Incremental scope support
- [ ] Provider interface for future services

### Milestone 3: Polish

- [ ] Linux secret service backend
- [ ] Service routing config file
- [ ] `charon auth remove`
- [ ] Cross-compile and test on Linux
- [ ] Integration test with brain's lib/gmail

## Log

### 2026-04-24
- Issue created
- Inspired by Infisical Agent Vault model but simpler: no UI, no dashboard, no approval workflow
- Name "Charon" — the ferryman who carries you across but doesn't let you see what's underneath
