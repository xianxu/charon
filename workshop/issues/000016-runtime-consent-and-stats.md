---
id: 000016
status: open
deps: []
github_issue:
created: 2026-04-29
updated: 2026-04-29
estimate_hours: 85
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

**Range: 65–124 hr (~6.5–12.5 working days). Best guess: ~85 hr (~8.5 days).**

Produced via `brain/data/life/42shots/velocity/estimate-logic-v1.md` against `baseline-v1.md`. Method A only.

The spec is detailed enough that Method B's design-density contribution is negligible — most decisions are pre-resolved.

| Phase | Primitive match | Base hr | Familiarity × | Adjusted |
|---|---|---|---|---|
| A — session state | Greenfield Go module (single concern) | 8–14 | ×1.0 | 8–14 |
| B — caller ID + audit | Greenfield Go module + macOS proc syscalls (CGo, novel) | 8–14 | ×1.5 | 12–21 |
| C — security.app trust edge | Greenfield Go module, reuses M4 DR-verification | 8–12 | ×1.0 | 8–12 |
| D — menubar + consent UI | Greenfield Go module (closest analog; novel Mac UI stack) | 8–14 | ×1.5 | 12–21 |
| E — stats Tier 1+2 | Smaller Go module + parser | 6–12 | ×1.0 | 6–12 |
| F — CLI surfaces | Familiar (charon CLI extension) | 4–8 | ×1.0 | 4–8 |
| G — atlas + threat-model docs | Atlas/docs maintenance ×3 | 1–3 | ×1.0 | 1–3 |
| H — code review × 3 | Process overhead | 3–12 | ×1.0 | 3–12 |
| **Subtotal** | | | | **54–103** |
| **+20% unknown-unknowns buffer** | | | | **65–124** |

Caveats:
- Assumes baseline calibration (10hr/day focused, solo founder + AI, current-baseline polish; see `baseline-v1.md`).
- Mac UI / AppKit work and CGo Darwin syscalls aren't direct primitives in v1 — handled via ×1.5 familiarity multipliers on adjacent Go-module primitives. Could be off in either direction; record actuals to recalibrate.
- Wide range reflects the 8-phase structure (per-phase ranges compound). Best-guess ~85 hr represents the P50.

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

- [ ] `atlas/charon.md`: session model section
- [ ] `atlas/security-audit.md`: runtime-consent role for
      security.app
- [ ] `docs/threat-model.md`: armed/disarmed gate; content-sampling
      posture; cooldown-on-oracle rationale

### Phase H — milestone code review

- [ ] Invoke `superpowers:requesting-code-review` →
      `superpowers:code-reviewer` after each multi-phase chunk
      (A+B, C+D, E+F+G), `BASE_SHA` = previous milestone close
- [ ] Address Critical / Important findings before next phase

## Log

(empty)
