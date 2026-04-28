---
id: 000010
status: working
deps: [000003]
github_issue:
created: 2026-04-27
updated: 2026-04-27
---

# Security audit tool — `make security` / `make security-remedy`

## Problem

Charon's threat model ([`docs/threat-model.md`](../../docs/threat-model.md))
is built on three environmental assumptions:

1. The agent / agent-launching shell has no admin password
2. No granted TCC permissions (FDA, Accessibility, Screen Recording,
   AppleEvents) on the launching context
3. SIP enabled

If any of these don't hold, the M4 keychain ACL boundary still gates the
API path, but adversary classes B (TCC-grants) and C (root/SIP) come into
scope and the broader process boundary collapses. Today this is a doc-only
expectation; users have no way to verify their environment matches.

A common failure mode: `tccutil` accumulates grants over years
("Terminal needs FDA for some script", "VS Code needs Accessibility for
a plugin") that the user has long forgotten. macOS never prompts to
revisit them.

## Goal

Ship a `charon-security` tool that audits a personal Mac for the hygiene
baseline charon's threat model assumes, and prints actionable remediation
steps. Targets developers running agentic-coding workflows, not just
charon users — but lives in charon since charon is the privacy-sensitive
piece of the stack.

## Spec

### Checks performed

| ID | Check | Severity | FDA needed |
|---|---|---|---|
| `sip` | `csrutil status` is `enabled` | Critical | No |
| `sudo` | `sudo -nv` cache state | Info | No |
| `tcc-fda` | Which apps have Full Disk Access; flag terminals/IDEs | Critical | Yes |
| `tcc-a11y` | Which apps have Accessibility; flag terminals/IDEs | Critical | Yes |
| `tcc-screen` | Which apps have Screen Recording; flag terminals/IDEs | Important | Yes |
| `tcc-events` | AppleEvents grants between apps | Important | Yes |
| `launchd` | Enumerate `~/Library/LaunchAgents` + system-scope plists | Info | No |
| `codesign-terms` | Hardened-runtime / weakening entitlements on detected terminals | Hygiene | No |
| `charon-acl` | Charon signing-key ACL trusted-applications list is empty (charon-specific) | Critical | No |
| `charon-entries` | Charon keychain entries have non-empty ACL pinning to charon DR | Critical | No |

Severity tiers:
- **Critical** — direct compromise of charon's threat-model assumptions
- **Important** — meaningful weakening but not catastrophic alone
- **Info** — informational, user judgment required
- **Hygiene** — general macOS-app-hygiene, not charon-specific

### Known terminal/IDE bundle IDs (initial list)

`com.apple.Terminal`, `com.googlecode.iterm2`, `com.mitchellh.ghostty`,
`dev.warp.Warp-Stable`, `co.zeit.hyper`, `org.alacritty`,
`com.github.wez.wezterm`, `net.kovidgoyal.kitty`, `org.tabby`,
`com.cmux.cmux`, `com.microsoft.VSCode`, `com.todesktop.230313mzl4w4u92` (Cursor),
`com.exafunction.windsurf`, `dev.zed.Zed`, `com.sublimetext.4`,
JetBrains family (`com.jetbrains.*`), `com.panic.Nova`, `com.apple.dt.Xcode`.

Hardcoded `var KnownTerminals` slice; `--apps-extra` flag for runtime
additions. List ages annually-ish; we maintain it in `internal/security/apps/known.go`.

### Pre-flight transparency block

Every run, before privileged ops:

```
Charon Security Audit v<version>
Binary SHA256: <hash>
Source: cmd/charon-security/, internal/security/

This tool will:
  ✓ Read /Library/Application Support/com.apple.TCC/TCC.db (requires FDA)
  ✓ Read ~/Library/Application Support/com.apple.TCC/TCC.db (requires FDA)
  ✓ Run `csrutil status`, `sudo -nv` (read-only)
  ✓ Enumerate ~/Library/LaunchAgents, /Library/LaunchAgents,
    /Library/LaunchDaemons (read-only)
  ✓ Run `codesign -d --entitlements -` on detected terminals (read-only)

This tool will NOT:
  ✗ Modify any TCC grants without explicit confirmation
  ✗ Make network requests
  ✗ Persist data outside this run

Continue? [y/N]
```

Default-deny on the gate. Auto-revoke prompt at the end defaults yes.

### TCC attribution: must run as a `.app` bundle

Critical packaging detail: TCC attributes permissions to the *responsible
code*. A bare Mach-O run from Terminal gets attributed to `com.apple.Terminal`,
not to our binary — so granting "FDA to charon-security" via System Settings
would actually grant FDA to Terminal, and the auto-revoke step would nuke
Terminal's FDA.

Fix: ship `Charon Security.app`, a tiny app bundle with bundle ID
`com.charon.security`. TCC attributes to the bundle. Auto-revoke targets
exactly the bundle ID.

```
~/Applications/Charon Security.app/
  Contents/
    Info.plist          # CFBundleIdentifier=com.charon.security
    MacOS/
      charon-security   # Go binary (signed with same Charon Self-Signed cert)
```

`make security` builds + installs the .app to `~/Applications/`, then
launches the inner binary directly (so output goes to terminal, not a GUI
window). User-scope install, no admin needed.

### Output format

- Default: colorized text, severity-tagged
  - Red: Critical findings
  - Yellow: Important findings
  - Cyan: Info
  - Dim: Hygiene
- `--no-color` for pipe-friendly output
- `--json` for structured (CI integration)
- `--remedy` long-form prose per finding (otherwise: one-line summary +
  finding ID; `charon-security remedy <id>` for details)

### Exit codes

- `0` — no findings or only Info/Hygiene
- `1` — at least one Important
- `2` — at least one Critical
- `--strict` — Hygiene → Important, Important → Critical (collapses to fail-fast)

### Auto-revoke flow

After audit:
```
Audit complete. <N> findings (<C> critical, <I> important).

This tool currently has Full Disk Access (TCC: com.charon.security).
Revoke now? [Y/n]
  → tccutil reset SystemPolicyAllFiles com.charon.security
  ✓ FDA revoked.
```

If user declines, prints the exact `tccutil` command for later use.

### Visual-mode fallback

`charon-security check --no-tcc` skips TCC.db reads entirely; runs only
SIP/sudo/launchd/codesign checks, then opens System Settings panes for
visual audit:

```
$ charon-security check --no-tcc
[automated checks output]

For TCC grants, open these panes manually and verify no terminals/IDEs
are listed:
  → System Settings → Privacy & Security → Full Disk Access
  → System Settings → Privacy & Security → Accessibility
  → System Settings → Privacy & Security → Screen Recording
  → System Settings → Privacy & Security → Automation

[Enter] to open Full Disk Access pane...
```

This mode is the safe default for users who don't want to grant FDA to
the audit tool. Tradeoff: no automated terminal-grant detection.

## Plan

Detailed design lives at [`workshop/plans/000010-security-audit-tool-plan.md`](../plans/000010-security-audit-tool-plan.md)
*(to be written after issue approval)*.

High-level milestones:

- [x] **M1** — Skeleton CLI (`cmd/charon-security/`) with cobra subcommands
  `check`, `remedy`. Pre-flight transparency block. Visual-mode fallback
  works end-to-end.
- [x] **M2** — Privilege-free checks: `sip`, `sudo`, `launchd`,
  `codesign-terms`, `installed-apps` detection.
- [x] **M3** — `.app` bundle packaging + `make security-install` /
  `make security-uninstall`. Signed with `Charon Self-Signed`.
- [x] **M4** — TCC.db reader (sqlite, parse `access` table). Per-app
  grant report scoped to known-terminals list.
- [x] **M5** — Charon-specific checks: charon keychain entries' ACL
  non-empty with proper DR. *(Signing-key ACL inspection deferred —
  requires CGo to walk a non-charon-namespace cert/key item; manual
  Keychain Access verification covered by the remedy text instead.)*
- [ ] **M6** — Auto-revoke flow with `tccutil reset` + verification.
  *(Blocked on [#000011](000011-apple-developer-id.md): Tahoe TCC
  won't grant FDA to self-signed bundles, so there's nothing to
  revoke. Resume after Apple Developer ID transition.)*
- [ ] **M7** — Severity tiers, exit codes, `--json` output, colorization.
- [x] **M8** — Remediation prose for each finding ID. `make security-remedy`
  prints the playbook. *(Reordered ahead of M3 per user — more value
  immediately than packaging.)*
- [ ] **M9** — Manual test plan: run on clean Mac, dirty Mac (intentionally
  granted Terminal FDA), verify findings + auto-revoke roundtrip.
- [ ] **M10** — README section + atlas entry. Link from `docs/threat-model.md`
  "Prioritized future work" item #4.

## Open questions

- Should we also detect Homebrew-installed shells (`bash`, `zsh`, `fish`
  from `/opt/homebrew`) that bypass macOS's signed system shells? Adjacent
  hygiene topic. **Decision: defer**, scope creep.
- AppleEvents grants are pairwise (app A → can-control → app B). Reporting
  format for these is awkward. **Decision: list as "<src> can drive <dst>"
  rows; flag any row where src is a terminal/IDE and dst is a credential
  app (Keychain Access, 1Password, Bitwarden).**
- Should this be released as a standalone repo for non-charon users? It's
  generally useful for any agentic-coding setup. **Decision: ship inside
  charon for now (per user); revisit extracting to its own repo if
  external interest materializes.**

## Notes

- Bundle ID `com.charon.security` distinct from `com.charon.cli` so TCC
  scope is unambiguous and revoking one doesn't affect the other.
- Tool intentionally does not auto-run any remediation other than
  revoking its own FDA. `tccutil reset All` is destructive to legitimate
  user grants (Zoom mic, etc.); user must paste consciously.
- Hardened-runtime check on terminals is an early warning of A5-class
  injection vectors (`DYLD_INSERT_LIBRARIES`) being viable against shells
  that load user code.

## Log

- 2026-04-27: Issue created. Design discussed in conversation:
  `.app`-bundle requirement settled (CLI-attribution gotcha), pre-flight
  consent default-deny, auto-revoke default-yes, colorized text default
  output, severity tiers Critical/Important/Info/Hygiene.
- 2026-04-27: M1 landed. `cmd/charon-security/` builds; `check`/`remedy`
  subcommands wired; `--no-tcc` visual-walk fallback works on a tty,
  cleanly skipped under `--yes` or non-interactive shells. Unit tests
  on severity rollup. End-to-end smoke test passes; full `go test ./...`
  green.
- 2026-04-27: M2 landed. Five privilege-free checks: SIP, sudo cache,
  third-party launchd plists, codesign weakening entitlements,
  installed-app detection (mdfind primary, `/Applications` fallback).
  Live run on author's machine surfaced 2 real findings (launchd
  third-party plists, VS Code weakening entitlements) — no false
  positives on SIP/sudo/known apps. Curated KnownApps list seeded
  with ~25 entries (terminals/editors/JetBrains).
- 2026-04-27: Detection bug fixes — Info.plist regex was XML-only,
  silently skipping binary plists (Warp, WezTerm). Switched to
  PlistBuddy. Also added /System/Applications/Utilities to scan dirs
  (Apple's Terminal/Console live there; Spotlight didn't index).
  Detection now finds 5/5 expected apps. Audit summary lists the
  detected apps explicitly so user can verify coverage.
- 2026-04-27: M8 landed (reordered ahead of M3 per user — remedies
  unlock immediate value). `internal/security/remedy.go` curates 10
  RemedyEntry records: 4 for live M2 checks (sip/sudo/launchd/codesign),
  4 for TCC family (M4 will fill in matching findings; prose stable),
  2 for charon-specific keychain ACL (M5). `charon-security remedy`
  prints all; `remedy <ref>` prints one; unknown ref lists valid refs
  and exits 1. Audit summary now appends "→ details: charon-security
  remedy <ref>" hint per finding so users don't have to dig. Tests
  cover lookup, completeness, and check-emitted refs all having
  matching remedies (catches future drift).
- 2026-04-27: Markdown rendering — remedy bodies are markdown source,
  rendered through charmbracelet/glamour for terminal output. Fenced
  code blocks for command snippets, ordered lists for procedures,
  bold for imperative verbs. Width auto-detects from TTY (capped 100
  cols). `--no-color` → glamour "ascii" style for pipes/CI.
- 2026-04-27: M3 landed. `scripts/dev/build-security-app.sh`
  assembles `Charon Security.app` at `~/Applications/`, signed with
  `Charon Self-Signed`, hardened runtime on, no entitlements.
  Bundle ID `com.charon.security` distinct from `com.charon.cli`.
  Makefile targets: `security-build`, `security-install`, `security`
  (= install + run check), `security-remedy`, `security-uninstall`.
  Idempotent re-installs (bundle ID + leaf-cert pair stable across
  rebuilds; existing TCC grants survive).
- 2026-04-27: M4 landed. `internal/security/check_tcc.go` reads both
  user- and system-scope TCC.db via `/usr/bin/sqlite3 -readonly -json`
  (no Go-SQLite dep — schema is stable, query is one-shot, macOS
  ships sqlite3). `evaluateTCCRows` is pure; tests cover the severity
  matrix end-to-end (FDA/A11y on terminal=Critical, ScreenCapture=Important,
  AppleEvents to credential apps=Critical, others=Important, plus
  filtered cases: denied, path-based, unknown, empty target). When
  the DB can't be opened due to missing FDA, surfaces an Info finding
  pointing at the remedy + visual-mode fallback rather than failing.
  Curated CredentialApps map (Keychain Access, 1Password, Bitwarden,
  Dashlane, LastPass) drives the AppleEvents severity bump.
- 2026-04-28: **macOS 26 (Tahoe) limitation discovered.** Self-signed
  bundles fail `spctl --assess` and TCC silently denies FDA grants
  even with the System Settings toggle ON. `os.Open(TCC.db)` returns
  `operation not permitted` regardless of toggle state. `sudo spctl
  --add` may help but unverified. **Punted.** Proper fix is the
  Apple Developer ID transition (threat-model future-work item #5).
  Until then, on macOS 26+, users should run with `--no-tcc` for the
  visual System Settings walk. M4 is functional and tested; the
  blocker is environmental, not code-level.
- 2026-04-28: M5 landed. `internal/security/check_charon.go` reuses
  the existing CGo helper `inspectGenericPasswordACL` (added a public
  wrapper `Store.InspectACL`). Inspects BOTH `charon` and
  `charon-dev` namespaces — the audit tool's own bundle ID
  (`com.charon.security`) doesn't satisfy ResolveServiceName's
  prod-DR predicate, so it would default to the wrong namespace
  otherwise. Added `keychain.NewWithService(service)` constructor for
  this. Severity matrix per entry: (0,0)=Critical, (>0,1)=healthy,
  (>0,N>1)=Important. Live run on author's machine: 0 findings under
  `charon` (entries healthy), 0 under `charon-dev` (empty). Signing-
  key ACL check deferred to a future iteration; remedy text already
  walks users through manual Keychain Access verification.
