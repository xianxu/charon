# Charon: Credential Proxy

## What
Single Go binary that acts as a fully transparent HTTPS forward proxy. AI agents route traffic through it via `HTTPS_PROXY`, and Charon transparently injects credentials into requests. The agent never sees the token, uses real API URLs, requires no code changes.

## Architecture
```
charon serve          → starts HTTPS proxy on localhost:8230
                        generates persistent CA cert (~/.config/charon/ca.pem)
                        builds combined CA bundle (system CAs + charon CA)

charon run -- <cmd>   → sets HTTPS_PROXY, SSL_CERT_FILE, REQUESTS_CA_BUNDLE, etc.
                        exec's child process — fully transparent

Agent → HTTPS request (real URL) → Charon proxy (CONNECT + TLS interception)
                                     → injects credential → upstream API
                                     ↕
                                OS Keychain (macOS)
```

## Key Components
- `cmd/charon/main.go` — CLI (cobra): `serve`, `run`, `accounts`, `status`, `vault set/delete`
- `internal/vault/vault.go` — `Store` interface + `Credential` type with expiry logic
- `internal/vault/keychain/` — macOS Keychain backend (pure Go, via `security` CLI)
- `internal/vault/memory/` — in-memory backend for testing
- `internal/proxy/proxy.go` — HTTPS forward proxy with CONNECT interception + credential injection
- `internal/proxy/cert.go` — persistent CA (`LoadOrCreateCA`), per-host cert generation with DNS/IP SAN support
- `internal/proxy/cabundle.go` — builds combined CA bundle (system CAs + charon CA)
- `internal/proxy/routing.go` — host → `Provider` mapping with pluggable `AuthMethod`
- `internal/proxy/audit.go` — append-only JSON lines audit log

## Credential Flow
```
request host → Provider (routing table) → {provider.Name, account} → token (vault) → InjectAuth
```
- Routing: exact host match → `Provider{Name, Auth}` (e.g. `gmail.googleapis.com` → `{google, bearer}`)
- Account resolution: single account auto-selected; multiple requires `X-Charon-Account` header
- Token cache: in-memory `sync.Map`, keyed by `provider:account`, respects expiry with 30s grace

## Auth Method (pluggable)
```go
type AuthMethod string
const AuthBearer AuthMethod = "bearer"  // Authorization: Bearer <token>
// Future: AuthBasic, AuthHeader, AuthQuery, AuthAWSSigV4
```
Currently only `bearer` is implemented. Each `Provider` has an `Auth` field and an `InjectAuth` method that dispatches on it. Adding a new method = one switch case.

## Transparent Proxy Model (like Infisical Agent Vault)
- `charon run -- python agent.py` wraps child with proxy env vars
- Agent code uses real URLs (`https://gmail.googleapis.com/...`), no changes needed
- CONNECT tunneling: known hosts get TLS interception + credential injection; unknown hosts get raw passthrough
- CA trust handled automatically via `SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE`, `NODE_EXTRA_CA_CERTS`, `CURL_CA_BUNDLE`, `GRPC_DEFAULT_SSL_ROOTS_FILE_PATH`

## Multi-account
Agent sends `X-Charon-Account: user@gmail.com` header to select account when multiple exist for same provider. Charon strips it before forwarding.

## Design Decisions
- **Pure Go, no CGo** — hermetic builds, no C toolchain needed
- **Persistent CA** — stored in `~/.config/charon/`, reused across restarts
- **Keychain via `security` CLI** — avoids CGo macOS bindings
- Token stored as JSON in keychain; access token cached in memory
- Health endpoint at `/healthz`, CA download at `/ca.pem`
- Auth method configurable per provider, defaults to bearer

## Test Coverage (47 tests)
- **CLI** (17) — all commands, flags, validation, help text, proxy lifecycle
- **Proxy** (9) — HTTP/CONNECT injection, passthrough, multi-account, health, CA endpoint
- **Routing** (6) — all Google hosts, unknown hosts, InjectAuth dispatch
- **CA/Cert** (5) — generation, persistence, DNS/IP SANs, serial uniqueness
- **Audit** (4) — JSON lines format, append mode, default path
- **Vault** (7) — expiry logic (5), memory store CRUD (2)

## Status
- M1 (proxy + keychain + manual token + `charon run` + tests): implemented
- M2 (OAuth PKCE): not started
- M3 (Linux, config file, wildcard host matching, polish): not started
