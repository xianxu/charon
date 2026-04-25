---
id: 000003
status: open
deps: [000001]
github_issue:
created: 2026-04-24
updated: 2026-04-24
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

- [ ] Set up Apple Developer signing identity
- [ ] Add `codesign` step to Makefile
- [ ] Update keychain backend to use ACL-bound entries (`security add-generic-password -T`)
- [ ] Test that unsigned processes get "Allow/Deny" dialog
- [ ] Document the signing process
