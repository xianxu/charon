---
id: 000003
status: working
deps: [000001]
github_issue:
created: 2026-04-24
updated: 2026-04-26
---

# Code signing + Keychain ACL

## Problem

Currently charon stores credentials as generic passwords (Keychain tier 2) — any process running as the user can read them via `security find-generic-password`. This means an AI agent could theoretically exfiltrate tokens directly, bypassing the proxy.

## Goal

Sign the charon binary with an Apple Developer certificate and use Keychain ACL (tier 1) to restrict access to only the signed binary. macOS will prompt "Allow/Deny" if any other process tries to read charon's keychain entries.

## Spec

- Apple Developer ID certificate (requires Apple Developer account)
- `codesign --sign` the binary
- Store keychain entries with ACL binding to the signed app
- Notarization for distribution (optional)

## Notes

From vision doc: "This is the long-term play: Charon as a signed binary with exclusive Keychain access."

## Plan

Detailed design lives at [`workshop/plans/000003-code-signing-keychain-acl-plan.md`](../plans/000003-code-signing-keychain-acl-plan.md).

Approach: ship self-signed code signing now (free, OSS-friendly), Apple Dev ID later as
a transparent upgrade. Single binary, CGo on darwin, runtime self-check picks the
keychain service name based on the binary's own signing state.

High-level milestones:

- [x] M1 — Self-signing identity bootstrap (`scripts/dev/setup-signing-identity.sh`)
- [x] M2 — CGo keychain backend (Security framework) replaces `security` CLI shelling on darwin
- [x] M3 — Runtime self-signature check + dev/prod service-name split (`charon` vs `charon-dev`)
- [x] M4 — ACL on `Set` for OAuth tokens + `_ca:cert` + `_ca:key`
- [ ] M5 — Generic ACL migration (`charon migrate-acl`) + first-run auto-migrate of CA *(user-deferred; revoke + re-auth path used instead)*
- [x] M6 — `make install` signs the binary; README documents the bootstrap flow
- [ ] M7 — Manual test: unsigned reader gets Allow/Deny dialog; signed charon does not

## Log

- 2026-04-26: **Critical fix** — M4's SecItemAdd-with-kSecAttrAccess path was
  silently dropping the SecAccess on macOS file-based keychains. SecItemAdd
  accepted the attribute without error, but inspection showed entries had
  zero ACL entries (`security find-generic-password ... -g` had no
  `Access:` line). The earlier "Test 1 demo" Allow/Deny dialog the user saw
  was probably macOS's first-time-access prompt for an unACL'd entry, NOT
  our ACL working — meaning the security boundary M4 was supposed to build
  wasn't actually enforced.

  Switched the create path from `SecItemAdd(attrDict)` to atomic
  `SecKeychainItemCreateFromContent(itemClass, attrList, data, keychain,
  initialAccess, ...)` — same legacy file-keychain family as our delete
  fallback, takes the SecAccess as an explicit parameter, attaches it
  reliably. The update path uses `SecKeychainItemModifyContent` (also
  legacy) for parity. Tried `SecKeychainAddGenericPassword +
  SecKeychainItemSetAccess` first; SetAccess prompts for owner-ACL
  authorization (returns -128 errSecUserCanceledAuthentication in
  non-interactive contexts) because SecAccessCreate's default owner ACL
  is "always prompt." The atomic create-with-access avoids the issue
  entirely.

  Added a `charon_inspect_generic_password` CGo helper +
  `inspectGenericPasswordACL` Go wrapper that reports ACL entry count
  and total trusted-application count. New integration test
  `TestACL_ActuallyAttachesACL` asserts a fresh ACL'd write produces
  >0 ACL entries — would have caught this bug. New test
  `TestACL_AtomicUpsert_PreservesACL` asserts updates don't change the
  ACL shape (token rotation safety).

  Verified end-to-end: signed binary writes entry → 5 ACL entries, 1
  trusted app. Previous code: 0 ACL entries, 0 trusted apps.

- 2026-04-26: Fixed TUI revoke path treating Google's HTTP 400
  (`error=invalid_token`, the response shape for already-revoked tokens)
  as a fatal error and skipping the local `vault.Delete`. Surfaced when
  user revoked an account from Google's side first (via
  myaccount.google.com/permissions), then tried `charon auth` → revoke
  in the TUI: HTTP 400 → no local cleanup → entry stuck. Fix: defined
  `oauth.ErrAlreadyRevoked` sentinel; `oauth.Revoke` parses Google's
  standard OAuth error envelope and returns the sentinel for
  `error=invalid_token` at HTTP 400. TUI revoke handler treats sentinel
  as non-fatal and proceeds to `vault.Delete`, with an exit note that
  honestly captures "already revoked on Google's side."
- 2026-04-26: Fixed `vault delete` returning errSecInvalidOwnerEdit (-25244)
  on entries created by another process. The keybase-library path uses
  modern `SecItemDelete`, which trips -25244 when the item's internal
  access object isn't owned by the current process — even for items with
  no explicit ACL. Replaced with a new CGo helper that tries
  `SecItemDelete` first and falls back to the legacy
  `SecKeychainFindGenericPassword` + `SecKeychainItemDelete` pair (same
  path the `security` CLI uses) on -25244. Verified by reproducing the
  failure with a `security add-generic-password`-created entry and
  confirming the new path deletes it cleanly. Side-effect: closes one
  of the M4 review's known gaps (test cleanup robustness across
  DR-mismatched runs) since `aclCleanup` now uses the same fallback
  path. Note: TUI revoke flow at `internal/tui/model.go:217` calls
  vault.Delete and was previously broken when Google's revoke endpoint
  itself returned 400 (token already revoked) — that's a separate UX
  bug (TUI bails before deleting local entry); not addressed here.
- 2026-04-26: M6 review (post-milestone, fresh-eyes subagent) returned no
  Critical findings; four follow-ups applied: dropped the silencing `@`
  prefix on the codesign + verify commands so failures surface their
  diagnostic output; corrected the README claim that the macOS Keychain
  Access dialog appears on "first `charon` run" — it actually fires
  during the first `make install` for `codesign` to use the private key;
  bumped the README Status entry for #000003 from `🔜` to `🚧` with a
  one-liner about what's shipped today; fixed an unrelated typo in the
  trio-overview blockquote (left in place from a pre-session user edit).
- 2026-04-26: M6 done. `make install` is now `build → sign → install` in one
  shot: new `make sign` target codesigns `bin/charon` with `Charon Self-Signed`
  + `--identifier com.charon.cli`, verifies, then the `install` target copies
  to `~/.local/bin/charon`. Errors out with a clear message if
  `make signing-identity` hasn't been run. Manually verified end-to-end:
  `make install` → installed binary correctly resolves to ServiceProd, writes
  ACL'd entries (read attempt from `security` CLI prompts Allow/Deny;
  click Allow → token visible; click Deny → access denied). README rewritten:
  new "Installation" section as the canonical entry point (one-time
  signing-identity bootstrap + `make install`), updated "Security model"
  with the ACL story, dropped the misleading "pure Go, no CGo" claim
  from the build-from-source section. M5 (migration command) deferred per
  user direction — the revoke-and-re-auth path covers existing
  pre-M4 entries; M5 reopens when an Apple Dev ID transition happens.

  Surfaced during manual verification: existing pre-M4 OAuth entries
  (e.g. xianxu@gmail.com) are still in the keychain *without* an ACL,
  because they predate M4 and SecItemUpdate only swaps data, not the
  ACL attribute. The user has agreed to revoke + re-auth those rather
  than migrate. The CA cert+key may also be pre-M4 if the user ran an
  unsigned binary previously; deleting `_ca:cert` / `_ca:key` and
  letting charon regenerate them is the cleanest path. Documented in
  M7 manual checklist.
- 2026-04-26: M4 review (post-milestone, fresh-eyes subagent) returned no
  Critical findings; CFRelease lifecycle and atomic-upsert path verified
  clean. Two Important + three Minor follow-ups; addressed:
  - Comment at the SecItemUpdate fall-through clarifying that
    DR-mismatched existing entries surface auth-failure intentionally
    (we don't silently re-create entries someone else owns); operator
    workaround documented inline.
  - Comment in the SecItemAdd attr block noting kSecAttrSynchronizable
    is intentionally not in the update path (we own the namespace; attrs
    are write-once at create time).
  - Atlas updated (`atlas/charon.md`): new "Keychain Service Namespace +
    ACL" section captures the runtime service-name resolution, the
    ACL-via-DR design, and the load-bearing observed-not-spec behavior
    that SecItemUpdate preserves the SecAccess attribute.
  Two known gaps logged here (non-blocking, M5/M7 follow-ups):
  - **Test branch-coverage gap**: `TestACL_AtomicUpsert` writes twice
    and verifies the second value is read back, but cannot fail-fast
    if the SecItemUpdate path silently regresses to delete-then-add.
    Light introspection (e.g. branch counter via cgo) would close the
    gap; deferred.
  - **Cleanup-across-runs**: integration tests use a dedicated
    `charon-acl-test` service, but if a previous test run left an
    ACL'd entry whose DR doesn't match the next test process, both
    write (SecItemUpdate auth-failure) and cleanup (SecItemDelete
    auth-failure) break. Operator workaround:
    `security delete-generic-password -s charon-acl-test -a <account>`.
- 2026-04-26: M4 done. New `acl_darwin.go` adds direct-CGo `setGenericPassword`
  helper that does atomic upsert via SecItemUpdate (preserves ACL across
  rotation) → SecItemAdd-with-fresh-ACL fallback when the entry doesn't
  exist yet. Both `Store.Set` and `kv.SetRaw` route through it, gated by
  service name: ServiceProd → ACL bound to current process's designated
  requirement (any other reader gets the macOS Allow/Deny dialog);
  ServiceDev → no ACL (dev iteration writes from many ephemeral binaries
  with non-matching DRs, so an ACL would lock dev out of its own state).
  Three integration tests added (`TestACL_WriteAndReadBack`,
  `TestACL_AtomicUpsert`, `TestACL_NoACLPath`) under a dedicated
  `charon-acl-test` service so they don't pollute real prod entries.
  Used the legacy `SecTrustedApplicationCreateFromPath(NULL,...) +
  SecAccessCreate` API — formally deprecated since 10.10 but the only
  path that gives codesign-DR-based ACL on file-based keychains; modern
  `SecAccessControlCreateWithFlags` is for biometric/passcode gating, a
  different use case. Suppressed deprecation warnings via cgo
  `-Wno-deprecated-declarations`.
- 2026-04-26: M3 review (post-milestone, fresh-eyes subagent) returned no
  Critical/Important findings; race-detector clean. One minor follow-up
  applied: warning comment on `signatureCheck` against `t.Parallel()` in
  overriding tests (no mutex; production-safe via init order; parallel
  tests would race).
- 2026-04-26: M3 done. New `service.go` + `codesign_darwin.go` resolve the
  keychain service-name namespace at runtime. Signed binary (`make install`,
  identifier `com.charon.cli`) → `charon`; everything else (linker-signed
  `go build`/`go run`/`go test`, ad-hoc with different identifier) →
  `charon-dev`. Empirically verified across signing states. The Store snapshot
  the resolved name at `New()` time so a mid-process signatureCheck flip can't
  silently re-route operations. `internal/proxy/cert.go` (CA storage) also
  routes through `ResolveServiceName()`, so dev binary's CA lives separately
  from the signed install's CA. Three unit tests added with an injectable
  `signatureCheck` (no dependency on the test binary's actual signature).
  Discovery: Go on Apple Silicon emits *linker-signed* binaries by default,
  so a naive `SecCodeCheckValidity(...,nil)` returns true even for `go build`
  output — defeating the dev/prod split. Tightened to require a specific
  designated requirement (`identifier "com.charon.cli"`) via
  `SecRequirementCreateWithString`. The check is non-strict on purpose:
  identifier-only — the M4 keychain ACL pins to the specific cert leaf and
  is the actual security boundary; M3 only does namespace selection.
- 2026-04-26: M2 review (post-milestone, fresh-eyes subagent) returned no
  Critical/Important findings. Two minor items applied: (a) added a comment
  to `kv_darwin.go` GetRaw explaining the intentional no-TrimSpace asymmetry
  with the CLI counterpart (Security framework returns exact bytes; CLI's
  `-w` adds a newline so the CLI version trims); (b) noted in the plan that
  M4 should switch `Set` to `SecItemUpdate` for atomic upsert — needed once
  entries have ACLs since the M2 delete-then-add window would briefly drop
  the ACL. Approve verdict from reviewer. The third minor (no default-run
  cgo backend test) is mitigated by integration tests covering the same
  surface; not addressed.
- 2026-04-26: M2 done. New `internal/vault/keychain/keychain_darwin.go` +
  `kv_darwin.go` implement the Store interface via direct macOS Security
  framework calls (using `github.com/keybase/go-keychain`); the legacy
  `security` CLI shell-out is preserved under `//go:build !darwin || !cgo`
  for hermetic CI / non-darwin builds. Shared types (`serviceName`,
  `keyName`, `storedCredential`) factored into `common.go`. M2 deliberately
  does not write keychain ACLs — that lands in M4. All 5 keychain
  integration tests pass under the new backend (Set/Get/Delete/List/Overwrite
  parity); CGO_ENABLED=0 darwin and Linux cross-compile both green; full
  test suite still green. Discovered during this work: `vault.Store`
  lacks `context.Context` and the service name is hardcoded — both fine
  for local Keychain but blockers for any future cloud-secrets backend.
  Captured as #000009 ("cloud-scalable vault backend + multi-user
  readiness"); not blocking #000003.
- 2026-04-26: M1 review (post-milestone, fresh-eyes subagent) returned no Critical
  findings, three Important/Minor follow-ups applied: (a) plan updated to reflect
  the actual M1 implementation (no `set-key-partition-list`, no `add-trusted-cert`,
  `find-identity` without filters), preventing future milestones from re-litigating
  those choices; (b) corrected SHA-256 → SHA-1 in plan's designated-requirement
  spec, verified empirically against `codesign -dr-` output (40-hex-char identity
  hash matches the auto-generated `H"..."` value); (c) trimmed the inaccurate
  "repairs partial state" claim from the script header. Approve verdict from
  reviewer.
- 2026-04-26: M1 done. `scripts/dev/setup-signing-identity.sh` + `make signing-identity`
  generate and import a self-signed code-signing identity (`Charon Self-Signed`,
  10-year RSA-4096) into the user's login keychain. Verified by codesigning a test
  binary; the auto-generated designated requirement is
  `identifier "com.X" and certificate leaf = H"<sha1>"` — exactly the predicate
  shape M4's ACL will reuse. Several finicky details documented inline:
  OpenSSL 3.x requires `-legacy` p12 export; `find-identity` (without `-v`) is the
  right verification path because `-v -p codesigning` filters to trusted-only and
  hides untrusted self-signed identities; `set-key-partition-list` is deprecated
  and brittle — we rely instead on the standard one-time "Always Allow" Keychain
  Access dialog the user clicks during the first `make install`.
- 2026-04-26: Brainstorm with user — landed on self-signed first, Apple Dev ID later.
  Discovered constraint: keychain ACLs gate on the requesting process's signature, so
  shelling out to `/usr/bin/security` defeats the model. Resolution: switch the
  darwin keychain backend to direct Security framework calls via CGo. Single binary
  preserved; runtime self-signature check switches keychain service name (`charon`
  for signed, `charon-dev` for unsigned) so dev iteration doesn't trip Allow/Deny
  prompts on prod entries. Designated requirement pinned to self-signed cert
  identity (CN + leaf fingerprint); the same migration command handles future
  Apple Dev ID transition by re-writing ACLs under a team-anchored predicate.
