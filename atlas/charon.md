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
- `internal/oauth/google.go` — Google OAuth flow: browser auth, local callback, token exchange, refresh with rotation, ID token email extraction
- `internal/oauth/scope_catalog.go` — known Google scope definitions with short names
- `internal/oauth/obfuscate.go` — XOR encode/decode for baked-in client credentials (same mechanism as brain)
- `internal/proxy/scope_tracker.go` — scope denial tracking (ring buffer) + scope enforcement helpers

## Credential Flow
```
request host → Provider (routing table) → {provider.Name, account} → token (vault/cache)
  → if expired and has refresh_token → Refresher.Refresh() → updated token + vault persist
  → InjectAuth (bearer header)
```
- Routing: exact host match first, then suffix match (e.g. `*.googleapis.com` → `{google, bearer}`)
- Account resolution: single account auto-selected; multiple requires `X-Charon-Account` header
- Token cache: in-memory `sync.Map`, keyed by `provider:account`, respects expiry with 30s grace
- Cache invalidation: `vault set/delete` POSTs to `/cache/clear` on the proxy

## OAuth
- Google OAuth 2.0 installed app flow (client_id + client_secret baked in, XOR-obfuscated)
- `charon auth google [email]` — opens browser, email auto-detected from ID token; optional email used as `login_hint`
- Tokens stored in macOS Keychain (refresh_token persisted, access_token cached in memory)
- Auto-refresh: proxy detects expired tokens, calls `Refresher.Refresh()`, persists rotated refresh tokens
- Incremental scopes via `charon auth google grant user@gmail.com scope1 scope2`
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

## Scope Management
- **Scope enforcement**: Callers can declare required scopes via `X-Charon-Scope: gmail.readonly,calendar.readonly` header. Proxy checks against granted scopes and returns 407 with structured JSON error on mismatch.
- **Scope tracking**: Proxy tracks scope denials (bounded ring buffer, 100 entries, 24h expiry). Exposed via `/scopes/denied` endpoint.
- **Scope catalog**: `charon auth google scopes` lists known Google scopes. `charon auth google scopes user@gmail.com` shows granted scopes.
- **Grant command**: `charon auth google grant user@gmail.com calendar.readonly` triggers incremental OAuth.
- **Fix command**: `charon auth fix` / `charon auth google fix [email]` queries proxy for denied scopes and offers to grant them interactively, one provider×account pair at a time.
- **Scope resolution**: Short names (e.g. `calendar.readonly`) resolve to full URLs (e.g. `https://www.googleapis.com/auth/calendar.readonly`).

## Design Decisions
- **Pure Go, no CGo** — hermetic builds, no C toolchain needed
- **Persistent CA** — stored in `~/.config/charon/`, reused across restarts
- **Keychain via `security` CLI** — avoids CGo macOS bindings
- **HTTP/1.1 forced upstream** — necessary for HTTP/1.1 MITM, standard practice
- **Chunked re-encoding** — Go's transport dechunks upstream responses; proxy re-adds `Transfer-Encoding: chunked` when `ContentLength < 0` so clients know where the body ends
- Token stored as JSON in keychain; access token cached in memory
- Health endpoint at `/healthz`, CA download at `/ca.pem`, cache clear at `/cache/clear`
- Auth method configurable per provider, defaults to bearer

## Test Coverage (90+ tests)
- **CLI** (17) — all commands, flags, validation, help text, proxy lifecycle
- **Proxy** (9) — HTTP/CONNECT injection, passthrough, multi-account, health, CA endpoint
- **Scope enforcement** (7) — scope granted/missing, multiple scopes, denial tracking, /scopes/denied endpoint
- **Scope tracker** (5) — track, filter, expiry, max size, missing scope detection
- **Refresh** (4) — auto-refresh on expiry, failure fallback, vault persistence, no-refresher case
- **Cache** (8) — expiry simulation with mock clock, cache clear, account resolution, vault fetch count
- **Keep-alive** (2) — 5 requests to same host = 1 CONNECT tunnel; different hosts = separate tunnels
- **Routing** (6) — all Google hosts, unknown hosts, InjectAuth dispatch
- **CA/Cert** (5) — generation, persistence, DNS/IP SANs, serial uniqueness
- **Audit** (4) — JSON lines format, append mode, default path
- **Vault** (8) — expiry logic with `IsExpiredAt` (7), grace period boundary
- **Memory store** (2) — CRUD, not-found
- **Keychain** (5) — `security` CLI contract tests (flags/subcommands exist)
- **OAuth** (14) — ID token parsing (7), login_hint (2), scope merging, required scopes, scope catalog (3), XOR
- **Keychain integration** (5) — behind `integration` build tag

## Zero-Config Deployment
Single binary, everything in keychain:
- CA cert + key → keychain (service: `charon`, accounts: `_ca:cert`, `_ca:key`)
- OAuth credentials → keychain (service: `charon`, account: `provider:email`)
- CA bundle → ephemeral temp dir, regenerated on each `serve` start
- Audit log → stderr by default, `--audit-log <path>` for file output
- No config directory needed

## Logging
- Normal mode: startup info and errors only
- `charon serve -v`: debug logging (TLS handshakes, per-request details, connection close reasons)
- Audit log: JSON lines to stderr (method, host, path, status, latency, provider, account)

## Scope-Management TUI (`charon auth`)

The interactive TUI is the canonical UX for OAuth scope management
(see #000005). Replaces the legacy `auth google scopes/grant/fix`
command family.

```
charon auth                                            # opens picker
```

Picker → pick existing account or "+ new account" → scope view:
- Search bar at top with substring filter (case-insensitive on short
  name + description)
- Catalog rows + custom keychain scopes + proxy-requested scopes
- Color matrix: muted grey (off), muted yellow (off + requested by
  proxy), normal (granted), green (toggled on, pending), red (toggled
  off, pending)
- Persistent session markers: `+` for scopes added in this session,
  `-` for scopes removed in this session
- Required scopes (openid, email) display as `[x] foo (req)`, can't
  be toggled off

Key bindings (see help line at the bottom of the TUI):
- search focus: type to filter, ↓/enter → list, ^r revoke account, esc quit
- list focus: ↑↓ nav, space toggle, enter apply, a add custom URL,
  ^r revoke account, / search, q quit

Apply paths:
- target == realized: no-op exit
- target ⊋ realized (additive): incremental OAuth (`include_granted_scopes=true`)
- target has any reduction: confirmation modal → fresh OAuth
  (`include_granted_scopes=false`) so the new token covers exactly the
  requested set, not the union of past grants
- ^r (revoke account): confirmation modal → calls Google's revoke
  endpoint, deletes the credential from the keychain, exits

### TUI environment knobs

- `CHARON_TUI_HEIGHT=N` — manual height override (raw value, no -1
  margin). Useful when the multiplexer doesn't keep PTY size in sync
  with the visible pane.
- `CHARON_TUI_NO_ALT=1` — disable alt-screen mode. For diagnosing
  terminals where alt-screen interacts badly with bubbletea's render diff.
- `CHARON_TUI_DEBUG=1` — log every render and key event to
  `/tmp/charon-tui-debug.log`. Off by default (zero overhead).

## CLI
```
charon serve [-v] [--audit-log path]                  # start proxy
charon run -- <cmd>                                    # run child with proxy env
charon auth                                            # scope-management TUI
charon accounts                                        # list credentials with scopes
charon status                                          # check proxy
charon vault set/delete                                # manual token management
charon service install/uninstall/start/stop/status     # OS service management
```

## Status
- M1 (proxy + keychain + manual token + `charon run`): done
- M2 (OAuth + auto-refresh + keep-alive): done
- M3 (wildcard routing + auth remove + zero-config + integration test): done
- M4 (scope management + auth flow improvements): done (#000004)
- M5 (scope-management TUI replaces legacy auth subcommands): done (#000005)
- Future: multi-provider (#000006), scope catalog with categories +
  filter syntax (#000007), Linux secret service (#000002), code
  signing + Keychain ACL (#000003), PKCE
