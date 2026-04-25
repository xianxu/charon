# Charon: Credential Proxy

## What
Single Go binary that acts as a fully transparent HTTPS forward proxy. AI agents route traffic through it via `HTTPS_PROXY`, and Charon transparently injects credentials into requests. The agent never sees the token, uses real API URLs, requires no code changes.

## Architecture
```
charon serve          → starts HTTPS proxy on localhost:8230
                        generates persistent CA cert (~/.config/charon/ca.pem)
                        builds combined CA bundle (system CAs + charon CA)

charon auth google x  → OAuth flow: browser → callback → tokens in keychain

charon run -- <cmd>   → sets HTTPS_PROXY, SSL_CERT_FILE, REQUESTS_CA_BUNDLE, etc.
                        exec's child process — fully transparent

Agent → HTTPS request (real URL) → Charon proxy (CONNECT + TLS interception)
                                     → injects credential → upstream API
                                     ↕                ↕
                                OS Keychain      OAuth refresh
                                (macOS)          (automatic)
```

## Key Components
- `cmd/charon/main.go` — CLI (cobra): `serve`, `run`, `auth`, `accounts`, `status`, `vault set/delete`
- `internal/vault/vault.go` — `Store` interface + `Credential` type with expiry logic
- `internal/vault/keychain/` — macOS Keychain backend (pure Go, via `security` CLI)
- `internal/vault/memory/` — in-memory backend for testing
- `internal/proxy/proxy.go` — HTTPS forward proxy with CONNECT interception + credential injection + auto-refresh
- `internal/proxy/cert.go` — persistent CA (`LoadOrCreateCA`), per-host cert generation with DNS/IP SAN support
- `internal/proxy/cabundle.go` — builds combined CA bundle (system CAs + charon CA)
- `internal/proxy/routing.go` — host → `Provider` mapping with pluggable `AuthMethod`
- `internal/proxy/audit.go` — append-only JSON lines audit log
- `internal/oauth/google.go` — Google OAuth flow: browser auth, local callback, token exchange, refresh with rotation
- `internal/oauth/obfuscate.go` — XOR encode/decode for baked-in client credentials (same mechanism as brain)

## Credential Flow
```
request host → Provider (routing table) → {provider.Name, account} → token (vault/cache)
  → if expired and has refresh_token → Refresher.Refresh() → updated token + vault persist
  → InjectAuth (bearer header)
```
- Routing: exact host match → `Provider{Name, Auth}` (e.g. `gmail.googleapis.com` → `{google, bearer}`)
- Account resolution: single account auto-selected; multiple requires `X-Charon-Account` header
- Token cache: in-memory `sync.Map`, keyed by `provider:account`, respects expiry with 30s grace
- Cache invalidation: `vault set/delete` POSTs to `/cache/clear` on the proxy

## OAuth
- Google OAuth 2.0 installed app flow (client_id + client_secret baked in, XOR-obfuscated)
- `charon auth google user@gmail.com` — opens browser, local callback server on dynamic port
- Tokens stored in macOS Keychain (refresh_token persisted, access_token cached in memory)
- Auto-refresh: proxy detects expired tokens, calls `Refresher.Refresh()`, persists rotated refresh tokens
- Incremental scopes: `--scope` flag merges with existing grants
- `Refresher` interface: pluggable per-provider, wired into `Server.Refreshers` map

## Auth Method (pluggable)
```go
type AuthMethod string
const AuthBearer AuthMethod = "bearer"  // Authorization: Bearer <token>
// Future: AuthBasic, AuthHeader, AuthQuery, AuthAWSSigV4
```
Currently only `bearer` is implemented. Each `Provider` has an `Auth` field and an `InjectAuth` method that dispatches on it.

## Transparent Proxy Model (like Infisical Agent Vault)
- `charon run -- python agent.py` wraps child with proxy env vars
- Agent code uses real URLs (`https://gmail.googleapis.com/...`), no changes needed
- CONNECT tunneling: known hosts get TLS interception + credential injection; unknown hosts get raw passthrough
- HTTP keep-alive supported within a CONNECT tunnel (multiple requests per connection)
- CA trust handled automatically via `SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE`, `NODE_EXTRA_CA_CERTS`, `CURL_CA_BUNDLE`, `GRPC_DEFAULT_SSL_ROOTS_FILE_PATH`

## HTTP/2 Downgrade
The MITM proxy operates at HTTP/1.1 on both sides (client↔proxy and proxy↔upstream). Upstream connections force HTTP/1.1 via `TLSNextProto: {}` on the transport. This is standard for MITM proxies (mitmproxy, Infisical Agent Vault do the same) — the MITM reads/writes HTTP/1.1 text framing, which is incompatible with HTTP/2 binary frames. The practical impact is negligible: API latency (80-200ms) dwarfs any protocol overhead, and keep-alive provides connection reuse.

## Multi-account
Agent sends `X-Charon-Account: user@gmail.com` header to select account when multiple exist for same provider. Charon strips it before forwarding.

## Design Decisions
- **Pure Go, no CGo** — hermetic builds, no C toolchain needed
- **Persistent CA** — stored in `~/.config/charon/`, reused across restarts
- **Keychain via `security` CLI** — avoids CGo macOS bindings
- **HTTP/1.1 forced upstream** — necessary for HTTP/1.1 MITM, standard practice
- **Chunked re-encoding** — Go's transport dechunks upstream responses; proxy re-adds `Transfer-Encoding: chunked` when `ContentLength < 0` so clients know where the body ends
- Token stored as JSON in keychain; access token cached in memory
- Health endpoint at `/healthz`, CA download at `/ca.pem`, cache clear at `/cache/clear`
- Auth method configurable per provider, defaults to bearer

## Test Coverage (69 tests)
- **CLI** (17) — all commands, flags, validation, help text, proxy lifecycle
- **Proxy** (9) — HTTP/CONNECT injection, passthrough, multi-account, health, CA endpoint
- **Refresh** (4) — auto-refresh on expiry, failure fallback, vault persistence, no-refresher case
- **Cache** (8) — expiry simulation with mock clock, cache clear, account resolution, vault fetch count
- **Keep-alive** (2) — 5 requests to same host = 1 CONNECT tunnel; different hosts = separate tunnels
- **Routing** (6) — all Google hosts, unknown hosts, InjectAuth dispatch
- **CA/Cert** (5) — generation, persistence, DNS/IP SANs, serial uniqueness
- **Audit** (4) — JSON lines format, append mode, default path
- **Vault** (8) — expiry logic with `IsExpiredAt` (7), grace period boundary
- **Memory store** (2) — CRUD, not-found
- **Keychain** (5) — `security` CLI contract tests (flags/subcommands exist)
- **OAuth** (3) — XOR round-trip, invalid hex, empty string
- **Keychain integration** (5) — behind `integration` build tag

## Logging
- Normal mode: startup info and errors only
- `charon serve -v`: debug logging (TLS handshakes, per-request details, connection close reasons)
- Audit log: JSON lines at `~/.config/charon/audit.log` (method, host, path, status, latency, provider, account)

## Status
- M1 (proxy + keychain + manual token + `charon run` + tests): done
- M2 (OAuth + auto-refresh + keep-alive + verbose logging): done
- M3 (Linux, config file, wildcard host matching, polish): not started
