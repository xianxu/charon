---
id: 000012
status: open
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

### A. Enumerate trusted applications by name in keychain entries

`internal/security/check_charon.go` currently returns
`(aclCount, appCount)` per entry and flags `(>0, N>1)` as Important
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

### C. Path-based TCC grants

`evaluateTCCRows` currently filters out `client_type == 1` (path-
based grants) entirely. Most legitimate path-based grants are for
`/usr/bin/git`, `/usr/local/bin/zoom`, etc. — not what we audit.

But certain paths *are* worth flagging:

- `/usr/bin/security` with FDA/Accessibility → should be impossible
  but worth confirming
- `/opt/homebrew/bin/<shell>` with FDA → user installed a custom
  shell with broad permissions
- Anything under `~/Downloads` or `/private/tmp` with TCC grants →
  highly suspicious

Curate a small allow / deny list of path patterns and surface
findings for matches.

### D. AppleEvents pairwise grant reporting

M4 partially does this — flags terminals that can drive credential
apps (Keychain Access, 1Password, etc.) — but doesn't surface the
broader graph of "who can drive whom". A full audit would render:

```
Automation grants:
  com.cmuxterm.app → can drive → com.apple.systemevents
                                  com.googlecode.iterm2
                                  ...
```

so the user sees the whole pairwise relationship, not just the
flagged subset. Probably over-noisy at full enumeration; gate
behind `--verbose` or a separate `audit-automation` subcommand.

### E. FileVault + encrypted Time Machine status

The threat-model's future-work item #4 calls these out as "user
responsibilities" not currently checked. Easy adds:

- `fdesetup status` → FileVault on/off
- `tmutil destinationinfo` + `tmutil hasbackupfor /` parsed for
  encryption metadata → Time Machine encrypted

Both are privilege-free reads. Hygiene-tier findings if off.

### F. Charon binary self-attestation

Audit could verify charon's installed binary actually matches what
keychain entries are pinned to. Specifically:

- Read `~/.local/bin/charon`'s codesign DR via `codesign -dr -`
- Compare against the DR in any keychain entry's ACL
- Drift = "an old binary signed entries that the current binary
  can't read; user needs to re-auth"

Surfaces the stale-daemon-overwriting-with-old-DR pattern that
motivated `_dev` namespace splitting in [#000003](000003-code-signing-keychain-acl.md).

### G. Hardened-runtime check for charon proper

Charon CLI is signed but doesn't currently apply `--options runtime`.
Audit could check and warn. Not strictly required for keychain ACL,
but defense in depth (A5 in threat model).

### H. Per-detected-terminal entitlement details

The `codesign-weak-*` finding currently reports "weakening
entitlements present" and lists them. But not all weakening
entitlements are equally bad — `allow-jit` is fine for an Electron
app but worrying for a shell-like tool. Differentiate severity by
entitlement type, possibly with a per-app override (some apps need
specific entitlements).

### I. Ongoing KnownApps maintenance

Apps in the curated list age annually-ish. New terminals appear
(Wave, Kiln, etc.); bundle IDs occasionally change (Cursor's bundle
ID is currently a ToDesktop-internal UUID). Periodic refresh via
`mdfind 'kMDItemKind == "Application"'` + manual review.

### J. Cross-machine comparison / baseline

For users with multiple Macs (work/personal), a JSON export of
audit findings + a `compare` subcommand → spot drift between
machines. Probably out of scope for personal-use; flagged for
completeness.

---

## Priority order (rough)

Roughly decreasing value-per-effort:

1. **(A)** Enumerate trusted apps by name in keychain entries —
   concrete user ask, catches the A10-class drift that count-only
   checks gloss over. **Higher priority now that (B) is landed but
   coarse-grained**: B counts but can't say whether the trusted apps
   are catastrophic (codesign) or benign (Apple system services).
2. ~~(B) Programmatic signing-key ACL inspection~~ — landed 2026-04-28.
3. **(F)** Charon binary self-attestation — catches the stale-daemon class of bug that motivated dev/prod split.
4. **(E)** FileVault + Time Machine encryption status — closes the user-checklist gap from threat-model item #4.
5. Everything else as motivation arrives.

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
