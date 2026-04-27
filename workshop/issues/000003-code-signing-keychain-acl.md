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

- [ ] M1 — Self-signing identity bootstrap (`scripts/dev/setup-signing-identity.sh`)
- [ ] M2 — CGo keychain backend (Security framework) replaces `security` CLI shelling on darwin
- [ ] M3 — Runtime self-signature check + dev/prod service-name split (`charon` vs `charon-dev`)
- [ ] M4 — ACL on `Set` for OAuth tokens + `_ca:cert` + `_ca:key`
- [ ] M5 — Generic ACL migration (`charon migrate-acl`) + first-run auto-migrate of CA
- [ ] M6 — `make install` signs the binary; README documents the bootstrap flow
- [ ] M7 — Manual test: unsigned reader gets Allow/Deny dialog; signed charon does not

## Log

- 2026-04-26: Brainstorm with user — landed on self-signed first, Apple Dev ID later.
  Discovered constraint: keychain ACLs gate on the requesting process's signature, so
  shelling out to `/usr/bin/security` defeats the model. Resolution: switch the
  darwin keychain backend to direct Security framework calls via CGo. Single binary
  preserved; runtime self-signature check switches keychain service name (`charon`
  for signed, `charon-dev` for unsigned) so dev iteration doesn't trip Allow/Deny
  prompts on prod entries. Designated requirement pinned to self-signed cert
  identity (CN + leaf fingerprint); the same migration command handles future
  Apple Dev ID transition by re-writing ACLs under a team-anchored predicate.
