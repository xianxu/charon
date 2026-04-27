---
issue: 000010
created: 2026-04-27
updated: 2026-04-27
---

# Security Audit Tool — Detailed Plan

Companion to [`workshop/issues/000010-security-audit-tool.md`](../issues/000010-security-audit-tool.md).

## Repo layout

```
cmd/charon-security/
  main.go                   # cobra root: `charon-security check|remedy`
internal/security/
  preflight.go              # transparency block, consent gate, --no-color/--json wiring
  output.go                 # severity tiers, color, JSON serialization
  bundle.go                 # detect own .app bundle ID, locate own binary path
  checks/
    sip.go                  # csrutil status
    sudo.go                 # sudo -nv (read-only cache check)
    launchd.go              # ~/Library/LaunchAgents enumeration
    apps.go                 # detect installed terminals/IDEs (filesystem + mdfind)
    codesign.go             # codesign -d --entitlements - per terminal app
    tcc.go                  # SQLite read of TCC.db, joined against known-apps
    charon_acl.go           # signing-key trusted-apps list, charon entries' ACLs
  knownapps/
    known.go                # bundle-ID list with friendly names
  remedy/
    text.go                 # per-finding-ID prose
build/
  CharonSecurity.app/        # assembled by make, gitignored
    Contents/Info.plist
    Contents/MacOS/charon-security
scripts/dev/
  build-security-app.sh     # idempotent .app assembly + signing
docs/
  security-audit-test-plan.md
atlas/
  security-audit.md
```

Existing `cmd/charon/` (main `charon` binary) untouched.

## Dependencies

- **SQLite reader for TCC.db**: use `modernc.org/sqlite` (pure Go) rather
  than `mattn/go-sqlite3` (CGo). Charon already uses CGo on darwin for
  keychain, but a pure-Go SQLite simplifies the build matrix and is
  read-only — TCC.db reads are not perf-sensitive.
- **Color**: `github.com/charmbracelet/lipgloss` already in go.sum.
  Reuse rather than add a new color lib.
- **Cobra**: already used by `cmd/charon`.
- **tty prompt**: `golang.org/x/term` (already in go.sum) for raw input
  on the consent gate; falls back to bufio scanner if not a tty.

No new top-level deps beyond `modernc.org/sqlite`.

## Build / packaging flow

Two new Makefile.local targets:

```make
SECURITY_BUNDLE_ID  ?= com.charon.security
SECURITY_APP_NAME   ?= Charon Security
SECURITY_APP_DIR    ?= $(HOME)/Applications/$(SECURITY_APP_NAME).app

security-build:
	go build -o bin/charon-security ./cmd/charon-security

security-install: security-build
	@scripts/dev/build-security-app.sh \
		--bundle-id "$(SECURITY_BUNDLE_ID)" \
		--app-dir "$(SECURITY_APP_DIR)" \
		--sign-identity "$(SIGN_IDENTITY)" \
		--binary bin/charon-security

security: security-install
	@"$(SECURITY_APP_DIR)/Contents/MacOS/charon-security" check

security-remedy:
	@"$(SECURITY_APP_DIR)/Contents/MacOS/charon-security" remedy

security-uninstall:
	rm -rf "$(SECURITY_APP_DIR)"
	tccutil reset SystemPolicyAllFiles "$(SECURITY_BUNDLE_ID)" 2>/dev/null || true
	@echo "Removed $(SECURITY_APP_NAME).app and revoked any TCC grants."
```

`make security` is the user-facing entry: builds, installs, runs check.
The `security-install` step is idempotent so re-runs don't churn TCC
grants (TCC keys on bundle ID + cert leaf, both stable across rebuilds —
same property M4 relies on).

`scripts/dev/build-security-app.sh` will:
1. mkdir `<app-dir>/Contents/MacOS`
2. cp Go binary in
3. Write Info.plist with `CFBundleIdentifier`, `CFBundleExecutable`,
   `CFBundleName`, `CFBundlePackageType=APPL`, `LSUIElement=true`
   (background app, no Dock icon)
4. `codesign --force --sign "$IDENTITY" --identifier "$BUNDLE_ID"
   --options runtime <app-dir>` — hardened runtime on, since the
   security tool itself should be exemplary
5. `codesign --verify --verbose=1 <app-dir>`

## TCC.db schema (target reference)

macOS 11+ schema (relevant subset):

```sql
CREATE TABLE access (
    service        TEXT NOT NULL,         -- e.g. kTCCServiceSystemPolicyAllFiles
    client         TEXT NOT NULL,         -- bundle ID or absolute path
    client_type    INTEGER NOT NULL,      -- 0=bundle ID, 1=absolute path
    auth_value     INTEGER NOT NULL,      -- 0=denied, 1=unknown, 2=allowed, 3=limited
    auth_reason    INTEGER,
    auth_version   INTEGER,
    csreq          BLOB,                  -- code signing requirement
    policy_id      INTEGER,
    indirect_object_identifier_type INTEGER,
    indirect_object_identifier      TEXT, -- for AppleEvents: target app
    indirect_object_code_identity   BLOB,
    flags          INTEGER,
    last_modified  INTEGER NOT NULL,
    PRIMARY KEY (service, client, client_type, indirect_object_identifier)
);
```

Service strings we care about:
- `kTCCServiceSystemPolicyAllFiles` — Full Disk Access
- `kTCCServiceAccessibility` — Accessibility
- `kTCCServiceScreenCapture` — Screen Recording
- `kTCCServiceAppleEvents` — AppleEvents (uses indirect_object_identifier for target)
- `kTCCServiceSystemPolicyDocumentsFolder` / `Downloads` / `Desktop` /
  `RemovableVolumes` / `NetworkVolumes` — Files & Folders sub-grants

Schema versioning: the schema has churned across macOS releases (columns
added in 10.15, 11, 12). Read defensively: `SELECT service, client, client_type, auth_value FROM access` only — these columns have been stable since 10.15. Indirect-object columns are 11+; check `PRAGMA table_info(access)` first and degrade gracefully on older systems.

We read **both** databases:
- `~/Library/Application Support/com.apple.TCC/TCC.db` — user-scope
- `/Library/Application Support/com.apple.TCC/TCC.db` — system-scope
  (less commonly populated; some grants live here)

Open with `?mode=ro&immutable=1` to be belt-and-suspenders read-only and
avoid SQLite trying to journal.

## Severity rollup

Per `internal/security/output.go`:

```go
type Severity int
const (
    SevHygiene Severity = iota
    SevInfo
    SevImportant
    SevCritical
)

type Finding struct {
    ID          string    // stable, e.g. "tcc-fda-terminal"
    Severity    Severity
    Title       string    // one-line summary
    Detail      string    // multi-line body
    RemedyRef   string    // pointer to remedy/text.go entry
    Affects     []string  // app names / paths impacted
}
```

Rollup → exit code:
- any Critical → exit 2
- any Important (no Critical) → exit 1
- otherwise → exit 0
- `--strict` shifts every tier up by one before rollup

## Pre-flight transparency block

Implementation in `internal/security/preflight.go`. Computed at runtime:
- self-binary SHA256 via `os.Executable()` + `crypto/sha256`
- own bundle ID from `Info.plist` adjacent to executable (when running
  from inside the .app); falls back to "(running from go build, no
  bundle)" with a warning that auto-revoke won't work
- version string from `runtime/debug.ReadBuildInfo()` VCS tag

The consent gate reads from `/dev/tty` directly (not stdin) so `make
security | tee log` still works. Default-deny: anything other than
`y` / `yes` (case-insensitive) returns false.

## Visual-mode fallback (`--no-tcc`)

When set:
- skip TCC.db reads
- skip auto-revoke prompt
- after privilege-free checks complete, walk user through System
  Settings panes one by one:

```
For automated TCC enumeration, re-run without --no-tcc (will require
granting Full Disk Access to Charon Security.app).

Manual TCC audit checklist:

  [Enter] open Full Disk Access pane...
  ✓ Look for any of: Terminal, iTerm2, Ghostty, VS Code, Cursor, ...
  ✓ Toggle off any terminal/IDE you find.

  [Enter] open Accessibility pane...
  ...
```

Each `[Enter]` runs `open "x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles"` etc.

## Auto-revoke flow (M6)

After audit + before exit:

```go
if grantedFDAToSelf() {
    fmt.Println("This tool currently has Full Disk Access (TCC: com.charon.security).")
    if confirmDefaultYes("Revoke now?") {
        cmd := exec.Command("tccutil", "reset", "SystemPolicyAllFiles", "com.charon.security")
        // verify by re-querying TCC.db (will now fail to open, which is the success signal)
    }
}
```

`grantedFDAToSelf()` is a probe: try opening the user TCC.db. If it
opens, we have FDA; if EACCES / "operation not permitted", we don't.
Cleaner than parsing TCC.db for our own row.

## Charon-specific checks (M5)

Two checks:

1. **`charon-signing-key-acl`** — Read the `Charon Self-Signed`
   private-key entry's ACL via `SecKeychainItemCopyAccess` +
   `SecAccessCopyACLList`. Trusted-applications list MUST be empty
   (or contain only entries whose path doesn't exist anymore). If
   `/usr/bin/codesign` is in the list → Critical: A10 defense
   compromised, signing key abusable without prompts.

2. **`charon-entries-acl`** — For each `charon` namespace entry in
   the login keychain, verify the ACL has at least one
   trusted-application entry, and that its DR matches charon's expected
   predicate (`identifier "com.charon.cli" and certificate leaf =
   H"<sha1>"`). Surfaces M4 ACL drops the way the test plan does
   manually.

Both reuse code paths that already exist in `internal/vault/keychain/`.
Lift into a new `internal/security/checks/charon_acl.go` that imports
the keychain package's helpers.

## Known terminals/IDEs list

`internal/security/knownapps/known.go`:

```go
type App struct {
    BundleID  string
    Name      string
    Category  Category  // Terminal | Editor | IDE
}

var Known = []App{
    {"com.apple.Terminal",          "Terminal",         Terminal},
    {"com.googlecode.iterm2",       "iTerm2",           Terminal},
    {"com.mitchellh.ghostty",       "Ghostty",          Terminal},
    {"dev.warp.Warp-Stable",        "Warp",             Terminal},
    {"co.zeit.hyper",               "Hyper",            Terminal},
    {"org.alacritty",               "Alacritty",        Terminal},
    {"com.github.wez.wezterm",      "WezTerm",          Terminal},
    {"net.kovidgoyal.kitty",        "Kitty",            Terminal},
    {"org.tabby",                   "Tabby",            Terminal},
    {"com.cmux.cmux",               "cmux",             Terminal},  // verify ID empirically
    {"com.microsoft.VSCode",        "VS Code",          Editor},
    {"com.todesktop.230313mzl4w4u92","Cursor",          Editor},
    {"com.exafunction.windsurf",    "Windsurf",         Editor},
    {"dev.zed.Zed",                 "Zed",              Editor},
    {"com.sublimetext.4",           "Sublime Text",     Editor},
    {"com.panic.Nova",              "Nova",             Editor},
    {"com.apple.dt.Xcode",          "Xcode",            IDE},
    // JetBrains family — these are per-product
    {"com.jetbrains.intellij",      "IntelliJ IDEA",    IDE},
    {"com.jetbrains.intellij.ce",   "IntelliJ IDEA CE", IDE},
    {"com.jetbrains.goland",        "GoLand",           IDE},
    {"com.jetbrains.pycharm",       "PyCharm",          IDE},
    {"com.jetbrains.WebStorm",      "WebStorm",         IDE},
    {"com.jetbrains.rubymine",      "RubyMine",         IDE},
    {"com.jetbrains.CLion",         "CLion",            IDE},
    {"com.jetbrains.rider",         "Rider",            IDE},
}
```

`--apps-extra <bundle-id>:<name>` flag for runtime additions. List ages
maybe annually; not worth externalizing to YAML.

Detection: for each `Known.BundleID`, `mdfind "kMDItemCFBundleIdentifier
== '<id>'"` returns paths. Cache the result for the run. Also check
common install dirs (`/Applications`, `~/Applications`,
`/System/Applications`) as a fallback for spotlight-disabled boxes.

## Tests

`internal/security/checks/*_test.go`:
- TCC parser: table-driven against fixture sqlite DBs in `testdata/`
  (small, hand-built DBs covering schema-old and schema-new).
- Severity rollup: pure-function unit tests.
- Knownapps detection: mocked filesystem.
- Codesign entitlements parser: fixture plist outputs.

Integration test in `cmd/charon-security/main_test.go`:
- Runs `check --no-tcc` against the live system; asserts output format
  but not content (which is host-dependent).

No tests against the live TCC.db — flaky and host-dependent.

## Order of operations (sequencing)

M1 → M2 land independent of TCC; usable on any Mac as the privilege-free
audit. M3 unblocks M4 (need .app bundle for proper TCC attribution).
M5 is independent — can land any time after M2. M6 depends on M4 (auto-
revoke needs FDA-required workflow). M7 is a refactor over M2/M4 outputs.
M8 is documentation. M9/M10 are docs/test-plan.

Critical-path: **M1 → M2 → M3 → M4 → M6 → release-ready.**
M5/M7/M8/M9/M10 can interleave or come after.

## Out of scope (deferred)

- Homebrew shell signing checks
- Time Machine encryption status check (would be relevant for C1, but
  requires querying TCC-protected APIs)
- Extracting tool to a standalone repo (revisit if external interest)
- Hardened runtime on `cmd/charon` itself — separate work, A5 in threat
  model
- AppleEvents pairwise reporting beyond the simple "terminal X can drive
  credential-app Y" flag
