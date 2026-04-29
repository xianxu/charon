# Charon threat model

This doc enumerates the attackers charon is designed to defend against,
what defenses are in place, and — equally importantly — what's
explicitly out of scope. It's written so a security-aware user
considering charon for personal use can decide whether the boundary it
draws matches their threat profile.

If you find yourself debating whether to add a defense for some scenario,
this doc is the canonical place to record the decision and reasoning.

---

## Posture in one page

### What charon protects, and what it explicitly doesn't

Charon protects **credentials**, not **user content**.

| Protected | Not protected |
|---|---|
| OAuth refresh tokens stored in keychain | Files in your home directory |
| The proxy CA private key | Your shell history, browser cookies, mail stores |
| The signing key that mints charon-trusted binaries | Outbound network traffic the agent makes (from non-credentialed paths) |

**Why this asymmetry**: an AI agent running on your Mac runs as your
user. By Unix convention, that already gives it read/write access to
your home directory, the ability to spawn processes, and the ability
to make network requests. Trying to "protect" your files from a
process running as you is a category error — it can read them by any
of a hundred paths. Charon doesn't try. Use a sandbox. 

What charon does try to do: prevent the agent from **escalating its
blast radius via stolen credentials**. Without your OAuth tokens or
the proxy CA, the agent's reach is bounded to "what an unprivileged
user-level program can do with no access to your accounts." With
them, the agent can act as you against every API you've authorized —
read your inbox, send mail in your name, delete files in your Drive,
etc. — and a leaked refresh token persists for months. Closing that
escalation path is charon's job.

### The defense in depth, in three conceptual layers

```
┌─────────────────────────────────────────────────────────────────┐
│ Layer 3 — Audit hygiene (`charon-security`)                     │
│   Verifies layers 1 and 2 are intact and the surrounding        │
│   environment doesn't undermine them.                           │
└─────────────────────────────────────────────────────────────────┘
            ▲ confirms ▲
┌─────────────────────────────────────────────────────────────────┐
│ Layer 2 — Single-use signing key                                │
│   Signing key has an empty trusted-applications list. Every     │
│   `make install` requires a human keychain-Allow click. An      │
│   agent can't silently sign a binary as charon.                 │
└─────────────────────────────────────────────────────────────────┘
            ▲ guards ▲
┌─────────────────────────────────────────────────────────────────┐
│ Layer 1 — DR-pinned keychain ACL                                │
│   Charon's keychain entries are pinned to charon's Designated   │
│   Requirement (Apple-anchored, team-id, identifier). Only a     │
│   binary that satisfies the DR can read silently. Anything else │
│   triggers an Allow/Deny dialog naming the calling process.     │
└─────────────────────────────────────────────────────────────────┘
            ▲ holds ▲
        Credentials in macOS Keychain
```

**Layer 1 — DR-pinned keychain ACL.** Each charon-managed keychain
entry has a `SecAccess` whose trusted-applications list contains
charon's Designated Requirement (`anchor apple generic and certificate
leaf[subject.OU] = "<TEAMID>" and identifier "com.charon.cli"`).
Whichever process calls `SecItemCopyMatching` is evaluated against
that DR. Charon (signed via `make install`) satisfies it, reads
silently. Any other process — `/usr/bin/security`, an agent's custom
binary, a stolen-cert imposter — fails the DR and macOS pops a dialog
naming the calling process. The user's job at that prompt: deny.

**Layer 2 — Single-use signing key.** To produce a binary that
satisfies layer 1's DR, you need to sign with charon's identity. That
identity's private key in your login keychain has an **empty
trusted-applications list** by intent. Every `codesign` invocation —
i.e., every `make install` — pops a keychain Allow/Deny dialog. The
agent can't bypass this without a human keystroke. The user's job at
that prompt: click **Allow**, **NEVER Always Allow**.

**Layer 3 — Audit hygiene** (the `charon-security` tool, in
[atlas/security-audit.md](../atlas/security-audit.md)). Layers 1 and 2
hold *if* the user follows the discipline (don't click Always Allow,
don't grant terminals dangerous TCC permissions, etc.). Layer 3 is the
recurring check that those properties haven't drifted: SIP enabled,
no terminal/IDE has FDA or Accessibility, charon's keychain entries
still have ACLs, the signing key's trusted-apps list is still empty,
no suspicious launchd persistence. Run via `make security`. Findings
come tagged Critical / Important / Info / Hygiene.

### User best practices — the reasonable bar

Running an AI agent as your user is reasonable iff **all** of these
hold. Most are macOS hygiene; one is a discipline charon specifically
relies on. The list is numbered, and `make security` walks the same
numbers — its top-of-output bar-status summary maps directly to this
table.

| # | Property | How to check |
|---|---|---|
| **1** | SIP enabled | `csrutil status` says `enabled` |
| **2** | No terminal/IDE or dangerous-path TCC client has Full Disk Access. *Dangerous paths*: `/usr/bin/security`, `/usr/bin/codesign`, shells (`/bin/sh`/`bash`/`zsh`), interpreters, `osascript`. | System Settings → Privacy & Security → Full Disk Access pane has no terminal/IDE/shell/interpreter listed. |
| **3** | …same as 2, Accessibility section (window managers like Rectangle / Hammerspoon are fine; they don't run shells). | …same pane, Accessibility section |
| **4** | …same as 2, Screen Recording. | …same pane, Screen Recording section |
| **5** | …same as 2, AppleEvents/Automation. Plus: no terminal/IDE has automation rights to credential apps (Keychain Access, 1Password, Bitwarden, etc.). | …same pane, Automation section |
| **6** | Sudo cache empty when launching agents | `sudo -k` before, fresh terminal afterwards |
| **7** | **Always Allow never clicked** on the charon signing-key dialog — and no `codesign` / `security` CLI in the key's trusted-applications list (Apple-default entries like Certificate Assistant, racoon, ServerManagerDaemon are tolerated) | `make security` rolls finding to Hygiene with verdict "all benign" if only Apple defaults are present, Critical if codesign / security CLI are trusted, Important if any entry is unrecognized |
| **8** | Charon's keychain entries have ACLs | `make security` reports no `charon-entries-acl-missing-*` finding |
| **9** | No suspicious launchd persistence | `make security` lists what's there; user reviews |
| **10** | Installed charon CLI is signed with the expected identifier and hardened runtime | `make security` checks `~/.local/bin/charon` via `codesign -dvv` |
| **11** | FileVault enabled (boot volume encrypted at rest) | `make security` parses `diskutil info /` for `FileVault: Yes` |

`make security` automates all eleven. Caveats: items 2–5 require Full
Disk Access on the bundle (Tahoe needs Dev ID + notarization for
that — done in #000011); item 7's classifier covers the named
catastrophic apps (codesign, security CLI) and ~6 Apple-default
benign apps — anything outside those lists surfaces as "unknown"
for manual review (curated list maintained in
`internal/security/check_charon.go`; new patterns get added as
they're encountered).

The single most overlooked item is **#3, Accessibility on a terminal**.
A process with Accessibility can synthesize keystrokes and clicks,
which means it can click through layer 1's Allow/Deny dialog itself.
Layer 1 collapses entirely. Audit it.

### What still leaks even with everything above

Even with all defenses healthy, a hostile agent running as your user
can still:

- Read all files in your home directory (anything not behind FDA).
- Make outbound network requests (unless you add OS-level firewall
  rules; see future-work item #3).
- Install a launchd persistence beachhead under `~/Library/LaunchAgents/`
  (audited but not blocked; A7).
- Call any API the user can call — *minus* anything that requires
  charon's credentials, which is the credential-bound subset charon
  is designed to protect.

Charon shrinks the agent's blast radius from "everything you've ever
authorized" to "what an unprivileged user-level program can do." That
gap is the value proposition. The defenses above hold that gap.

---

## Scope

Charon is a personal-machine credential proxy. The threat model assumes:

- **Single human user** logged into a single Mac.
- **Untrusted code running as that user** — the AI agent the user has
  launched, plus the long tail of `npm install`, `pip install`, `curl |
  bash`, etc.
- **No special privileges** for that untrusted code: no admin password,
  no granted TCC permissions, no ability to disable SIP.

This is the threat model for a personal dev tool. A multi-user
deployment ("charon as a service") would invert most of these
assumptions and is tracked separately as
[#000009](../workshop/issues/000009-cloud-scalable-vault-backend.md).

---

## Assets

| Asset | Storage | Sensitivity | Why |
|---|---|---|---|
| OAuth refresh tokens | macOS Keychain (`charon` service) | High | Long-lived, per provider × account, mints fresh access tokens until revoked at provider |
| Proxy CA private key | macOS Keychain (`charon` service, `_ca:key` account) | **Highest** | Owning it lets an attacker forge HTTPS for any host the agent trusts via `charon run`'s env vars |
| Access tokens | In-memory cache only | Low | Short-lived (~1h), never persisted |
| Charon signing key | macOS Keychain, login | High | Lets attacker produce Mach-O binaries that satisfy charon's M4 ACL predicate |

The CA private key is arguably more dangerous than OAuth tokens — its
blast radius isn't bounded by a single provider. Both get the same M4
ACL treatment.

---

## Adversaries

Listed by capability, lower → higher.

### A. The AI agent (or other unprivileged code) running as the user

The primary threat. Has shell access, can spawn subprocesses, can read
files in the user's home, can call any system API the user can. Cannot:
escalate to root, mount full-disk-access, attach a debugger to a signed
binary while SIP is enabled, install a system-wide kext.

This is what charon is designed to defeat.

### B. Code with TCC grants (FDA / Files and Folders)

Out of charon's scope as a defense target. If the user has explicitly
granted FDA to a hostile app, or an attacker has chained a TCC-bypass
exploit, the attacker can read raw `~/Library/Keychains/login.keychain-db`,
attempt offline brute-force of the keychain master key, and read
adjacent secrets (browser cookies, mail stores, etc.). Charon's M4 ACL
holds against the API path even with FDA, but the broader process
boundary doesn't.

### C. Local root / SIP-disabled adversary

Out of scope. An attacker with `sudo` or SIP disabled can attach a
debugger to charon, read decrypted secrets directly from process
memory, replace system binaries, or modify charon. We assume the user
hasn't given anyone this level of access.

### D. Off-machine adversaries

Out of scope. Network-level MITM, compromised CAs in the system trust
store, compromise of Google's identity providers, stolen unencrypted
backups. Some are partially mitigated by other layers (FileVault,
Mozilla CA root list, Google's security, encrypted Time Machine
backups) but charon doesn't add anything.

---

## Defense layers (what's built)

Listed in evaluation order — the topmost layer fires first.

### 1. Process isolation

The agent is a separate process from charon. Tokens never appear in the
agent's address space, environment, or filesystem. The agent's only
path to the credential is the local HTTPS proxy on `127.0.0.1:8230`.

Defeats: agents that read parent-process memory, env-var dumping,
file-system credential scraping.

Status: ✅ Inherent to charon's architecture.

### 2. Proxy mediation

Agent traffic to known API hosts (Google's googleapis.com family today)
is routed through charon, which attaches the bearer token. Agent
declares its intent via `X-Charon-Account` and `X-Charon-Scope`
headers; charon enforces both — wrong account → request fails, declared
scope not granted → 407 with structured error.

Defeats: agents that try to call the API directly without charon.
**Only when the agent is launched via `charon run`** — direct
invocation without `HTTPS_PROXY` set bypasses this entirely (see A2
below).

Status: ✅ Implemented; bypass via direct upstream connection
documented in A2.

### 3. Keychain ACL bound to charon's designated requirement

Each entry in the `charon` keychain namespace is written with a
`SecAccess` whose trusted-applications list pins to the running
process's codesign DR. Reads from any process not satisfying the DR
trigger an Allow/Deny dialog.

Defeats: agents that learn about Keychain APIs and call
`SecItemCopyMatching` directly, or shell out to `security
find-generic-password`.

Status: ✅ Implemented in M4. Uses legacy
`SecKeychainItemCreateFromContent` because modern `SecItemAdd`
silently drops `kSecAttrAccess` on macOS file-based keychains. ACL
inspection is verified by integration tests
(`TestACL_ActuallyAttachesACL`).

### 4. Codesign requirement format = identifier + signer

The ACL predicate is set automatically by
`SecTrustedApplicationCreateFromPath(NULL, ...)` at write time —
whatever DR the running charon binary has gets baked into the entry's
SecAccess. After [#000011](../workshop/issues/000011-apple-developer-id.md)
that's the team-id-anchored Apple form:

```
anchor apple generic and certificate leaf[subject.OU] = "<TEAMID>" and
    identifier "com.charon.cli"
```

For machines still on self-signed (no Dev ID enrolled), it's the
older `identifier "com.charon.cli" and certificate leaf = H"<sha1>"`
variant. Both are stable across `make install` re-runs (cdhash
changes; identifier + signer don't), and the team-id form is also
stable across the ~5-year Apple cert renewal window.

Defeats: an agent that builds its own Go binary with
`--identifier com.charon.cli` but isn't signed by the user's
authorized signer.

Status: ✅ Implemented. The Makefile auto-detects which signing
identity is available (`make print-sign-identity`) and signs
accordingly; the M4 ACL captures whichever DR the resulting binary
has.

### 5. Signing key requires user click for each use

The user's signing private key — `Charon Self-Signed` (legacy) or
`Developer ID Application: <Name> (<TEAMID>)` (current) — should have
no codesign-class apps in its trusted-applications list. Every
`make install` then triggers an Allow dialog from codesign. The user
must explicitly approve, single-use (NOT "Always Allow" — that
re-adds codesign to the trust list).

Defeats: agents that try to bypass the M4 ACL by signing their own
binary with the user's signing identity (i.e., disguising as charon).
Without a user click, codesign cannot use the key.

Status: ✅ Bootstrap script omits `-T /usr/bin/codesign`; users
warned in three places against clicking "Always Allow." `charon-security`
([#000012B](../workshop/issues/000012-audit-evolution.md)) now
programmatically audits the trusted-apps list, names each app, and
classifies catastrophic vs benign vs unknown — so drift surfaces in
every `make security` run rather than waiting for a manual visual
check.

For Dev ID-issued keys, Apple's Certificate Assistant adds itself
(plus a few vestigial daemons like `racoon`, `ServerManagerDaemon`)
to the trusted-apps list at key generation. These are recognized as
benign by the audit. The catastrophic case is `/usr/bin/codesign` or
`/usr/bin/security` ending up in the list — flagged Critical.

### 6. Runtime self-signature self-check

Charon evaluates its own binary's DR at startup via
`SecCodeCheckValidity` against a specific predicate. Linker-signed
dev binaries (`go build`, `go run`, `go test` defaults on Apple
Silicon) don't match → routed to a separate `charon-dev` keychain
namespace. Prevents accidental clobbering of prod state and Allow/Deny
prompt fatigue during dev iteration.

Status: ✅ Implemented in M3.

---

## Threat-by-threat enumeration

Tagged: ✅ defended, 🟡 partial, ❌ not defended, ⏸ deferred (with
plan), ⛔ out of scope.

### A1. AI agent reads tokens via Keychain Services API

An agent running as the user calls `SecItemCopyMatching` (or shells out
to `security find-generic-password -w`) to read charon's stored
refresh tokens.

✅ The M4 keychain ACL pops Allow/Deny. Agent has no way to satisfy the
predicate without the user's signing key.

### A2. AI agent bypasses charon and calls API directly

Drops `HTTPS_PROXY` from env, opens a raw TCP connection to
`gmail.googleapis.com:443`. With no credential of its own this fails,
but combined with any leaked token (whose lifetime is bounded by Google's
refresh-token rotation, see A6) the agent can reach upstream without
charon's audit log seeing it.

🟡 Partial. Charon doesn't enforce network policy. Mitigation requires
OS-level firewall rules (Little Snitch, Lulu, pf custom rules,
managed configuration profile). Charon could ship a sample pf rule
file but doesn't today.

### A3. AI agent sends scope-confused requests

Declares `X-Charon-Scope: gmail.readonly` while the actual API call
will write a Gmail draft. Charon's scope check is honest-self-reporting,
not API-method-aware.

🟡 Partial. Google enforces actual scope at their side and returns 403
on under-scope. Charon's pre-check is a courtesy that gives a faster
407. Building an API-method-to-scope map for every Google endpoint
isn't tractable.

### A4. Multi-account confused-deputy

User has two accounts in charon. Agent A is meant to act on
account-A's data but sets `X-Charon-Account: account-B@gmail.com`
and acts on B.

🟡 Partial. Charon requires the header in multi-account configurations
and errors when ambiguous, but doesn't authorize *who may set which
account*. That belongs in a higher layer (the orchestrator that
launches agents — i.e. nous, not charon). For single-account users
this isn't a risk.

### A5. AI agent injects code into running charon

`DYLD_INSERT_LIBRARIES`, `DYLD_FRAMEWORK_PATH`, `dlopen`, debugger
attach. Once injected, the attacker code runs as charon — DR matches,
ACL trivially satisfied.

✅ Defended. Both binaries sign with `--options runtime` and ship
zero weakening entitlements:

- `Charon Security.app` (audit bundle) — hardened from inception.
- `charon` proper (`~/.local/bin/charon`) — hardened as of
  2026-04-28 ([#000012 item G](../workshop/issues/000012-audit-evolution.md)).
  Verified functionally: `charon serve`, `charon accounts list`,
  and proxied requests all work under hardened-runtime defaults.

Defaults in effect: `DYLD_INSERT_LIBRARIES` blocked, debugger attach
gated by entitlement (we declare none), unsigned dylib loading
blocked, unsigned executable memory blocked. Charon needs none of
those weakening exemptions — pure Go (AOT) with CGo only against
Apple's Security framework.

### A6. Refresh token blast radius

A leaked refresh token grants months of access until revoked at
Google's side.

🟡 Partial. Google's refresh token rotation (each `Refresh()`
invalidates the prior refresh token) bounds the leak window to the
next legitimate refresh. Charon already persists rotated tokens.
Beyond that, only scope minimization helps — and that's the user's
responsibility (charon shows the granted scope set in the TUI).

### A7. AI agent persistence beachhead

Compromised agent installs a launchd plist or cron job to auto-run on
login. The user can audit, but most don't.

⛔ Out of charon's scope. Charon could verify its own service plist
points at the canonical binary, but a malicious agent's persistence
isn't impersonating charon.

### A8. Build-pipeline / supply-chain compromise

Malicious code in `keybase/go-keychain`, `cobra`, charm libraries,
or our own `internal/` packages. The signed binary legitimately satisfies
the ACL and exfiltrates tokens.

🟡 Partial. `make govulncheck` runs Go's official vulnerability
scanner against the module graph and reachable code. Catches known
CVEs in pinned dependencies; doesn't catch novel malicious packages
or pre-disclosure issues. Currently a manual / pre-merge step (no
CI yet). Module integrity still relies on `go.sum`.

### A9. Build-toolchain compromise

Compromised `clang` / `codesign` / `make`. The build environment is
implicated.

⛔ Out of scope. Trust the dev environment.

### A10. Signing key abuse via codesign

Agent shells out to `codesign --sign "Charon Self-Signed"
/tmp/agent-charon-impostor` (or the Dev ID variant) and gets a
Mach-O whose DR matches the M4 predicate.

✅ Defended (since 2026-04-27). Two layers:

1. **Static**: bootstrap script omits `-T /usr/bin/codesign`; signing
   key created with no codesign-class apps in trusted-applications
   list; every codesign use prompts. Click Allow (single-use), not
   Always Allow.
2. **Dynamic**: `charon-security` programmatically inspects the
   trusted-apps list ([#000012 items B and A](../workshop/issues/000012-audit-evolution.md))
   on every `make security` run. Catastrophic entries (codesign,
   security CLI) → Critical finding; unrecognized entries →
   Important; benign Apple defaults → silent.

The combination means the user is told to be careful AND verified
to have stayed careful, rather than relying on memory.

### A11. Charon's own bugs

Token logged to audit by mistake; routed to wrong upstream; bearer
header not stripped on outbound; deserialization bug in token
handling; etc.

🟡 Partial. Charon's audit log redacts tokens
(`internal/proxy/audit.go`); the proxy strips internal headers
(`X-Charon-Account`, `X-Charon-Scope`) before forwarding;
per-host routing tables limit blast radius. Defense is code review +
tests, no architectural backstop.

### B1. TCC-bypass exploit / FDA-granted hostile app

Out of scope as a primary defense target. Macros:

- The encrypted login keychain alone doesn't reveal secrets; needs
  master key from `securityd` memory.
- API-path access still gates on M4 ACL.
- Adjacent secrets (mail, Messages, browser cookies) leak — but those
  aren't charon's responsibility.

⛔ Out of scope as a defense.

🟡 Partially **detected** by `charon-security` from the other side:
the audit reads system TCC.db and surfaces any terminal/IDE that
holds FDA / Accessibility / Screen Recording / AppleEvents grants
(bar items 2–5). So while charon doesn't *prevent* an FDA-granted
hostile app from existing, it tells the user when their environment
contains the precondition. This is the most common practical path
to B1 — the user accidentally granted FDA to their terminal once
and forgot.

### C1. Stolen device or unencrypted backup

Time Machine backs up `~/Library/Keychains` by default. If the backup
isn't encrypted, an attacker can attempt offline brute-force of the
login keychain master key.

⛔ Out of scope. User must enable FileVault and encrypted backups.
Worth flagging in user-facing docs.

### C2. iCloud Keychain sync

Charon entries are explicitly opt-out via `kSecAttrSynchronizable: false`.
The signing cert + private key in login keychain don't sync to iCloud
Keychain by default (iCloud Keychain syncs specific item types).

✅ Defended for charon entries; signing key relies on Apple's default
behavior.

---

## Lifecycle considerations

### macOS sleep / lock

When the user is logged out or the screen is locked long enough,
`securityd` purges the keychain master key from memory. Even charon
itself can't read entries silently in that state — first access
prompts for keychain unlock.

Implication: charon as an unattended launchd daemon (e.g. for an
overnight automation run) won't auto-refresh tokens after sleep. Acceptable
trade-off; matches user expectation that "credentials require my unlocked
session."

### Stale binary running

A `charon serve` left running from a previous install (e.g. via
launchctl) can have a different DR than the current `~/.local/bin/charon`.
Surfaced earlier this session: a backgrounded refresh-write created
entries that lacked the M4 ACL.

🟡 Partial. Hygiene around `charon service` lifecycle. Not formally
enforced; we trust the user / launchd to be consistent. The diagnostic
sequence in [security-test-plan.md](security-test-plan.md) Test 4
addresses one instance.

### Re-install stability

`make install` rebuilds the binary; cdhash changes. The M4 ACL
predicate pins to identifier + cert leaf hash, both stable across
rebuilds. Empirically validated; covered by `Test 7` in the manual
test plan.

✅ Stable across rebuilds.

### Signing identity rotation

Any signing-identity change — self-signed cert regeneration, machine
move, or one-way self-signed → Dev ID transition — produces a new
DR and breaks the ACL on existing entries written by the old binary.

Recovery: delete the old entries (`security delete-generic-password
-s charon -a <account>`) and re-auth. The CA cert/key are
auto-regenerated on next `charon serve` start. The migration runbook
for the self-signed → Dev ID case is in
[#000011](../workshop/issues/000011-apple-developer-id.md).

### Apple Developer ID (current state)

Done in [#000011](../workshop/issues/000011-apple-developer-id.md).
Predicate is now
`anchor apple generic and certificate leaf[subject.OU] = "<TEAMID>" and identifier "com.charon.cli"`.

- Cert renewals (Apple issues new certs every ~5 years) don't break
  the predicate as long as the team ID stays stable.
- Hardened runtime + notarization become straightforward to enable
  per-binary. `Charon Security.app` already uses both (required on
  macOS 26+ for TCC). `charon` proper signs with Dev ID but doesn't
  yet enable hardened runtime — see A5 / future-work item #1.
- Revocation: Apple can revoke the Dev ID cert, breaking the
  signature chain for all binaries signed with it. No equivalent for
  self-signed.
- The Makefile auto-detects identity: prefers
  `Developer ID Application: ...` if present in the login keychain,
  falls back to `Charon Self-Signed`.

### macOS version baseline

The audit (`charon-security`) and TCC behavior depend on macOS
version:

- **macOS ≤ 25 (Sequoia and earlier)**: TCC honors FDA grants for
  Dev ID-signed bundles regardless of notarization. Self-signed
  bundles can also receive FDA after manual `spctl --add`.
- **macOS 26 (Tahoe)**: TCC enforcement tied to `spctl --assess`.
  Self-signed bundles fail spctl ("Unnotarized Developer ID" or
  "Rejected"); TCC silently denies FDA even if the System Settings
  toggle is on. The audit's TCC-read path needs a notarized
  Developer ID-signed `Charon Security.app` — provided by
  `make security-install` post-#000011. Without notarization,
  fall back to `make security ARGS=--no-tcc` (visual System
  Settings walk).

charon proper (`make install`) is unaffected by Tahoe's TCC tightening
because its security boundary (M4 keychain ACL) routes through
`securityd`, not `tccd`.

---

## Prioritized future work

### Done

- ✅ **Apple Developer ID** (2026-04-28).
  [#000011](../workshop/issues/000011-apple-developer-id.md).
  Team-id-anchored DR, notarized `Charon Security.app`, M4 ACL
  predicate stable across cert renewals.
- ✅ **User-facing security audit + remedy playbook**
  ([#000010](../workshop/issues/000010-security-audit-tool.md),
  [#000012 items B and A for the signing key](../workshop/issues/000012-audit-evolution.md)).
  `make security` walks the 9-item bar (SIP, terminal/IDE TCC,
  sudo cache, signing-key trust list, charon keychain ACLs, launchd
  persistence) with severity-tiered output and JSON support.
- ✅ **Hardened runtime on `charon` proper** (A5; 2026-04-28).
  [#000012 item G](../workshop/issues/000012-audit-evolution.md).
  `make install` signs with `--options runtime` + `--timestamp`;
  no weakening entitlements declared.
- ✅ **`govulncheck` integration** (A8; 2026-04-28). `make
  govulncheck` runs Go's official vulnerability scanner against the
  module graph + reachable code. Auto-installs the tool on first
  use. Currently zero reachable CVEs.

### Open

Roughly decreasing value-per-effort for a single-user dev tool:

1. **OS firewall rule sample** (A2). A pf rule (or Little Snitch
   profile) blocking outbound TLS to Google API hosts unless via
   localhost. Make it opt-in; document in README.

2. **Time Machine per-destination encryption check**. FileVault
   landed (bar 11); Time Machine destination encryption is still
   open. Per-destination logic via `tmutil destinationinfo -X` +
   `diskutil info` per destination — tracked in
   [#000012 item E](../workshop/issues/000012-audit-evolution.md).

3. ~~Audit naming for `charon` keychain entries' trusted apps~~ —
   landed 2026-04-28 ([#000012 item A](../workshop/issues/000012-audit-evolution.md)
   broader scope). Both signing-key and per-entry checks now name
   each trusted application and classify
   expected/benign/unknown/catastrophic.

4. **CI** (general). No CI exists today. When we add one,
   `make govulncheck` should be a required job.

5. **Predicate-form DR comparison** ([#000012 item F
   deeper](../workshop/issues/000012-audit-evolution.md), partial).
   The path-form drift check landed: if the installed binary path
   doesn't match what entries trust, the audit surfaces it. The
   predicate-form case (entries trust `identifier "com.charon.cli"
   and anchor apple generic and team = X`-style DRs rather than
   paths) is still TODO — uncommon in practice since
   `SecTrustedApplicationCreateFromPath(NULL, ...)` stores paths.

The keychain ACL boundary that #000010's test plan verifies — A1
and A10 — is the one that's unique to charon and was the reason
this work existed. The open items above are general
macOS-application-hygiene improvements that charon can adopt but
doesn't differentiate on.
