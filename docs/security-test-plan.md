# Charon security test plan

Verifies the keychain ACL boundary built in
[#000003](../workshop/issues/000003-code-signing-keychain-acl.md). Run
this end-to-end after any change touching the keychain backend, the
codesign pipeline, or the runtime self-signature check.

**What this confirms**: a process that is *not* the canonical
`make install`-signed charon binary cannot silently read OAuth refresh
tokens or the proxy CA private key. macOS surfaces an Allow/Deny dialog
to the user. The signed charon binary itself reads its own entries
without any prompt.

For the structured analysis of what charon defends against, what it
explicitly doesn't, and the ranked list of open weaknesses, see
[threat-model.md](threat-model.md). This test plan only verifies the
M4 keychain ACL boundary — one defense layer among several.

**What this does NOT confirm**: that an attacker with arbitrary code
execution as your user can't escalate. The keychain ACL is one layer;
it doesn't defeat e.g. a malicious binary the user clicks "Always Allow"
on, full-disk-access TCC bypasses, or compromise of the charon binary
itself.

A specifically important hygiene point: when prompted by macOS during
`make install` ("codesign wants to use a key…"), **always click Allow,
never Always Allow**. Always Allow adds codesign to the signing key's
trusted-applications list, after which any process running as you can
sign a Mach-O with the charon identity and silently satisfy the M4
keychain ACL — fully bypassing the boundary this doc verifies.

---

## Prerequisites

- macOS with file-based login keychain (default).
- One-time bootstrap done:
  ```bash
  make signing-identity     # creates "Charon Self-Signed" cert (10y) in login keychain
  ```
- `make install` produces a signed binary at `~/.local/bin/charon`.
- Read [the Installation section of the README](../README.md#installation)
  for context on what's running in your environment.

---

## How to use this doc

Each test is a numbered section with:
- **Setup**: prep commands.
- **Run**: the commands you actually execute.
- **Expect**: what success looks like.
- **Pass criteria**: the single observable that means "yes, this works."

If any test fails, stop and capture the unexpected output before moving
on. Failures here indicate the security boundary isn't holding the way
the docs claim.

A consolidated **Cleanup** section at the end removes test artifacts.

---

## 1. OAuth credential gets an ACL on fresh write

### Setup

```bash
make install                                                          # latest signed binary
~/.local/bin/charon vault delete --provider google --account testuser@example.com 2>/dev/null
```

### Run

```bash
~/.local/bin/charon vault set --provider google --account testuser@example.com --token test-12345
security find-generic-password -s charon -a "google:testuser@example.com" 2>&1 | grep cdat
```

### Expect

`Stored token for google/testuser@example.com` from the set command, and
a `cdat` line whose decoded timestamp is within ~5 seconds of now.

### Pass criteria

The cdat is fresh, and the entry exists under service `charon`
(not `charon-dev`).

---

## 2. External read of an ACL'd credential triggers Allow/Deny

### Setup

Test 1 just ran; a fresh entry exists.

### Run

```bash
security find-generic-password -s charon -a "google:testuser@example.com" -w
```

### Expect

A macOS keychain dialog pops up: *"security wants to use a key in your
keychain. Enter password to allow this."* (Wording varies slightly by
macOS version.)

Click **Deny**. The command should error out (`security: …authorization
denied…` or similar non-zero exit).

### Pass criteria

The dialog appears AND clicking Deny prevents the password from being
printed. If the password prints with no dialog at all, the ACL isn't
attached and the boundary is broken.

---

## 3. Charon-side read of its own entry is silent

### Run

```bash
~/.local/bin/charon accounts
```

### Expect

```
google / testuser@example.com
```

No dialog, no prompt. (May see a one-time "Always Allow / Allow / Deny"
dialog on the very first run after a fresh `make install` — macOS's
first-time-access UI for a new cdhash. Click **Always Allow**; subsequent
runs are silent. This is independent of the ACL — the OS just confirms
"yes, this newly built binary may use the previously trusted DR.")

### Pass criteria

After at most one one-time confirmation, repeated `charon accounts`
invocations succeed silently.

---

## 4. CA cert + key get the same ACL treatment

This is the most consequential test — the CA private key is the highest-
value secret charon manages.

### Setup

Stop any running charon proxy and wipe the existing CA pair so
`charon serve` regenerates them via the post-fix code path:

```bash
charon service stop 2>/dev/null    # ignore error if not installed
pkill -f 'charon serve' 2>/dev/null

security delete-generic-password -s charon -a "_ca:cert" 2>/dev/null
security delete-generic-password -s charon -a "_ca:key"  2>/dev/null
```

### Run

```bash
~/.local/bin/charon serve &
SERVE_PID=$!
sleep 2

# CA cert and key should now exist, fresh:
security find-generic-password -s charon -a "_ca:cert" 2>&1 | grep cdat
security find-generic-password -s charon -a "_ca:key"  2>&1 | grep cdat

# External read of the CA private key should pop Allow/Deny:
security find-generic-password -s charon -a "_ca:key" -w
# (Click DENY.)

kill "$SERVE_PID"
```

### Expect

- Both cdat lines are within ~5s of now (fresh regeneration).
- The `-w` read of `_ca:key` triggers a dialog. Deny → command errors.

### Pass criteria

Same as Test 2 but for the CA private key. If the dialog doesn't appear,
the CA private key is exfiltratable — STOP and report.

---

## 5. Token rotation preserves the ACL

The proxy refreshes access tokens and writes back rotated refresh tokens
via `vault.Set`. Test 1 already proved fresh writes get an ACL. Here we
verify that a *second* write to the same key — i.e., the
`SecKeychainItemModifyContent` (update) branch — doesn't drop the ACL.

### Setup

The test 1 entry still exists.

### Run

```bash
# Overwrite with new data
~/.local/bin/charon vault set --provider google --account testuser@example.com --token rotated-67890

# External read should still trigger Allow/Deny
security find-generic-password -s charon -a "google:testuser@example.com" -w
# (Click Deny — confirms ACL still gating.)

# Charon-side read should still be silent
~/.local/bin/charon accounts
```

### Expect

Dialog appears for external read; charon-side read is silent.

### Pass criteria

Same gating behavior as Test 2 / 3 after the update. If the external
read suddenly succeeds without prompting, the update path silently
dropped the ACL — a regression of the M2 review concern.

---

## 6. Dev/prod namespace isolation

Unsigned binaries (`make build`, `go build`, `go run`, `go test`) must
write to a separate `charon-dev` namespace, not pollute prod state.

### Setup

```bash
go build -o /tmp/charon-unsigned ./cmd/charon
```

### Run

```bash
# Write a credential via the unsigned binary
/tmp/charon-unsigned vault set --provider google --account devtest@example.com --token dev-only

# Should land in charon-dev, not charon
security find-generic-password -s charon-dev -a "google:devtest@example.com" 2>&1 | grep cdat
security find-generic-password -s charon     -a "google:devtest@example.com" 2>&1 | head -1
# First should print a fresh cdat. Second should say "could not be found".

# Signed binary's view of accounts should NOT include devtest
~/.local/bin/charon accounts
# Should NOT include devtest@example.com.

# Cleanup
/tmp/charon-unsigned vault delete --provider google --account devtest@example.com
rm /tmp/charon-unsigned
```

### Expect

The dev write is in `charon-dev` only, invisible to the signed binary's
`accounts` enumeration.

### Pass criteria

`security find-generic-password -s charon -a "google:devtest@example.com"`
returns "could not be found." Without this, dev iteration would either
clobber prod credentials or trip Allow/Deny dialogs constantly.

---

## 7. Re-install stability (DR-pinned, not cdhash-pinned)

`make install` rebuilds the binary; cdhash changes every build. The ACL
predicate pins to *codesign identifier + cert leaf hash*, not cdhash.
Re-installing should let charon read its own previously-written entries.

### Setup

Test 1's entry still exists, ACL'd to the previous build's DR.

### Run

```bash
# Force a rebuild + re-sign + re-install
touch cmd/charon/main.go
make install
codesign -dv ~/.local/bin/charon 2>&1 | grep -E "Identifier|Authority"
# Expect: Identifier=com.charon.cli, Authority=Charon Self-Signed

# Read entries written before this rebuild
~/.local/bin/charon accounts
# Should include testuser@example.com without prompting (or with at most
# one-time first-access confirmation; click Always Allow).
```

### Expect

The newly built binary reads entries silently after at most one
first-access confirmation. The ACL didn't break despite the new cdhash.

### Pass criteria

`charon accounts` succeeds. If reads now prompt every time, the ACL is
pinned too tightly (e.g. by cdhash) and `make install` is broken.

---

## Cleanup

After all tests pass:

```bash
~/.local/bin/charon vault delete --provider google --account testuser@example.com 2>/dev/null
~/.local/bin/charon vault delete --provider google --account devtest@example.com 2>/dev/null
pkill -f 'charon serve' 2>/dev/null
```

The CA pair (`_ca:cert`, `_ca:key`) should NOT be cleaned up — they're
load-bearing for any agent currently running through the proxy. Leave
the freshly regenerated, ACL'd pair in place.

---

## Reporting results

Paste the consolidated output (which test passed, any anomalies) into
the issue's `## Log` section, e.g.:

```markdown
- 2026-MM-DD: M7 manual run — all 7 tests pass. Notes:
  - Test 3: saw one-time first-access dialog as expected; clicked
    Always Allow.
  - Test 5: dialog still fired after the update; ACL preserved.
  - Test 7: rebuild + re-install read previously-written entry without
    re-prompting.
```

Then mark M7 complete in the issue's `## Plan` and move the issue to
`workshop/history/` per the standard workflow.
