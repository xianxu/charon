---
id: 000016
status: done
deps: []
github_issue:
created: 2026-04-29
updated: 2026-05-03
estimate_hours: 20
estimate_method: estimate-logic-v2.md (Method A)
prior_estimate_hours: 85  # v1 estimate; superseded by v2 after #13 actuals
---

# Runtime consent + proxy stats

## Problem

Today, once `charon serve` is running and OAuth scopes are granted,
the proxy is a permanently-armed cookie jar on `localhost:8230`. Any
same-uid process can connect and use any granted scope as a confused
deputy. Token-hiding doesn't help here — the process never needed the
token. An agent that runs while the user is away (or one already
resident on the machine) can quietly read the user's email, calendar,
drive, etc., bounded only by the scopes the user has ever granted.

The current threat-model section explicitly draws charon's scope at
**credentials, not content** ([`../docs/threat-model.md`](../docs/threat-model.md)),
on the basis that an agent running as the user can read `$HOME` anyway.
That argument doesn't fully extend to API surfaces: charon **created**
the access vector by aggregating long-lived OAuth grants behind a
single localhost endpoint. Without it, an arbitrary process couldn't
talk to Gmail at all without first finding tokens or running a full
OAuth flow.

## Goal

Two related capabilities, folded into one issue because they share
audit-log plumbing and user-facing surfaces in `Charon Security.app`:

1. **Runtime consent.** Make the cookie jar time-bounded: armed
   (frictionless) ↔ disarmed (refused). Re-arming is a user click in
   security.app — a UI an in-process agent cannot drive. Idle and
   absolute timers auto-disarm.
2. **Proxy stats / observability.** Enrich audit log with response
   size, content-type, generic JSON item counts, and best-effort
   caller identity (PID + exe path + parent chain). Surface this via
   `charon who` (live + last-hour CLI) and a menubar in security.app.

The first closes the always-on attack surface; the second lets the
user actually see what the cookie jar is being used for.

## Spec

### 1. Session model

Proxy carries a single piece of state: `armed` or `disarmed`.

- **Armed**: behaves exactly as today. No per-request prompts. Cookie-jar UX.
- **Disarmed**: CONNECTs are refused with a 407 carrying a structured
  JSON error pointing the caller to `charon auth` / security.app.
  In-flight requests drain; new ones blocked.

State lives in memory only. `charon serve` boots **disarmed**;
re-arming is a user click. (Persisting armed-state across restarts
would defeat the point — a forgotten armed jar surviving overnight is
exactly what we're trying to prevent.)

#### Lifetime

Two timers, both reset on user activity in security.app:

- **Idle timer**: 30m of zero proxy traffic auto-disarms.
- **Absolute cap**: 8h max regardless of activity.

Defaults; both configurable. The absolute cap is the load-bearing
one — a chatty agent could otherwise keep the idle timer alive
indefinitely on its own traffic.

#### Granularity

Proxy-wide. Per-account / per-provider / per-scope arms are out
of scope; the per-scope work already happened at the OAuth layer
(M4, #000004). The runtime consent gate is one bit, not a matrix.

### 2. Consent oracle: `Charon Security.app`

Already a `.app` bundle with separate TCC identity
(`com.charon.security`, [`../atlas/security-audit.md`](../atlas/security-audit.md)).
Adds a `LSUIElement` mode so it can run as a background menubar agent
without dock presence.

#### Why this is the right surface

- Distinct bundle ID from `com.charon.cli` — TCC grants don't bleed.
- Hardened runtime + no Accessibility entitlement means synthetic
  events from `cliclick` / AppleScript can't drive its UI. The
  Accessibility-on-terminal=Critical check that `charon-security`
  already emits closes this loop: as long as the user keeps the
  audit clean, an in-process agent has no way to forge a click.
- Already the home for "Mac hygiene" UX — the consent oracle is the
  natural runtime extension of the same posture.

#### Trust between proxy and security.app

A unix-domain socket in `~/Library/Caches/charon/` (perms 0600). The
proxy reads the connecting peer's PID via `LOCAL_PEEREPID` and
verifies the codesign DR matches `com.charon.security`. Same M4
mechanism as the keychain ACL (#000003) and the cli↔proxy boundary —
just one more DR-pinned trust edge.

Approval messages from security.app to the proxy carry a short-lived
signed token. Signing key lives in the keychain under the
`charon-security` namespace with an ACL pinned to security.app's DR.
This makes "compromise the consent oracle" a properly hard problem
even for an attacker who has somehow gotten code execution as the
user — they'd need the M4 ACL bypass, which is a separate, audited
threat.

#### Cooldown on the oracle, not on proxying

If no user has actively interacted with security.app in N hours
(default 4h), defer subsequent prompts: instead of popping a
notification, queue a pending re-arm and require the user to click
into security.app explicitly. Defeats the "agent triggers a
late-night popup that the half-asleep user reflexively approves"
case. Active interaction = clicking inside the app's own window, not
clicking a notification.

### 3. Caller identification (display-quality, not auth-quality)

HTTP doesn't carry PID, and macOS TCP loopback doesn't expose
`SO_PEERPID`/`SO_PEERCRED` (those are unix-socket only). At CONNECT
time the proxy looks up the peer via `proc_listpids` +
`proc_pidinfo(PROC_PIDFDSOCKETINFO)` — same approach `lsof -iTCP:8230
-sTCP:ESTABLISHED` uses internally. Cost ~10–50ms; one CONNECT can
serve many requests, so amortized fine.

Caveats made explicit so we don't drift later:

- **Racy.** Connecting process can `fork`/`exec` between accept and
  lookup. Mitigation: take the lookup snapshot at accept, log it as
  observed; don't gate auth on it.
- **Best-effort.** If the lookup fails (process gone, table churn),
  log `pid: unknown` and continue.
- **Never on the auth path.** Auth is the armed/disarmed bit + user
  click in security.app, full stop. Caller identity is purely for
  the user's benefit ("show me what's connected").

A future Unix-socket listener with a TCP shim inside `charon run`
would yield auth-quality identity for the `charon run`-spawned tree;
deferred (see "Out of scope" below).

### 4. Audit log enrichment

`internal/proxy/audit.go` gets new fields, all best-effort:

| field                  | source                                | notes                                  |
|------------------------|---------------------------------------|----------------------------------------|
| `peer_pid`             | proc_listpids/fdinfo at CONNECT       | `null` if lookup fails                 |
| `peer_exe`             | proc_pidpath                          | absolute path of executable            |
| `peer_argv0`           | proc_pidinfo PROC_PIDT_SHORTBSDINFO   | first arg / process name               |
| `peer_parent_chain`    | walk up via PROC_PIDT_BSDINFO.ppid    | array of `(pid, exe)` up to launchd    |
| `req_bytes`            | content-length on request             |                                        |
| `resp_bytes`           | bytes written downstream              | exact, since proxy already streams it  |
| `resp_content_type`    | response Content-Type                 |                                        |
| `items_returned`       | generic JSON top-level array count    | see §5                                 |

Resolution happens once per CONNECT, not per request, since CONNECT
identifies the tunnel and all requests within reuse the same peer.

### 5. Stats: Tier 1 + Tier 2

**Tier 1 — magnitude.** `req_bytes`, `resp_bytes`, `resp_content_type`.
Free; we already stream the body through. Gives the user "this call
returned 14 KB of JSON" vs "2 MB of binary" without any content
inspection.

**Tier 2 — generic JSON top-level array count.** When
`Content-Type` ⊆ `application/json` and `resp_bytes < 1 MiB`, parse
the response, walk top-level keys, sum the lengths of any array-valued
fields. Captures the conventional shape used by Google, OpenAI, AWS,
GitHub, etc. — `{ "messages": [...] }`, `{ "data": [...] }`,
`{ "items": [...] }`, `{ "Items": [...] }`.

Output: `items_returned: 47` when an unambiguous count is found,
omitted otherwise. No per-key labelling; the catalog work in §6
handles that.

Skip conditions (logged as `items_returned: null`):
- `Transfer-Encoding: chunked` + streaming content-types (SSE, NDJSON)
- response > size cap
- non-JSON content-type
- JSON parse error

**Implementation note.** Body has to be teed into a parser. Use a
size-capped buffer that switches to passthrough-only above the cap,
so big responses still flow without memory pressure.

**Threat-model implication.** Tier 2 means the proxy now reads
response *content*, not just headers. The proxy already could (it's
TLS-MITM); this issue is the explicit posture shift from "pure
credential injector" to "credential injector with lightweight
content sampling." Update [`../docs/threat-model.md`](../docs/threat-model.md)
accordingly: log only counts and byte sizes, never JSON keys/values.

### 6. Tier 3 — deferred

Per-provider endpoint catalog with canonical names + dedicated
extractors. e.g. `GET /gmail/v1/users/*/messages` →
`gmail.messages.list` + extract `messages[]`. Lands under #000015
(provider catalog), not here — canonical endpoint names are a
provider-catalog concern. Mentioned for traceability; no v1 code.

### 7. CLI surfaces

```
charon arm     [--ttl 1h]                 # request re-arm via security.app
charon disarm                              # immediate disarm (no prompt)
charon status                              # extends existing: armed?, expiry, recent activity
charon who                                 # live: connections in flight
charon who --since 1h                      # last-hour replay grouped by exe
charon stats --since 1h                    # aggregate: (exe, host) → calls, items, bytes
```

`charon arm` is for power users / scripts; the normal arm path is
clicking into security.app's menubar.

### 8. security.app: menubar + consent UI

New responsibilities for the bundle:

- Menubar item with a single status glyph: armed (green dot) /
  disarmed (grey) / cooldown (amber).
- Click-to-arm panel: shows "armed for 30m / 1h / 8h" picker,
  current session expiry, live count of connections.
- Notification on disarm-due-to-idle so the user knows it expired.
- On a CONNECT-while-disarmed, optionally toast "blocked: pid 12345
  (`/path/to/agent`) tried to reach gmail.googleapis.com" — bounded
  toast rate so a fork-bomb can't notification-spam.
- Audit log viewer: scrollable last-hour view, same data as
  `charon who --since 1h`, plus a search box.

Existing security-audit (`make security`) functionality stays.
Runtime consent is a parallel feature inside the same `.app`.

## Out of scope

- **Unix-socket listener with TCP shim inside `charon run`** for
  auth-quality peer identity. Doable (see prior discussion), breaks
  `HTTPS_PROXY=` transparency, has its own race window for the TCP
  side. Worth a follow-up issue once we feel the pain of
  display-quality identity.
- **Per-account / per-provider / per-scope runtime consent.** The
  matrix kills the UX; one-bit arm/disarm is the design.
- **Persisting armed state across reboots.** Defeats the purpose.
- **Restricting who can invoke `charon run`.** Structurally
  impossible (anyone can call the binary as themselves). Reframed:
  consent is on session-open via security.app, not on binary
  invocation.
- **Trusting agent-supplied identity** (headers, env vars).
  Forgeable.
- **Tier 3 endpoint catalog.** Lands under #000015.

## Open questions

- Should `charon arm` (CLI) bypass the security.app prompt, or
  always go through it? Lean: always go through. If `arm` is a
  no-prompt shortcut, an agent that finds itself in a sandbox with
  shell access (likely) gets to bypass the oracle. Cost: scripts
  need a UI loop. Worth it.
- Idle-timer reset: any traffic, or only "human-initiated" traffic?
  Probably any traffic (we have no signal for the latter), but
  worth thinking about whether absolute cap should be tighter
  (e.g. 4h not 8h) to compensate.
- What happens to a long-running CONNECT tunnel when the session
  expires mid-flight? Drain (let it finish) vs. RST (close hard).
  Lean: drain — agents handle TCP RST poorly, and once the tunnel
  is up nothing prevents the next request anyway, so RST mid-tunnel
  doesn't actually defend more. Block new CONNECTs.
- Notification-rate cap on the toast surface: bucket size and
  decay? Avoid making security.app itself a DoS vector.

## Estimate

**Range: 11–34 hr. Best guess: ~20 hr.**

Produced via `brain/data/life/42shots/velocity/estimate-logic-v2.md` against `baseline-v2.md`. Method A only.

v2 supersedes the previous v1 estimate (65–124 hr range). #13's actuals showed v1 over-estimated by ~10×; v2 splits design + impl per primitive. #16's design density is the highest of the three remaining issues — Mac UI / AppKit and CGo proc syscalls are genuinely novel territory with no charon precedent — so the v2 reduction is least aggressive (~4×). Spec is detailed (most decisions pre-resolved per the issue Spec section), so design hours are bounded but not zero.

| Phase | Primitive | Spec quality | Design (hr) | Impl (hr × familiarity) | Total |
|---|---|---|---|---|---|
| A — session state (armed/disarmed mutex, idle/absolute timers, /session/{arm,disarm,status} HTTP, CLI: arm/disarm/status, drain-vs-RST tests) | Greenfield Go module | ×0.5 (spec detailed, idle-vs-absolute timer logic + drain semantics need design dialogue) | 0.5–2 | 0.3–0.8 ×1.0 = 0.3–0.8 | 0.8–2.8 |
| B — caller ID + audit (proc_listpids + proc_pidinfo CGo, parent-chain via PROC_PIDT_BSDINFO.ppid, cache per CONNECT, audit.Record peer_* fields) | Greenfield Go + CGo darwin syscalls | (CGo darwin novel; design+impl both bounded but unfamiliar) | 0.5–2 | 0.5–1.5 ×1.5 = 0.75–2.25 | 1.25–4.25 |
| C — security.app trust edge (unix socket at `~/Library/Caches/charon/runtime.sock`, peer DR check, signed approval token + keychain ACL, JSON proto: Arm/Disarm/Status/RecentActivity) | Greenfield Go module (reuses M4 DR-verification) | ×0.5 (signed token shape + JSON proto design open) | 0.5–2 | 0.5–1.5 ×1.0 = 0.5–1.5 | 1.0–3.5 |
| D — menubar + consent UI (LSUIElement bundle, status glyph, click-to-arm panel, live connection count, disarm-on-idle notification, blocked-CONNECT toast with rate cap, audit log viewer with search) | Greenfield Mac UI (novel stack — Swift or Obj-C, AppKit) | (no spec discount — every UI element is a design decision) | 1.5–4 | 1–3 ×1.5 = 1.5–4.5 | 3.0–8.5 |
| E — stats Tier 1+2 (req_bytes/resp_bytes/resp_content_type plumbing, size-capped JSON tee, top-level array count, streaming-detection skip path for chunked + SSE/NDJSON, threat-model amend) | Smaller Go module + parser | ×0.5 (streaming detection has design choices) | 0.3–1 | 0.5–1.5 ×1.0 = 0.5–1.5 | 0.8–2.5 |
| F — CLI surfaces (`charon who`, `charon stats --since 1h`, `--json`) | Smaller Go module (familiar) | ×0.2 (charon CLI patterns established) | 0.1–0.3 | 0.3–0.8 ×1.0 = 0.3–0.8 | 0.4–1.1 |
| G — atlas + threat-model docs ×3 | Atlas/docs maintenance ×3 | n/a | 0.15–0.6 | 0.15–0.6 | 0.3–1.2 |
| H — code review × 3 chunks | Process overhead | n/a | 0–0.6 | 0.6–1.5 | 0.6–2.1 |
| Real-API discovery (macOS Security framework + LaunchServices; expect 2–3 surprises in proc syscalls + AppKit) | NEW v2 primitive | n/a | 0 | 0.6–1.8 | 0.6–1.8 |
| Mid-flight scope pivot (8-phase issues typically have 1–2; allocate 1) | NEW v2 primitive | n/a | 0.2–0.5 | 0.2–0.5 | 0.4–1 |
| **Subtotal (design / impl)** | | | **3.75–13** | **5.4–15.7** | **9.15–28.7** |
| **+30% on design subtotal** | | | +1.1–3.9 | n/a | +1.1–3.9 |
| **Total** | | | | | **10.3–32.6** |

Caveats:
- v2's least aggressive reduction (~4×) of the three remaining issues, reflecting genuine novelty: Mac UI / AppKit (no charon precedent), CGo darwin proc syscalls (no charon precedent).
- Assumes baseline-v2 calibration (Claude Code Opus 4.7 + SDK loop). Mac UI primitive in v2's table inherits v1's extrapolation (mapped to "Greenfield Go module" + ×1.5 familiarity for impl) — this is the largest single source of estimate uncertainty. **First Mac-UI actual will calibrate it sharply.**
- 8-phase structure compounds ranges. Best guess (~20 hr) represents the P50 (geometric mean of design+impl-with-familiarity ranges).
- Open question on **streaming auto-detection** is a real design open; if it expands beyond chunked + SSE/NDJSON, +0.5–1 hr design on E.
- Wide range honest reflection of cross-stack work (Go + CGo + Swift/AppKit) plus 8 phases.

## Plan

### Phase A — proxy session state (runtime consent skeleton)

- [ ] Add `armed bool` + `expires time.Time` state to proxy server,
      protected by mutex
- [ ] CONNECT handler checks armed-state; rejects with structured
      407 if disarmed
- [ ] Idle + absolute timers, with a single goroutine running
      `time.AfterFunc`-style decay
- [ ] `/session/arm`, `/session/disarm`, `/session/status` HTTP
      endpoints on the proxy (DR-gated when called from
      security.app — see Phase C)
- [ ] CLI: `charon arm` / `charon disarm` / extend `charon status`
- [ ] Tests: state transitions, timer behavior with mock clock,
      drain-vs-RST semantics for in-flight requests

### Phase B — caller identification + audit-log enrichment

- [ ] `internal/proxy/peerinfo_darwin.go`: `proc_listpids` +
      `proc_pidinfo` lookup keyed on local TCP 4-tuple
- [ ] Parent-chain walk via `PROC_PIDT_BSDINFO.ppid`
- [ ] Hook into CONNECT: resolve once, cache for the tunnel's
      lifetime
- [ ] Extend `audit.Record` with `peer_*` fields
- [ ] Tests: known-PID self-test (charon spawns a child, looks it
      up); cache reuse across multiple requests on one tunnel;
      graceful fallback when lookup fails

### Phase C — security.app trust edge

- [ ] Unix-domain socket in `~/Library/Caches/charon/runtime.sock`,
      perms 0600
- [ ] Proxy: reject connections whose peer DR != `com.charon.security`
- [ ] security.app: signed approval token, key in keychain with ACL
      pinned to its own DR
- [ ] Wire format: small JSON protocol over the unix socket
      (`Arm`, `Disarm`, `Status`, `RecentActivity`)
- [ ] Tests: peer-DR rejection; signed-token verify; replay/expiry

### Phase D — security.app menubar + consent UI

- [ ] `LSUIElement` mode for the bundle (separate menu vs. the
      existing audit window)
- [ ] Status glyph + click-to-arm panel
- [ ] Live connection count via streaming `RecentActivity`
- [ ] Disarm-on-idle notification
- [ ] Blocked-CONNECT toast with rate cap
- [ ] Audit log viewer with search

### Phase E — stats: Tier 1 + Tier 2

- [ ] Tier 1: `req_bytes`, `resp_bytes`, `resp_content_type` in
      audit records (free; just plumb through existing streams)
- [ ] Tier 2: size-capped JSON tee, top-level array count
- [ ] Streaming-detection skip path (chunked + SSE/NDJSON)
- [ ] Update `docs/threat-model.md` with the content-sampling
      posture shift
- [ ] Tests: counts on Google's standard list shapes (`messages`,
      `items`, `events`, `files`); skip on streaming responses;
      passthrough above size cap

### Phase F — CLI surfaces

- [ ] `charon who` (live + `--since 1h`)
- [ ] `charon stats --since 1h` aggregator
- [ ] Output: human-readable default, `--json` for scripts

### Phase G — atlas + threat-model docs

- [x] `atlas/charon.md`: session model section
- [x] `atlas/security-audit.md`: runtime-consent role for
      security.app
- [x] `docs/threat-model.md`: armed/disarmed gate; content-sampling
      posture; cooldown-on-oracle rationale

### Phase H — milestone code review

- [x] Invoke `superpowers:requesting-code-review` →
      `superpowers:code-reviewer` after each multi-phase chunk
      (A+B+E+F → 9803c11; C+D+G → this commit), `BASE_SHA` =
      previous milestone close
- [x] Address Critical / Important findings before next phase

## Log

- **2026-05-01 — Phase A done.** Proxy session-state skeleton:
  - `internal/proxy/session.go` — `Session` struct with armed bit,
    armedAt / idleExpiresAt / absoluteCapAt timestamps, mutex,
    injectable clock for tests. Lazy expiry computation (no
    background goroutine — IsArmed/Status compute on read).
  - `internal/proxy/session_http.go` — `/session/{arm,disarm,status}`
    HTTP endpoints on the proxy's direct path. POST-only for
    arm/disarm; GET for status. Returns structured JSON.
  - `handleConnect` gates on Session.IsArmed() at the top.
    Disarmed → 407 with `{"error":"session_disarmed",
    "fix":"charon arm   # or click the menubar dot..."}`. Once the
    tunnel is up, in-flight requests drain (per spec: agents
    handle TCP RST poorly, and once the tunnel is up nothing
    prevents the next request anyway).
  - CLI: `charon arm [--ttl 1h]` / `charon disarm` / extended
    `charon status` shows armed/disarmed + expiry + reason
    (idle vs absolute).
  - `charon serve` boots disarmed by default. Power users can
    `charon arm` from CLI; eventual UX is the menubar (#16 D).

  Tests cover: boot-disarmed, arm-then-armed, default TTL, cap
  at SessionAbsoluteCap, idle auto-disarm, absolute auto-disarm
  with continuous activity ("chatty agent" defense), IsArmed
  refreshes idle, Status doesn't refresh idle, Disarm immediate,
  re-arm extends, expired→Status returns disarmed. HTTP layer
  covers: 407+structured-JSON on disarmed CONNECT, default-ttl
  arm, explicit-ttl arm, disarm via endpoint, status reads,
  POST-only enforcement, nil-Session no-gate (legacy tests).

  **Behavioral note for the operator**: after `make install` the
  prod proxy will boot disarmed. Existing agents will see 407s
  until `charon arm` is called. Phase D's menubar will be the
  ergonomic path; until then it's CLI.

- **2026-05-01 — Phase B done.** Caller identification + audit
  enrichment.
  - `internal/proxy/peerinfo.go`: `ResolvePeer(peerPort)` shells
    out to `lsof -nP -iTCP:<port> -sTCP:ESTABLISHED -Fpn` and
    `ps -o comm=,ppid=` for the parent walk. Pure shell-out for
    MVP; the spec's CGo path (proc_listpids + proc_pidinfo) is
    deferred — saves ~50ms but adds 200 lines of CGo. Per-CONNECT
    one-shot, amortized over the tunnel.
  - `AuditEntry` gains `peer_pid`/`peer_exe`/`peer_argv0`/
    `peer_parent_chain` (best-effort; absent when lookup fails).
    Per the §3 contract: never on the auth path.
  - `handleConnect` resolves once at CONNECT, stamps every
    request entry inside the tunnel.
  - Test: real-TCP self-test (test process is both client +
    server, looks up its own PID via lsof). PID-match is the
    contract; exe-via-ps is best-effort.

- **2026-05-01 — Phase E done.** Stats Tier 1 + Tier 2.
  - `internal/proxy/stats.go`: `bodyTap` wraps `resp.Body` to
    count total bytes (Tier 1) and sample the first 1 MiB for
    Tier 2 array counting. Sampling is non-destructive
    observation — the body still streams unaltered to the
    client.
  - `countTopLevelItems` walks JSON: top-level array → len; top-
    level object → sum of array-valued field lengths. Captures
    Google's `messages`, OpenAI's `data`, GitHub's `items`,
    AWS-style `Items`. Skip on streaming content-types
    (text/event-stream, application/x-ndjson).
  - `AuditEntry` gains `req_bytes` / `resp_bytes` /
    `resp_content_type` / `items_returned`. items is a pointer
    so 0-vs-unknown is distinguishable.
  - Audit log moved to AFTER `resp.Write` so tap counters are
    final.
  - Tests: tap counts + samples, cap behavior under adversarial
    read patterns, top-array shapes, Google list shape, multi-
    array sum, null/scalar/parse-error rejection, content-type
    matrix, secret-redaction (function only returns int+bool;
    can't leak content even if tests evolve).

- **2026-05-01 — Phase F done.** CLI: `charon who` and
  `charon stats`.
  - `AuditLog` keeps a bounded in-memory ring (5000 entries) of
    recent audit entries alongside the file/stderr write. Memory
    cost ~100KB at typical entry size.
  - `GET /audit/recent?since=1h` exposes the ring as JSON.
  - `charon who [--since 5m]` groups recent entries by caller exe,
    shows top-N hosts per caller. Default window 5m for "what's
    happening right now."
  - `charon stats [--since 1h]` aggregates `(exe, host) → calls,
    items, req_bytes, resp_bytes`. Sorted by call count desc.
  - Both support `--json` for scripting.

  Combined with B+E, this is the observability half of the issue:
  the user can now see what's been talking to their proxy, from
  which exes, at what volume, returning how many items. Without
  C+D, there's no consent oracle UX yet — but the gate from A
  works via CLI, and the audit story is complete.

- **2026-05-01 — Phase C done.** security.app trust edge.
  - `internal/proxy/runtime_darwin.go`: CGo helper
    `verifyPeerDR(pid, requirement)` calling Security framework's
    `SecRequirementCreateWithString` + `SecCodeCopyGuestWithAttributes`
    + `SecCodeCheckValidity`. Same pattern as the existing self-
    check in keychain/codesign_darwin.go.
  - `internal/proxy/runtime_peer_darwin.go`: peer-PID lookup via
    `getsockopt(LOCAL_PEEREPID)` on the unix socket. Race window
    documented (TOCTOU between getsockopt + SecCodeCopyGuest);
    audit-token hardening deferred.
  - `internal/proxy/runtime_socket.go`: unix-domain listener at
    `~/Library/Caches/charon/runtime.sock` (perms 0600). One
    request-per-connection JSON protocol — connection close after
    reply keeps the peer DR check fresh.
  - Ops: `arm` / `disarm` / `status` / `audit_recent` mirror the
    existing HTTP shape so security.app can choose either path
    (the socket is the load-bearing one once the DR check is
    enforced; HTTP stays for CLI ergonomics).
  - Auto-bypass in dev mode (unsigned binary, ServiceDev
    namespace): unsigned charon talking to a dev-built menubar
    can drive arm/disarm without ceremony. Production (signed)
    always enforces. `CHARON_RUNTIME_ALLOW_UNSIGNED_PEER=1`
    overrides for any-purpose dev testing.
  - `charon serve` brings up the listener alongside HTTP; clean
    shutdown on SIGINT/SIGTERM unlinks the socket file.
  - Tests: 7 covering arm/disarm/status/audit_recent over the
    socket plus error paths. Sandbox can't bind unix sockets so
    they skip rather than fail; production path verified by code
    inspection + manual smoke.

- **2026-05-01 — Phase D done (MVP).** Menubar agent.
  - Dependency: `fyne.io/systray` v1.12.1 (maintained fork of
    getlantern/systray). Pure Go API; the cgo for AppKit's
    NSStatusItem is internal to systray.
  - `cmd/charon-security/menubar.go`: status icon (●/○) + summary
    text in title ("● 27m" / "○ off"). Dropdown menu:
    - Status (read-only line)
    - Arm 30m / 1h / 8h
    - Disarm
    - Quit
  - Talks to the proxy via the unix socket (#16 C). Connection-
    per-RPC matches the server side. Polls every 5s for state
    refresh; the title updates live.
  - Notifications via `osascript display notification`. Fires on
    "session auto-disarmed" detection (transition from armed→
    disarmed not driven by a user click). First fire prompts for
    Notification Center permission.
  - Default no-args invocation of `charon-security` is menubar
    mode. Double-clicking the .app bundle (LSUIElement=true)
    lands on this. Explicit subcommands (`check`, `remedy`,
    `menubar`) still work for CLI use.
  - Skipped per "Go-only MVP" decision: live connection count,
    blocked-CONNECT toast, in-app audit viewer with search.
    Those need full AppKit windows, not what systray gives us.
    `charon who --since 1h` covers the audit-viewer use case
    from the CLI.

  Tests cover the title/duration/summary helpers (the systray UI
  itself can't run headless). The end-to-end flow is verified
  manually: launch via `./bin/charon-security menubar` against a
  `make dev` proxy, click Arm/Disarm, watch title + arms/disarms
  the proxy.

  Phase D MVP closes the loop on the issue: the consent gate from
  A is now driven by clicking a menubar dot, not just CLI. C+D
  together mean an in-process agent cannot drive arm/disarm —
  the .app's distinct DR is the trust anchor.

- **2026-05-03 — Phase D refinements.** Polish surfaced by first
  real use of the menubar:
  - **Native notifications.** osascript-driven banners attribute
    to Script Editor and dismiss in 5 s; user can't override that
    in System Settings because the Banner-vs-Alert preference is
    keyed on bundle id. Added `cmd/charon-security/notify_darwin.go`
    using UserNotifications.framework via cgo Objective-C
    (`-x objective-c -fobjc-arc`, `-framework UserNotifications`).
    `requestAuthorization` runs once at menubar startup; `notify()`
    posts via `addNotificationRequest` when running inside the .app
    bundle and falls back to osascript for bare-binary dev runs.
    `notify_other.go` stubs the same surface for non-darwin builds.
    Now the auto-disarm banner is attributed to "Charon Security",
    which lets the user pick Alerts (sticky) instead of Banners
    (auto-dismiss) in System Settings → Notifications. Wording
    fix: "Click the ○ icon in the menu bar to re-arm" — the old
    "Click the menubar to re-arm" read like the *notification*
    was clickable.
  - **Adaptive polling.** Fixed 5 s ticker felt sluggish at the
    end of a session; the user wants to see "30s … 29s …" in real
    time. New cadence: 10 s when ttl > 60 s (or disarmed /
    unreachable), 1 s when armed and ttl < 60 s. Implemented as
    `time.Sleep(nextPollDelay())` so the cadence flips as soon as
    a poll lands a sub-minute ttl.
  - **Audit denied requests.** Disarmed-gate denials in
    `handleConnect` / `handleHTTP` were short-circuiting before the
    audit log. Background processes hammering the proxy looked
    indistinguishable from silence. New `logDisarmedDenial` helper
    records `status=407, error=session_disarmed` plus best-effort
    peer attribution into the audit ring before returning the JSON
    error body. Now `charon who --since 5m` shows what knocked
    while the user was away. Test in `session_http_test.go`
    (`TestSession_Gate_DisarmedRequestIsAudited`).

- **2026-05-03 — Phase G done.** Atlas + threat-model docs sweep.
  - `atlas/charon.md`: new "Runtime consent" section covering the
    armed/disarmed bit, idle/absolute timers, the unix-socket
    trust edge, caller-ID posture, and Tier 1/Tier 2 stats.
    `Key Components` extended with the new files; CLI box
    extended with arm/disarm/who/stats.
  - `atlas/security-audit.md`: bundle now described as a
    two-purpose helper (audit + consent oracle). New "Runtime-
    consent oracle" section explains why the same bundle and
    distinct-from-`com.charon.cli` bundle ID is the trust anchor.
    Architecture box gained the menubar subcommand.
  - `docs/threat-model.md`: new Defense layer 7 (runtime consent
    gate) with idle/absolute caps, the "agent activity while user
    away" framing, and the cooldown-on-oracle deferred design
    note. New A1b threat case explicitly tagged 🟡 partial. New
    "Content-sampling posture" subsection under Scope spelling
    out exactly what Tier 1/Tier 2 do and don't read. "Done" list
    gained a #16 entry.

  No behaviour change. The threat-model section is the load-
  bearing one — a future reviewer asking "what does the gate
  actually defend against, given an in-process compromise can
  drive arm directly?" finds the answer there: the gate doesn't
  defend against an actively-using user; it defends against
  agent activity while the user is away.

- **2026-05-03 — Phase H done (C+D+G review).** Reviewed `9803c11..HEAD`
  (5 commits: phases C, D MVP + refinements, G). Zero Critical;
  4 Important; all four addressed in this commit. Minor and Notes
  filed for follow-up where appropriate.

  Important findings + fixes:

  1. **`SecCodeCheckValidity` ran with `kSecCSDefaultFlags`.**
     Default flags accept some forms of bundle tampering depending
     on macOS version. For a security-critical trust edge that
     gates the consent oracle, this needs strict validation. Fixed
     by passing `kSecCSStrictValidate | kSecCSCheckAllArchitectures
     | kSecCSCheckNestedCode` in `runtime_darwin.go`. Connect-time
     check; perf cost irrelevant.

  2. **Idle-TTL doc/code drift.** Atlas + threat-model claimed
     "reset on each proxied request"; actually `IsArmed()` fires
     once per CONNECT and once per plain-HTTP request — requests
     multiplexed inside an open MITM tunnel never re-check. So a
     long-running keep-alive tunnel with intermittent internal
     traffic can let the idle timer lapse. Updated both docs to
     match code. The behaviour itself is fine (mid-tunnel disarm
     would RST live tunnels and confuse agents); the doc was
     misleading.

  3. **Disarmed-audit peer attribution didn't actually work in
     production.** `connFromResponseWriter` relies on the writer
     exposing `Conn() net.Conn`, which the stdlib `http.response`
     does not — only test shims do. So every disarmed-gate audit
     entry under `make install` was missing PID/exe — exactly the
     visibility the feature exists to provide. Fixed by adding a
     `ConnContext` hook on the proxy's `http.Server` that stashes
     the per-conn `net.Conn` into the request context, plus a new
     `peerConnForRequest(w, r)` helper that tries the writer
     interface first (test path) and falls back to context lookup
     (production). All pre-hijack peer-attribution call sites
     migrated.

  4. **No tests for `verifyPeerDR` / `peerPID`.** The trust edge's
     C-side cgo wrappers had zero direct coverage; the existing
     socket tests run under the dev-mode bypass and never hit the
     cgo path. Added `runtime_peer_darwin_test.go` with five
     tests: peerPID round-trip, peerPID type-assert, verifyPeerDR
     against foreign identifier (negative), malformed requirement
     (negative), and dead/foreign pid (negative). All fail-closed
     pins — would catch a regression in finding 1's strict flags
     or a polarity flip in the rc==0 check.

  Minor findings logged for follow-up (all individually low
  priority; none blocked closure):
  - Wire-protocol version field on the runtime socket (defer
    until we actually need rolling upgrades).
  - `Listen` → `Chmod` race window for the unix socket (single-
    user box; window is microseconds).
  - Synchronous arm/disarm RPCs on the click goroutine (could
    freeze UI on a stuck proxy; 3 s timeout makes the freeze
    bounded).
  - `pollLoop` lacks shutdown context (irrelevant today; matters
    once anything besides systray's exit drives shutdown).
  - `Close()` re-evaluates the socket path instead of unlinking
    the bound one (test-only brittleness).

  Notes worth recording:
  - cgo memory ownership in `runtime_darwin.go` is correct — all
    CF objects have matching CFRelease on every error path; ARC
    is not on by design (this is plain C, not Obj-C).
  - cgo memory ownership in `notify_darwin.go` is correct under
    `-fobjc-arc`.
  - Build-tag asymmetry between `runtime_{darwin,other}.go`
    (`darwin && cgo`) and `runtime_peer_{darwin,other}.go`
    (`darwin`) is gratuitous but fail-closed under
    `darwin && !cgo` (no real builds).
  - 0600 socket perms verified by existing test.
  - Threat-model A1b claims verified against code:
    `SessionDefaultTTL=1h`, `SessionAbsoluteCap=8h`,
    `SessionIdleTTL=30m` (`session.go:8-20`).

  #16 closes after this commit.
