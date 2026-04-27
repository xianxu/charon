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
- [ ] M4 — ACL on `Set` for OAuth tokens + `_ca:cert` + `_ca:key`
- [ ] M5 — Generic ACL migration (`charon migrate-acl`) + first-run auto-migrate of CA
- [ ] M6 — `make install` signs the binary; README documents the bootstrap flow
- [ ] M7 — Manual test: unsigned reader gets Allow/Deny dialog; signed charon does not

## Log

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
