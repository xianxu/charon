---
id: 000011
status: working
deps: [000003, 000010]
github_issue:
created: 2026-04-28
updated: 2026-04-28
---

# Apple Developer ID transition

## Problem

Charon currently signs binaries with a self-signed identity
(`Charon Self-Signed`), bootstrapped per-machine by
`scripts/dev/setup-signing-identity.sh`. This works for the M4
keychain-ACL boundary (predicate matches by leaf cert hash, which is
stable for the local user) but hits hard limits with macOS's broader
trust system:

1. **TCC silently denies grants on macOS 26 (Tahoe).** Discovered
   during issue [#000010](000010-security-audit-tool.md) M4. Self-
   signed bundles fail `spctl --assess`. macOS 26 ties TCC enforcement
   to spctl — even when the user toggles Full Disk Access ON for a
   self-signed bundle, `os.Open(TCC.db)` returns `operation not
   permitted` at the kernel level. The System Settings UI lies about
   the grant being active. Workaround so far: `--no-tcc` visual mode.
2. **Hardened runtime + entitlements (A5 in threat model).** Not
   currently enforced. Without an Apple-anchored identity we can't
   meaningfully tighten this — the `DYLD_INSERT_LIBRARIES` blanket
   block depends on a stable trust root.
3. **Notarization.** Required for distribution beyond the developer's
   own machine. Self-signed binaries flunk Gatekeeper for new users
   on first launch.
4. **Revocation story.** Self-signed has none. With Dev ID, Apple can
   revoke a compromised cert chain.

## Goal

Replace the self-signed signing identity with an Apple Developer ID
cert. Migrate the M4 ACL predicate from leaf-cert-hash binding to a
team-id-anchored binding (`anchor apple generic and certificate
leaf[subject.OU] = "<TEAMID>"`), which survives Apple's ~5-year cert
renewals as long as the team ID stays stable.

## Spec

Bundles together:

- **Prerequisites**: Apple Developer Program membership ($99/year).
- **Signing**: `make install` and `make security-install` use the
  Dev ID Application cert in place of `Charon Self-Signed`.
- **Hardened runtime**: enable `--options runtime` with a minimal
  entitlements plist on `cmd/charon` (already on for
  `Charon Security.app`).
- **Notarization**: `notarytool submit` integrated into release
  flow. Out of scope for personal-machine use, but cheap once we
  have the cert.
- **M4 ACL predicate migration**: switch from
  `identifier "com.charon.cli" and certificate leaf = H"<sha1>"` to
  `anchor apple generic and certificate leaf[subject.OU] = "<TEAMID>"
  and identifier "com.charon.cli"`. Existing self-signed-era keychain
  entries become unreadable; user re-auths to migrate.
- **Self-signed fallback**: keep `setup-signing-identity.sh` as a dev/
  CI path. `make install` chooses the appropriate identity based on
  what's available (Dev ID preferred, self-signed fallback).

Background: most of this is sketched in
[`workshop/plans/000003-code-signing-keychain-acl-plan.md#apple-dev-id-upgrade-future-not-this-issue`](../plans/000003-code-signing-keychain-acl-plan.md#apple-dev-id-upgrade-future-not-this-issue).

## What this unblocks

- **#000010 M6** — auto-revoke flow with `tccutil`. Currently blocked
  because Tahoe TCC won't actually grant FDA to the self-signed
  bundle in the first place; nothing to revoke.
- **#000010 manual test plan (M9)** — currently can't validate the
  TCC read path on the author's machine.
- **Charon distribution beyond personal use.** Other users hitting
  Gatekeeper friction on first launch.
- **Threat model A5** — hardened runtime + entitlements becomes
  meaningful with a stable Apple-anchored trust root.

## Plan

Sketch (detailed plan when prerequisites in hand):

1. Sign up for Apple Developer Program. Generate a Developer ID
   Application certificate; export as a `.p12` and import into the
   build host's login keychain.
2. Add `APPLE_TEAM_ID` and `APPLE_DEV_ID_NAME` to `Makefile.local`
   (or `.env` — the team ID is sensitive-ish).
3. Update `internal/vault/keychain/codesign_darwin.go`'s DR predicate
   to the team-id-anchored variant.
4. Rebuild `make install`; re-auth all OAuth accounts (one-time cost).
5. Add `make notarize` for `Charon Security.app` distribution.
6. Validate on Tahoe: install, grant FDA, run `make security`, expect
   TCC reads to actually succeed.
7. Resume #000010 M6.

## Log

- 2026-04-28: Apple Developer Program renewed. Personal Apple ID (Xian Xu).
  Team ID `23GUFD3P3G`. Generated CSR via Keychain Access → Certificate
  Assistant; downloaded G2 Sub-CA Developer ID Application cert. Initially
  showed `CSSMERR_TP_NOT_TRUSTED` because the G2 intermediate
  (`DeveloperIDG2CA.cer`) wasn't installed; fetched from
  <https://www.apple.com/certificateauthority/>, double-clicked, chain
  validates. `security find-identity -v -p codesigning` returns the
  Dev ID identity.
- 2026-04-28: Wired auto-detect into `Makefile.local`. `SIGN_IDENTITY`
  picks the first `Developer ID Application` cert from the login keychain;
  falls back to `Charon Self-Signed` when absent. `make
  print-sign-identity` reveals which one is selected. No M4 DR predicate
  change needed: the ACL captures the running binary's DR via
  `SecTrustedApplicationCreateFromPath` at write time, so subsequent
  `make install` runs automatically write entries pinned to the new DR.

## Migration runbook

To pick up Dev ID signing on a machine with prior self-signed-era
keychain entries:

```bash
# 1. Confirm auto-detect found the Dev ID identity.
make print-sign-identity
# Expect: SIGN_IDENTITY = Developer ID Application: <Name> (<TEAMID>)

# 2. Re-sign + install charon.
make install
# Click "Allow" on the Keychain Access dialog for the Dev ID private
# key the first time. NEVER "Always Allow" — same A10 hygiene as the
# self-signed key.

# 3. Delete old self-signed-era charon keychain entries. The new
# binary's DR doesn't satisfy their existing ACLs, so writes would
# fail with errSecAuthFailed.
security delete-generic-password -s charon -a _ca:cert  2>/dev/null
security delete-generic-password -s charon -a _ca:key   2>/dev/null
# For each OAuth account currently registered:
charon accounts list                    # see what to delete
security delete-generic-password -s charon -a "google:<email>"

# 4. Regenerate CA + re-auth accounts.
charon serve &                          # writes new CA cert/key with new DR
sleep 1
kill %1
charon auth google <email>              # writes new token with new DR

# 5. Re-sign + reinstall the security audit bundle.
make security-uninstall                 # clean slate
make security-install                   # signs Charon Security.app with Dev ID
# In FDA pane: drag the new bundle in, toggle on. On Tahoe this should
# now actually grant FDA (the original blocker that motivated #11).
make security                           # verify TCC reads work
```

## Notes

- Cost: $99/year. Worth it given how much friction self-signed has
  caused so far.
- The ACL migration is destructive to existing keychain entries; user
  loses access to the old ones (correct behavior — the trust anchor
  changed, ACLs no longer satisfied). Document the re-auth step
  prominently.
- This issue is **not** about hardening charon's threat model — that
  stays the same. It's about working with macOS's trust system at a
  level that doesn't get silently rejected.
