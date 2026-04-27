# Plan: Code signing + Keychain ACL (#000003)

This plan elaborates the *how* for #000003. The *what* lives in the issue.

## Goal recap

Make charon's keychain entries readable only by the charon binary itself,
so an in-process AI agent (or any other user-space process) can no longer
exfiltrate OAuth refresh tokens or the proxy CA private key via
`security find-generic-password` or sibling APIs.

## Non-goals

- Apple notarization / distribution outside the user's own Mac (later).
- Linux secret-service ACL hardening (#000002 owns Linux).
- PKCE migration for the OAuth client (separate work).
- Hardened runtime / entitlements (only matters once we move to Apple Dev ID).

## Decisions (from brainstorm 2026-04-26)

1. **Self-signed first, Apple Dev ID later.** Self-signed gives the same
   in-process security boundary on a single machine and makes the OSS path
   one-script-to-bootstrap. Dev ID upgrade is a predicate swap + migration.
2. **Single binary, CGo on darwin always.** No separate "secure" build. The
   `security` CLI shell-out is replaced wholesale by Security framework
   calls via CGo on darwin. Linux keeps its own backend.
3. **Runtime self-signature check picks the keychain service name.** A
   binary signed with the expected identity uses service `charon`; an
   unsigned/dev binary uses service `charon-dev`. Same source, same
   compilation; behavior diverges from the binary's own signing state.
4. **`make install` is the only step that signs.** `make build` stays fast
   and unsigned. The install pipeline does build → codesign → copy.
5. **Generic ACL migration command.** One code path handles legacy → ACL
   (today) and self-signed → Dev ID (later). Migration is "find any entry
   whose ACL doesn't match my current expected predicate, re-write it
   under my predicate."
6. **CA cert + CA key get the same ACL treatment.** The CA private key is
   strictly more dangerous than OAuth tokens (forges HTTPS for any host).
   Migration auto-handles them on first signed-binary run.
7. **OAuth tokens are not migrated.** User revokes them in Google + the
   keychain pre-cutover; re-auth on the signed binary writes them under
   ACL.

## Architecture overview

### What changes

```
cmd/charon/                             # +1 cobra command: migrate-acl
internal/vault/keychain/
├── keychain.go        (today)          # current security-CLI Store, kept as
│                                       #   `// +build !cgo` fallback for
│                                       #   non-darwin or CGO_ENABLED=0
├── keychain_darwin.go  (NEW)           # darwin+cgo Store using Security framework
├── codesign_darwin.go  (NEW)           # SecCodeCopySelf + designated requirement
├── acl_darwin.go       (NEW)           # SecAccess construction with predicate
├── service.go          (NEW)           # ResolveServiceName(): runtime self-check
└── *_test.go                           # unit + integration tests
internal/vault/migrate/   (NEW pkg)
└── migrate.go                          # generic "re-write under current predicate"
scripts/dev/
└── setup-signing-identity.sh (NEW)     # bootstrap: openssl + security import
Makefile.local                          # `install` adds codesign step
README.md                               # signing bootstrap section
atlas/charon.md                         # update Design Decisions + Zero-Config
```

### Backend selection by build tags

```
//go:build darwin && cgo
// keychain_darwin.go — primary backend
```

```
//go:build !cgo || (!darwin && !linux)
// keychain.go — security-CLI fallback (kept for CGO_ENABLED=0 builds + CI matrices)
```

Linux secret-service backend (#000002) lives in its own file under its own
build tag; out of scope here.

### Runtime service-name selection

```go
// internal/vault/keychain/service.go
const (
    ServiceProd = "charon"
    ServiceDev  = "charon-dev"
)

// ResolveServiceName inspects the running binary's signature and returns
// the keychain service name to use. Cached after first call.
func ResolveServiceName() string {
    if hasExpectedSignature() {
        return ServiceProd
    }
    return ServiceDev
}
```

`hasExpectedSignature()` calls `SecCodeCopySelf` + `SecCodeCheckValidity`
with the designated requirement string we expect (see below). On the
fallback (non-cgo / non-darwin) builds, it always returns false → service
is `charon-dev` — which is a no-op since those builds don't write ACLs
anyway.

## Self-signing identity

### Why a script

OSS users (and the maintainer on a fresh Mac) need a one-shot way to
create the local code-signing cert. The Keychain Access GUI works but
isn't scriptable; OpenSSL + `security import` is the standard programmatic
recipe.

### `scripts/dev/setup-signing-identity.sh`

Idempotent. Skips if `Charon Self-Signed` already exists in the user's
login keychain.

Steps:

1. Existence check: `security find-identity "$LOGIN_KC" | grep "$CN"`.
   We use plain `find-identity` (no `-v -p codesigning`) on purpose —
   the filtered form lists only *trusted* identities and would hide our
   self-signed cert (which is correctly marked `CSSMERR_TP_NOT_TRUSTED`
   without an explicit trust override). On match → exit 0.
2. Generate a 4096-bit RSA key + self-signed X.509 cert via `openssl req`
   with these extensions:
   - `extendedKeyUsage = codeSigning`
   - `1.2.840.113635.100.1.1` Apple-specific code signing OID (required
     for codesign to accept it)
   - 10-year validity (`-days 3650`)
   - Subject CN: `Charon Self-Signed`
3. Bundle into a `.p12` (with `openssl pkcs12 -legacy` — OpenSSL 3.x's
   default PBE encoding is unreadable by macOS's importer) using a
   random throwaway password.
4. `security import <p12> -k login.keychain -P <pw> -T /usr/bin/codesign -f pkcs12`
   — imports the cert+key, adds `/usr/bin/codesign` to the entry's ACL
   so it can use the private key.
5. Print "ready, run `make install`."

We deliberately do **not** run:

- `security set-key-partition-list` — deprecated in modern macOS,
  requires the user's login password through a brittle stdin path, and
  the standard "Always Allow" Keychain Access dialog on first
  `make install` accomplishes the same thing more reliably.
- `security add-trusted-cert` — the M4 ACL predicate
  (`certificate leaf = H"<sha1>"`) matches by leaf cert hash, not by
  trust anchor, so user-trust-root status is irrelevant. Running it
  would trigger an admin auth UI dialog for no benefit.

The script is also runnable as `make signing-identity` (added to
`Makefile.local`) for discoverability.

## CGo keychain backend

### Library choice

`github.com/keybase/go-keychain` — BSD-licensed, actively maintained, used
by 1Password's Go projects + Keybase. Wraps `SecKeychainFindGenericPassword`,
`SecAccessCreate`, `SecKeychainItemSetAccess`, etc. Saves us writing
several hundred lines of CGo by hand.

### `internal/vault/keychain/keychain_darwin.go`

```go
//go:build darwin && cgo

package keychain

import (
    "encoding/json"
    "fmt"

    "github.com/keybase/go-keychain"
    "github.com/xianxu/charon/internal/vault"
)

type Store struct {
    service string // resolved once at construction
}

func New() *Store {
    return &Store{service: ResolveServiceName()}
}

func (s *Store) Get(provider, account string) (*vault.Credential, error) {
    query := keychain.NewItem()
    query.SetSecClass(keychain.SecClassGenericPassword)
    query.SetService(s.service)
    query.SetAccount(keyName(provider, account))
    query.SetMatchLimit(keychain.MatchLimitOne)
    query.SetReturnData(true)
    results, err := keychain.QueryItem(query)
    // ... unmarshal storedCredential, return
}

func (s *Store) Set(cred *vault.Credential) error {
    item := keychain.NewItem()
    item.SetSecClass(keychain.SecClassGenericPassword)
    item.SetService(s.service)
    item.SetAccount(keyName(cred.Provider, cred.Account))
    item.SetData(marshalled)
    item.SetSynchronizable(keychain.SynchronizableNo)
    if s.service == ServiceProd {
        item.SetAccess(buildCharonAccess())  // → ACL bound to designated requirement
    }
    // delete-then-add (keybase wrapper exposes Update too, but contract is
    //   "overwrite"; delete-add matches today's behavior)
    _ = keychain.DeleteItem(item)
    return keychain.AddItem(item)
}
// Delete, List similarly
```

`buildCharonAccess()` constructs a `SecAccess` whose ACL says: "any
process satisfying the charon designated requirement may read/use this
item without prompt." See `acl_darwin.go`.

### Designated requirement (self-signed era)

```
identifier "com.charon.cli" and certificate leaf = H"<sha1-of-Charon-Self-Signed-leaf>"
```

This is exactly the predicate codesign auto-generates and embeds in the
binary as the designated requirement when signing with `Charon Self-Signed`
(verified empirically in M1 via `codesign -dr- <signed-binary>`).

- `identifier "com.charon.cli"` is set at codesign time via `--identifier`.
  Constant across rebuilds.
- `certificate leaf = H"..."` pins the specific self-signed cert. The
  hash is **SHA-1** (40 hex chars) — that's macOS's code-signing
  designated-requirement format, regardless of the digest used elsewhere
  in the signature (which is SHA-256 on modern macOS). The hash equals
  the identity hash printed by `security find-identity`.
- Stable until the cert is regenerated (every 10 years per script default,
  or on machine move).

The fingerprint is computed at first `make install` and embedded in a
generated file (`internal/vault/keychain/requirement_generated.go`) — or,
to keep the build pure: read at runtime from the cert in the user's
keychain by querying the identity's leaf and hashing. Tradeoff:

- **Embed at build time**: simpler runtime, but `make install` becomes a
  multi-step thing (read cert → codegen → build → sign → install). Cert
  rotation requires rebuild.
- **Read at runtime**: install pipeline stays clean. Slightly more
  Security framework code at startup. Cert rotation is detected
  automatically.

**Pick: read at runtime.** `ResolveDesignatedRequirement()` does
`SecIdentityCopyCertificate` for the `Charon Self-Signed` identity,
SHA-1's the DER, plugs into the predicate string. Cached in memory after
first call. Cleaner build pipeline; aligns with the "binary adapts to its
own state" philosophy. Even better: read the binary's *own* designated
requirement back via `SecCodeCopyDesignatedRequirement` and reuse it
verbatim — no need to recompute.

When upgrading to Apple Dev ID later, this function returns the
team-anchored predicate instead, with no other code changes.

### `acl_darwin.go`

`SecAccessCreate` with two ACL entries:

1. **Charon trusted** — applications list = `[charonTrustedRef]` where
   `charonTrustedRef` is built from the designated requirement string.
   Permitted operations: `kSecACLAuthorizationDecrypt`, `…ExportClear`.
2. **Default-deny everyone else** — empty applications list, no
   `kSecACLAuthorizationDecrypt`. With prompts disabled, this gives the
   "Allow/Deny dialog appears" behavior macOS shows for unrecognized
   readers.

The keybase library exposes `SetAccess(access *Access)` but its API for
constructing `*Access` from a designated requirement is thin — we may
need a small wrapper using `cgo` directly to call `SecAccessCreate` with
`SecTrustedApplicationCreateFromRequirement`. Tracked as risk below.

## Migration

### `internal/vault/migrate/migrate.go`

```go
package migrate

// Result describes one entry's migration outcome.
type Result struct {
    Provider string
    Account  string
    Action   string // "ok", "rewritten", "skipped", "failed"
    Err      error
}

// Run scans the keychain for entries under both ServiceProd and (if
// running signed) ServiceDev, reads any whose ACL doesn't match the
// current designated requirement, and re-writes them under the current
// predicate.
func Run(s *keychain.Store) ([]Result, error) { ... }
```

Two surface forms:

1. **Auto-migrate on first signed-binary startup**, scoped to
   `_ca:cert` + `_ca:key` only (low-risk, deterministic). Logs at INFO
   level. Idempotent.
2. **Explicit `charon migrate-acl` command** — user-facing, scans all
   entries, prompts for any cross-identity reads (the macOS "Allow"
   dialog will fire when reading entries written under a different
   predicate). Useful for the future Dev ID transition.

### Migration ACL detection

The keybase wrapper doesn't directly expose "is this entry's ACL X" — we
detect by attempting a read. If the read succeeds without prompt → entry
is already under our predicate (or has no ACL). If it prompts → user
clicks Allow → we re-write under our predicate → next read is silent.

For the *first* migration (legacy `_ca:*` entries written without ACL by
today's CLI-shelling backend), reads succeed without prompt regardless,
so migration is a single-pass rewrite.

## Install pipeline

### `Makefile.local`

```make
SIGN_IDENTITY ?= Charon Self-Signed
CODESIGN_IDENTIFIER = com.charon.cli

build:
	CGO_ENABLED=1 go build -o bin/charon ./cmd/charon

build-nocgo:
	CGO_ENABLED=0 go build -o bin/charon-nocgo ./cmd/charon

install: build
	@if ! security find-identity -v -p codesigning | grep -q "$(SIGN_IDENTITY)"; then \
	    echo "No '$(SIGN_IDENTITY)' identity. Run: scripts/dev/setup-signing-identity.sh"; \
	    exit 1; \
	fi
	codesign --force --sign "$(SIGN_IDENTITY)" --identifier "$(CODESIGN_IDENTIFIER)" bin/charon
	codesign --verify --verbose bin/charon
	@mkdir -p ~/.local/bin
	@rm -f ~/.local/bin/charon
	@cp bin/charon ~/.local/bin/charon
	@echo "Installed signed charon to ~/.local/bin/charon"

signing-identity:
	scripts/dev/setup-signing-identity.sh
```

Tests on darwin require CGO; CI matrices on Linux fall back to the
non-CGO build via `build-nocgo`.

## Test strategy

### Unit (pure Go)

- `service_test.go` — `ResolveServiceName()` with mocked self-check
  function; verifies `charon` for signed, `charon-dev` for unsigned.
- `migrate_test.go` — uses an in-memory fake keychain implementing the
  store interface; verifies "non-matching predicate → rewrite" semantics
  without touching real Security framework.
- `keychain_test.go` (existing) — kept; still validates the CLI fallback.

### Integration (darwin, behind `integration` tag, CGo)

- `keychain_darwin_test.go` (gated `integration && darwin && cgo`):
  - Round-trip Set/Get under `charon-dev` service (no ACL).
  - Round-trip Set/Get under `charon` service (ACL'd to current
    designated requirement, written and read by the same test binary).
  - Migration test: write a non-ACL'd entry via legacy CLI path, run
    migration, verify it's re-readable.

### Manual (documented checklist in issue Log when complete)

- After `make install`, run `~/.local/bin/charon serve` and `charon auth`
  to write a real Google credential.
- Inspect: `security dump-keychain | grep charon` shows entry exists.
- Read attempt from a different shell:
  `security find-generic-password -s charon -a google:<email> -w` →
  expect Allow/Deny dialog. Click Deny.
- Read attempt from charon itself: `~/.local/bin/charon accounts` →
  expect silent success (entry visible).
- Repeat for `_ca:cert` and `_ca:key`.
- Build dev binary `make build` → `./bin/charon serve` — should not
  prompt because it uses `charon-dev` service (which has no ACL'd
  entries; first auth populates fresh state).

## Risks / open questions

1. **`SecAccessCreate` from a requirement string in the keybase wrapper**
   may be missing — if so, we drop down to direct CGo for the
   `acl_darwin.go` shim. ~50 lines of CGo. Not a blocker, but adds work.
2. **`security set-key-partition-list` UX**: on first signing identity
   creation the user may see a system password prompt during codesign.
   Documented in the bootstrap script README block. If it repeatedly
   prompts during normal `make install`, we'll add `-A` to the import
   step.
3. **Hardened runtime + entitlements**: not enabled for self-signed; will
   be required when we move to Apple Dev ID + notarization. Out of scope
   for this issue.
4. **`charon migrate-acl` cross-identity prompts**: the future Dev ID
   migration will fire one Allow dialog per entry. Acceptable for an
   infrequent one-shot. Document in upgrade notes.
5. **`go-keychain` dependency surface**: brings in `golang.org/x/sys` and
   a small CGo wrapper. Pure-Go elsewhere unaffected; `go.sum` grows.

## Apple Dev ID upgrade (future, not this issue)

When the user enrolls in Apple Developer Program:

- `setup-signing-identity.sh` is replaced by the Dev ID cert install
  (Apple's standard provisioning flow).
- `SIGN_IDENTITY` Makefile var changes to `Developer ID Application: ...`.
- `ResolveDesignatedRequirement()` returns the team-anchored predicate
  `anchor apple generic and certificate leaf[subject.OU] = "<TEAMID>"`.
- User runs `charon migrate-acl` once. macOS prompts Allow per entry
  (one-time), then all entries are re-written under the new predicate.
- Add `--options runtime` + minimal entitlements to codesign step.
- Optional: `xcrun notarytool submit ... --wait` for distribution.

The migration command and the runtime self-check are written generically
today so this upgrade is a config-level change.

## Milestone breakdown

| M  | Scope                                                     | Verification              |
|----|-----------------------------------------------------------|---------------------------|
| M1 | `setup-signing-identity.sh` + `make signing-identity`     | Cert appears in find-identity |
| M2 | `keychain_darwin.go` + `acl_darwin.go` (no ACL writes yet, just CGo Get/Set/Delete/List parity with CLI) | Existing keychain tests pass under CGo backend; integration test passes |
| M3 | `service.go` + `codesign_darwin.go`; runtime split        | Unit test for ResolveServiceName with mocked check |
| M4 | ACL on `Set` for `ServiceProd`; designated requirement resolution | Integration test: written entry has expected ACL |
| M5 | `internal/vault/migrate/` + `charon migrate-acl` + first-run CA auto-migrate | Integration test: legacy entry → re-written; idempotent |
| M6 | `Makefile.local` install pipeline + README + atlas update | `make install` produces signed binary on a fresh machine |
| M7 | Manual test checklist run; document Allow/Deny screenshots in issue Log | Logged in issue |

Post-each-milestone code review per AGENTS.md §3 (subagent code-reviewer
with milestone-boundary BASE_SHA/HEAD_SHA). Critical/Important findings
addressed before next milestone.
