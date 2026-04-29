---
id: 000012
status: done
deps: [000010]
github_issue:
created: 2026-04-28
updated: 2026-04-28
---

# charon-security audit evolution

Umbrella ticket for ongoing improvements to the security audit tool
shipped in [#000010](000010-security-audit-tool.md). Each subsection
is an independent enhancement — pick them up as motivation /
prioritization warrants. Close items as they ship; close this issue
when the list is empty or stale.

The base tool is solid: privilege-free system checks, TCC enumeration
on detected terminals/IDEs, charon-keychain-ACL inspection, remedy
playbook rendered through glamour. What follows is *deeper* coverage —
catching subtler drift, naming things the count-based checks only
flag.

---

## Backlog

### A. Enumerate trusted applications by name in keychain entries ✅ landed 2026-04-28 (signing-key + charon-entries)

Both the signing-key check and the charon-namespace entry check
now extract per-trusted-app DR strings via the CGo path
(`charon_inspect_key_acl_by_label` / `charon_inspect_generic_password`,
both with optional `out_drs` parameter). Go-side classifier verdicts
each DR as expected/benign/unknown/catastrophic; severity rolls up
from the worst entry. See `classifyTrustedAppsForEntry` and
`classifyOneFor` in `internal/security/check_charon.go`.

---

(historical context for posterity:)

`internal/security/check_charon.go` originally returned
`(aclCount, appCount)` per entry and flagged `(>0, N>1)` as Important
without naming the extra apps. The user has to open Keychain Access
manually to see what's in the trusted list.

Desired: walk the SecAccess for each entry and emit per-trusted-app
detail in the finding:

```
[IMPORTANT] charon-entries-acl-many-charon/google:foo@gmail.com
    Trusted apps:
      - identifier "com.charon.cli" and anchor apple generic ... ✓ legitimate
      - identifier "com.apple.security"                          ⚠ should not be here
```

CGo path:

```c
SecKeychainItemCopyAccess(item, &access);
SecAccessCopyACLList(access, &aclList);
// for each ACL:
SecACLCopyContents(acl, &trustedApps, ...);
// for each trusted app:
SecTrustedApplicationCopyDesignatedRequirement(app, &req);
SecRequirementCopyString(req, kSecCSDefaultFlags, &drText);
```

~50 lines of CGo extending `internal/vault/keychain/acl_darwin.go`.
Pure Go side parses the DR strings and matches against an allow-list
("legitimate" = matches charon's expected predicate; everything else
= drift).

**Why this matters**: catches the post-"Always Allow" drift directly.
Today's `appCount > 1` finding is a weak signal because it doesn't
say *which* app is the extra. With names, the user can see whether
it's `/usr/bin/security` (the catastrophic A10 case) or some
other app they actually intended to add.

### B. Programmatic signing-key ACL inspection ✅ landed 2026-04-28

`internal/vault/keychain/acl_darwin.go` adds `InspectSigningKeyACL`
which finds an identity by certificate Common Name (the cert label
and key label diverge often; matching by CN via SecIdentity is
robust to that), follows `SecIdentityCopyPrivateKey` to get the
private key, and inspects its `SecAccess` ACL list. Returns the
same `(aclCount, appCount)` shape as the generic-password
inspector.

`internal/security/check_charon.go` adds `CheckCharonSigningKeyACL`
which discovers charon-relevant identities via
`security find-identity` (no policy filter — self-signed certs
need to be checked too even though they fail codesigning policy
validation), inspects each, and emits findings.

Coarse-grained: counts trusted apps but doesn't name them. Severity
is **Important** (not Critical) because Apple's Certificate
Assistant default state for Dev ID Application keys is ~4 trusted
apps (Keychain Access, SecurityAgent, etc.) — usually benign. The
catastrophic A10 case (codesign in the list) requires a Critical,
but distinguishing the two requires (A) below.

The remedy text walks the user through manually inspecting in
Keychain Access to determine whether the trusted apps include
codesign (catastrophic) or only Apple system services (probably
benign).

### C. Path-based TCC grants ✅ landed 2026-04-28

`evaluateTCCRows` previously skipped `client_type == 1` entirely.
Now path-based rows route through `evaluatePathBasedRow`:

- **DangerousTCCPaths** (curated map: `/usr/bin/security`,
  `/usr/bin/codesign`, shells, interpreters, `osascript`,
  Homebrew shell variants) → Critical for FDA/A11y/Events,
  always Critical for `security` and `codesign` regardless of
  service. Findings tag the appropriate bar (2/3/4/5).
- **suspiciousPathPrefixes** (`/private/tmp/`, `/tmp/`,
  `/private/var/folders/`) → Important. Catches downloaded /
  build-output binaries that asked for permissions.
- **Other paths** silent — `/usr/bin/git`, `/usr/local/bin/zoom`,
  etc. are noisy and legitimate.

Bar labels updated to "no terminal/IDE or dangerous path has X"
to reflect the unified scope.

### D. AppleEvents pairwise grant reporting — ⛔ wontfix

The dangerous case (terminal/IDE → credential app like Keychain
Access / 1Password / Bitwarden) is already flagged Critical by the
existing AppleEvents check via `CredentialApps`. A full pairwise
graph would add visibility but no security guarantee — it'd just
be a noisier inventory. Open a new issue if a real use case
materializes.

### E. FileVault + encrypted Time Machine status ✅ landed 2026-04-28 (both)

`internal/security/check_filevault.go` parses `diskutil info /` and
emits an Important finding when FileVault is off. Bar item 11.
Switched from `fdesetup status` to `diskutil info /` because Tahoe's
fdesetup errors with "Unknown volume or device specifier: '/'".

`internal/security/check_timemachine.go` parses `tmutil
destinationinfo`, then for each Local destination calls
`diskutil info <mountPoint>` to check `Encrypted: Yes` / `FileVault:
Yes`. Bar item 12. Severity matrix:

- No TM destinations configured → silent (you don't have a TM
  backup to worry about; bar passes).
- Local destination, encrypted → silent.
- Local destination, unencrypted → Important (C1 backup angle).
- Local destination, encryption status unknown → Info.
- Network destination (AFP/SMB) → Info ("verify manually; remote
  encryption isn't programmatically observable").

### F. Charon binary self-attestation ✅ landed 2026-04-28 (first pass + DR-vs-entry path comparison)

`internal/security/check_charon_binary.go` scans
`~/.local/bin/charon` via `codesign -dvv` and emits findings for:

- binary not installed at expected path → Info
- binary unsigned / signature invalid → Critical
- binary signed but `Identifier=` is not `com.charon.cli` → Critical
- binary signed but lacks the hardened-runtime flag → Important

This is bar item 10. Closes the most common stale-binary surface.

**Path-form comparison landed** under #12F deeper. After (A) broader
extracted per-entry trusted-app DRs, `driftFindings` extracts paths
from those DRs (when they're path-shaped, which is the common
case — Apple's `SecTrustedApplicationCreateFromPath(NULL, ...)`
stores the path). If the installed binary's path
(`~/.local/bin/charon`) doesn't appear in any entry's expected
trust list, surfaces an Important drift finding naming the
mismatched paths.

**Still TODO** — predicate-form comparison (`identifier
"com.charon.cli" and anchor apple generic and team = X`-style DRs).
Less common in practice (path-form is what charon writes today),
and predicate equivalence is hard to compute exactly. Defer until
needed.

### G. Hardened-runtime check for charon proper ✅ landed 2026-04-28

`make install` now signs with `--options runtime --timestamp`. No
weakening entitlements declared. Verified functionally on the
author's machine: `charon serve`, `charon accounts list`, and
proxied requests all work under hardened-runtime defaults.

The audit doesn't yet *check* that the installed charon binary is
hardened (the Make target enforces it for fresh installs, but a
user could have an old binary). Adding that check is a small
follow-on under (F) — read `codesign -d ... | grep flags` on
`~/.local/bin/charon` and warn if `runtime` is missing.

### H. Per-detected-terminal entitlement details — ⛔ wontfix

`codesign-weak-*` findings are uniformly Hygiene today — hidden
from the per-finding text output, available via `--json` for
consumers who want them. Severity differentiation per entitlement
type would be marginal: the audit already correctly de-prioritizes
this class of finding (third-party app's entitlement choices are
not actionable by the user beyond "pick a different app"). Reopen
if a specific entitlement+app combination warrants a Critical
warning.

### I. Ongoing KnownApps maintenance — ⛔ wontfix as a tracked item

Not a one-shot. Add bundle IDs to
`internal/security/knownapps.go` as new terminals/IDEs become
relevant; the existing `--apps-extra` runtime override handles
ad-hoc additions without code changes. No issue to track; merge as
encountered.

### J. Cross-machine comparison / baseline — ⛔ wontfix

Niche use case (multiple Macs to keep in sync). The existing
`--json` output already supports the diff use case manually
(`make security --json | jq` on each machine, `diff` the results).
Reopen if motivation appears.

---

## Priority order (rough)

Roughly decreasing value-per-effort:

1. ~~(A) Enumerate trusted apps by name~~ — landed 2026-04-28
   (signing-key path, then extended to charon-namespace keychain
   entries — full scope done).
2. ~~(B) Programmatic signing-key ACL inspection~~ — landed 2026-04-28.
3. ~~(G) Hardened runtime on charon proper~~ — landed 2026-04-28.
4. ~~(F) Charon binary self-attestation~~ — first pass landed
   2026-04-28 (identifier + hardened-runtime + signed checks);
   path-form DR-vs-keychain-entry drift check landed same day.
   Predicate-form comparison still open but rare in practice.
5. ~~(E) FileVault + encrypted Time Machine~~ — both landed 2026-04-28.
6. (D), (H), (I), (J) — wontfix (see per-section notes).

## Status

**Done 2026-04-28.** All security-relevant items (A, B, C, E, F,
G) landed; remaining (D, H, I, J) marked wontfix as their security
value would be marginal over what's already in place. The audit
covers a 12-item bar that maps directly to the threat model's
"reasonable bar" checklist; no adversary status in the threat
model would change with further #12 work.

## Notes

- Each item should land as its own commit/milestone within this
  issue. Resist the urge to bundle.
- Update `docs/security-audit-test-plan.md` for any check that
  emits new finding IDs (test plan should mirror the live findings
  surface).
- Update `internal/security/remedy.go` whenever a check emits a new
  `RemedyRef` — the
  `TestFindingRefsHaveRemedies` test enforces this; running tests
  catches drift on commit.
