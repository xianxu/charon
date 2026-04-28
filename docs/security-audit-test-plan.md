# Charon-security audit test plan

Verifies the audit tool built in
[#000010](../workshop/issues/000010-security-audit-tool.md). Run
end-to-end after any change touching `cmd/charon-security/`,
`internal/security/`, or the bundle assembly script.

**What this confirms**: each check produces the expected severity
and finding shape against known good/bad system states; severity
rollup drives the right exit code; output formats (text + JSON +
visual walk) are correct.

**What this does NOT confirm**: that the tool catches *every*
possible misconfiguration. Coverage is bounded by the curated
KnownApps list, the curated CredentialApps map, and the four
well-known TCC services we audit (FDA, Accessibility, Screen
Recording, AppleEvents). New attack surfaces require new checks.

For the analysis of what charon defends against, see
[`threat-model.md`](threat-model.md). For the keychain-ACL boundary
test plan that #000003 produced, see [`security-test-plan.md`](security-test-plan.md).

---

## Setup

Each test assumes:

- Working tree clean, on a recent commit.
- `make signing-identity` has already been run (Charon Self-Signed
  identity exists in login keychain).
- Running on macOS (any version supported by charon).

**Note on macOS 26 (Tahoe)**: TCC silently denies FDA grants to
self-signed bundles, so the FDA-required tests (T6) won't pass on
Tahoe until #000011 (Apple Developer ID) lands. Use `--no-tcc` for
visual-mode coverage on Tahoe.

---

## T1 — Privilege-free checks run on a clean shell

```
make security-build
./bin/charon-security check --yes --no-tcc
```

**Expect**:
- Pre-flight transparency block prints (binary path, sha256, source
  pointers, "will / will not" sections).
- "(--yes specified, skipping consent gate)" line.
- "Detected N known terminals/editors/IDEs:" with a per-app list.
- Audit summary line with counts.
- Per-finding lines with severity tag, ID, title, Affects, remedy
  hint.

**Success**: at least SIP and sudo checks appear in the run path
(no Critical findings unless your machine genuinely has them);
launchd findings only for non-Apple/non-Homebrew plists; exit code
0 (or 1 for any Important finding).

---

## T2 — Detection covers installed terminals/IDEs

```
ls /Applications | grep -E '\.app$' | head
./bin/charon-security check --yes --no-tcc 2>&1 | grep "(terminal\|editor\|IDE)"
```

**Expect**: every terminal/editor/IDE in `/Applications/`,
`/Applications/Utilities/`, `/System/Applications/`,
`/System/Applications/Utilities/`, and `~/Applications/` whose
bundle ID is in `internal/security/knownapps.go`'s `KnownApps`
list appears in the detected list.

**Success**: nothing missing; bundle ID and path columns line up.

**Common gotcha**: macOS `Terminal.app` lives in
`/System/Applications/Utilities/`, not the top-level Applications.
The audit explicitly scans there.

---

## T3 — Charon keychain entries inspected (silent on healthy)

Setup: charon has been used at least once on this machine — there's
a CA cert/key plus at least one OAuth account.

```
./bin/charon-security check --yes --no-tcc
```

**Expect**: no `charon-entries-acl-*` findings at any severity. The
M5 check inspects the entries silently when their state is healthy
(`aclCount > 0, appCount == 1`).

**Success**: silent. To verify the inspection actually ran, look at
the raw keychain:

```
security dump-keychain | grep -B1 'svce.*"charon"' | head
```

The presence of entries here while the audit is silent confirms
they were inspected and passed.

**Failure mode to test deliberately**: write an entry without an
ACL using the legacy fallback (no CGo path) and re-run. The audit
should emit `[CRITICAL] charon-entries-acl-missing-charon/...`.

---

## T4 — Severity-driven exit codes

```
./bin/charon-security check --yes --no-tcc ; echo "exit: $?"
```

**Expect** (depending on host state):
- Exit `0`: only Info / Hygiene findings.
- Exit `1`: at least one Important.
- Exit `2`: at least one Critical.

```
./bin/charon-security check --yes --no-tcc --strict ; echo "exit: $?"
```

**Expect**: every severity bumped up one tier before rollup. A
Hygiene finding with `--strict` becomes Info → still exits 0; an
Info → Important → exit 1; an Important → Critical → exit 2.

**Success**: exit codes match the highest severity present per the
table above.

---

## T5 — `--no-tcc` visual walk

```
./bin/charon-security check --no-tcc
```

(No `--yes`; this needs interactive input.)

**Expect**: after the privilege-free checks, the tool walks five
System Settings panes one at a time: FDA, Accessibility, Screen
Recording, Automation, Files and Folders. For each, it prints
"look for: ..." and prompts `[Y/n]` to open the pane. Pressing
Enter opens the pane via `open
"x-apple.systempreferences:..."`; pressing `n` skips.

**Success**: each pane opens correctly; the walk completes; "Manual
audit complete." prints at the end.

---

## T6 — TCC.db read (FDA path)

**Skip on macOS 26+** until #000011 lands. Self-signed bundles
can't get FDA on Tahoe.

On supported macOS (≤ 25):

Setup:
```
make security-install
# Drag ~/Applications/Charon\ Security.app into System Settings
# → Privacy & Security → Full Disk Access; toggle ON.
```

Then:
```
make security
```

**Expect**: no `tcc-no-fda-*` Info findings. If any of the detected
terminals/editors/IDEs have FDA, A11y, ScreenCapture, or AppleEvents
grants, they surface as findings with the appropriate severity.

**Failure mode to test deliberately**:
```
# Grant Full Disk Access to Terminal.app, then re-run.
```
Expect `[CRITICAL] tcc-fda-com.apple.Terminal — Terminal has Full
Disk Access` in the output.

---

## T7 — `--json` output is well-formed

```
./bin/charon-security check --yes --no-tcc --json | jq .summary
./bin/charon-security check --yes --no-tcc --json | jq '.findings[].severity'
```

**Expect**:
- `summary` shape: `{total, by_severity: {critical, important, info, hygiene}, exit_code}`.
- Severity values are uppercase strings (`"CRITICAL"`, etc.).
- `findings` is an array; each entry has `id`, `severity`, `title`,
  optional `detail`, `remedy_ref`, `affects`.
- `jq` doesn't error on the output.

**Success**: pipes to `jq` cleanly; CI consumers can drive on
`summary.exit_code` or `summary.by_severity.critical > 0`.

---

## T8 — Remedy lookups

```
./bin/charon-security remedy            # full playbook
./bin/charon-security remedy sip        # one entry
./bin/charon-security remedy not-a-ref  # error path
```

**Expect**:
- Full playbook: 10 entries, header announces total, each entry
  carries `[N/10]` position.
- Single entry: heading, Why, Fix, See also sections; rendered
  through glamour with markdown formatting (code blocks shaded,
  bold for emphasis).
- Unknown ref: lists valid refs sorted alphabetically; exit code 1.

**Success**: rendering looks like `glow` output in your terminal;
each finding emitted by the live audit has a matching remedy entry
(unit test `TestFindingRefsHaveRemedies` enforces this).

---

## T9 — Bundle re-install is idempotent

```
make security-install        # signs once, may prompt for keychain Allow
make security-install        # second run should print:
                             #   "bundle unchanged and signature valid — skipping re-sign"
make security-install        # ditto
```

**Expect**: only the FIRST run triggers the keychain Allow prompt
and re-signs. Subsequent runs short-circuit on the cmp check.

**Success**: the cdhash printed by `make security-install` stays
constant across runs (preserved across the script's idempotent
short-circuit). Important because TCC binds grants to cdhash —
silent re-signs would invalidate user-granted FDA.

---

## T10 — Uninstall cleans everything

```
make security-uninstall
ls ~/Applications/                # no Charon Security.app
ls bin/.security-bundle.stamp     # no such file
```

**Expect**:
- The `.app` bundle removed from `~/Applications/`.
- The Make stamp removed.
- `tccutil reset` ran for SystemPolicyAllFiles, Accessibility,
  ScreenCapture, AppleEvents scoped to `com.charon.security`
  (silent if no grants existed).

**Success**: no traces of the tool on the system; a subsequent
`make security` correctly says "Charon Security.app not found".
