package security

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/xianxu/charon/internal/vault/keychain"
)

// CheckCharonKeychainACLs verifies that charon's keychain entries
// actually have the M4 SecAccess attached. Reuses
// internal/vault/keychain's CGo inspector — the same code that the
// vault integration tests use to assert ACLs on writes.
//
// Inspects BOTH the prod (`charon`) and dev (`charon-dev`) namespaces
// because the audit tool runs from charon-security.app with a
// different code-signing identity than the charon CLI, so it can't
// rely on ResolveServiceName picking the right one.
//
// Findings per inspected entry:
//
//	(0, 0)            — no ACL at all → Critical (any process can read
//	                    via `security find-generic-password`).
//	(>0, 0)           — SecAccess present but no trusted apps. Always-
//	                    prompt mode; healthy and intentional.
//	(>0, 1)           — typical good state — one trusted app (charon's
//	                    DR). Healthy.
//	(>0, N>1)         — multiple trusted apps. Suspicious; emit
//	                    Important so the user can audit via Keychain
//	                    Access.
//
// Signing-key ACL inspection (the `charon-signing-acl` finding ID) is
// deferred — it requires CGo to walk the cert/key item directly, not
// through Store. Users can verify manually via Keychain Access; the
// remedy text walks them through it.
func CheckCharonKeychainACLs() []Finding {
	var findings []Finding
	totalChecked := 0
	for _, service := range []string{keychain.ServiceProd, keychain.ServiceDev} {
		f, checked := inspectCharonNamespace(service)
		findings = append(findings, f...)
		totalChecked += checked
	}
	if totalChecked == 0 {
		findings = append(findings, Finding{
			ID:       "charon-no-entries",
			Severity: SevInfo,
			Title:    "No charon keychain entries found in either namespace",
			Detail:   "No CA cert/key and no OAuth accounts under `charon` or `charon-dev`. If you've used charon, the entries may have been deleted; otherwise this is normal on a fresh install.",
		})
	}
	return findings
}

func inspectCharonNamespace(service string) ([]Finding, int) {
	store := keychain.NewWithService(service)
	creds, err := store.List()
	if err != nil {
		return []Finding{{
			ID:       "charon-list-error-" + service,
			Severity: SevImportant,
			Title:    fmt.Sprintf("Could not enumerate keychain entries under %q", service),
			Detail:   err.Error(),
		}}, 0
	}

	// Internal namespaces aren't returned by Store.List (they're not
	// user-facing credentials) but they're high-value targets — the
	// CA private key in particular — so the audit must check them
	// explicitly.
	accounts := []string{"_ca:cert", "_ca:key"}
	for _, c := range creds {
		accounts = append(accounts, c.Provider+":"+c.Account)
	}

	var findings []Finding
	checked := 0
	for _, account := range accounts {
		ac, app, err := store.InspectACL(account)
		if err != nil {
			if errors.Is(err, errInspectUnavailable) {
				return []Finding{{
					ID:       "charon-acl-cgo-required",
					Severity: SevInfo,
					Title:    "Charon ACL inspection requires darwin+cgo build",
					Detail:   err.Error(),
				}}, 0
			}
			// errSecItemNotFound is normal for absent internal
			// namespaces on a fresh install — silent skip.
			continue
		}
		checked++
		label := service + "/" + account
		switch {
		case ac == 0 && app == 0:
			findings = append(findings, Finding{
				ID:        "charon-entries-acl-missing-" + service + "-" + account,
				Severity:  SevCritical,
				Title:     fmt.Sprintf("Keychain entry %q has no ACL", label),
				Detail:    "Entry has no SecAccess attached — readable by any process running as you via `security find-generic-password`. The M4 boundary that this entry should enforce is absent. Common cause: a stale `charon serve` daemon wrote it before M4 landed (or after a regression). Re-write the entry through the current charon binary to attach the ACL.",
				RemedyRef: "charon-entries-acl",
				Affects:   []string{label},
			})
		case ac > 0 && app > 1:
			findings = append(findings, Finding{
				ID:        "charon-entries-acl-many-" + service + "-" + account,
				Severity:  SevImportant,
				Title:     fmt.Sprintf("Keychain entry %q has %d trusted applications (expected 1)", label, app),
				Detail:    "Multiple apps in the trusted-applications list. Expected state is one entry: charon's own DR. Verify each via Keychain Access → Get Info → Access Control.",
				RemedyRef: "charon-entries-acl",
				Affects:   []string{label},
			})
			// (>0, 0) and (>0, 1) — healthy. No finding.
		}
	}
	return findings, checked
}

// errInspectUnavailable is the sentinel the keychain package returns on
// builds without CGo. We import its message via string match because
// adding a new exported error to the keychain package isn't worth the
// blast radius for this one diagnostic.
var errInspectUnavailable = errors.New("InspectACL requires darwin+cgo")

// signingIdentityLine matches lines from `security find-identity -v`
// of the form `  N) <40-hex> "Identity Name"`. Used to enumerate
// charon-relevant signing identities to audit.
var signingIdentityLine = regexp.MustCompile(`^\s*\d+\)\s+[0-9A-Fa-f]+\s+"([^"]+)"`)

// CheckCharonSigningKeyACL inspects the signing-key ACLs for any
// charon-related code-signing identities present in the user's login
// keychain. The desired state is an EMPTY trusted-applications list
// — every codesign use should prompt. Any non-zero appCount is a
// Critical finding because it lets a process sign a Mach-O that
// satisfies charon's M4 ACL DR predicate without prompting,
// defeating defense layer 5 and adversary A10.
//
// Identities checked: anything whose label is "Charon Self-Signed"
// or starts with "Developer ID Application:". The latter is the
// post-#000011 production signing identity; the former is the
// historical self-signed one (still present on machines that
// haven't fully migrated). Both should have empty trusted-apps
// lists for the same reason.
//
// Discovery uses `security find-identity -v -p codesigning` rather
// than walking the keychain directly — that's the same approach
// Makefile.local takes to auto-detect SIGN_IDENTITY and matches
// what the user sees in their environment.
func CheckCharonSigningKeyACL() []Finding {
	// Drop the `-p codesigning` policy filter: it excludes self-signed
	// certs (CSSMERR_TP_NOT_TRUSTED), but charon's M4 ACL doesn't
	// route through that policy — it evaluates the DR predicate
	// directly. So both Charon Self-Signed and Developer ID
	// identities matter. Plain `find-identity` returns everything.
	out, err := exec.Command("security", "find-identity").Output()
	if err != nil {
		return []Finding{{
			ID:       "charon-signing-discovery-error",
			Severity: SevImportant,
			Title:    "Could not enumerate signing identities",
			Detail:   fmt.Sprintf("`security find-identity` failed: %v", err),
		}}
	}

	labels := []string{}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		m := signingIdentityLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name := m[1]
		if !isCharonSigningIdentity(name) || seen[name] {
			continue
		}
		seen[name] = true
		labels = append(labels, name)
	}

	if len(labels) == 0 {
		// No charon-relevant identities present. Not a finding —
		// machine may not have charon installed yet, or may use a
		// different signing setup. Silent.
		return nil
	}

	var findings []Finding
	for _, label := range labels {
		ac, app, err := keychain.InspectSigningKeyACL(label)
		if err != nil {
			if errors.Is(err, keychain.ErrSigningKeyNotFound) {
				// Identity listed by find-identity but the matching
				// private key isn't directly findable by label —
				// possible on Dev ID where the cert label and key
				// label can diverge. Skip silently rather than alarm.
				continue
			}
			findings = append(findings, Finding{
				ID:       "charon-signing-acl-error-" + safeLabel(label),
				Severity: SevImportant,
				Title:    fmt.Sprintf("Could not inspect signing key %q", label),
				Detail:   err.Error(),
			})
			continue
		}
		switch {
		case ac == 0:
			// No SecAccess at all — extremely unusual for a private
			// key but theoretically possible. Treat as Critical
			// because the key's protections are unknown.
			findings = append(findings, Finding{
				ID:        "charon-signing-acl-missing-" + safeLabel(label),
				Severity:  SevCritical,
				Title:     fmt.Sprintf("Signing key %q has no SecAccess", label),
				Detail:    "Private key has no access controls. Inspect via Keychain Access; consider regenerating the identity.",
				RemedyRef: "charon-signing-acl",
				Affects:   []string{label},
			})
		case app > 0:
			findings = append(findings, Finding{
				ID:       "charon-signing-acl-trusted-apps-" + safeLabel(label),
				Severity: SevImportant,
				Title:    fmt.Sprintf("Signing key %q has %d trusted application(s) (expected 0)", label, app),
				Detail: fmt.Sprintf(
					"Charon's signing key trusted-applications list should be empty so every codesign use prompts the user. "+
						"%d entries are present.\n\n"+
						"This audit can count the entries but cannot yet name them (issue #000012 item A is the planned extension). "+
						"You need to verify in Keychain Access which apps are listed:\n\n"+
						"  - **Catastrophic** (the A10 case): /usr/bin/codesign or /usr/bin/security in the list. Any process "+
						"running as you can then sign a Mach-O that satisfies charon's M4 keychain ACL silently. Remove "+
						"immediately.\n"+
						"  - **Probably benign**: Keychain Access, SecurityAgent, or similar Apple system services. These "+
						"are the default state for keys generated via Certificate Assistant. Strict hygiene removes them; "+
						"in practice they don't compromise the layer 5 protection.\n\n"+
						"How to inspect: Open Keychain Access → search %q → right-click the *private key* → Get Info → "+
						"Access Control tab. Each row in the lower list is a trusted app. Remove anything codesign- or "+
						"signing-related; leave Apple system services alone unless you want maximum strictness.\n\n"+
						"To prove the catastrophic case isn't happening: try `make install`. If you got a Keychain Access "+
						"Allow/Deny prompt, codesign is NOT trusted (good). If install completed silently, codesign IS "+
						"trusted (bad — the A10 case).",
					app, label),
				RemedyRef: "charon-signing-acl",
				Affects:   []string{label},
			})
			// Healthy state (ac > 0 && app == 0) — silent.
		}
	}
	return findings
}

// isCharonSigningIdentity reports whether the given identity name is
// one we audit. Filtering at name match time keeps us from
// accidentally inspecting unrelated user identities (e.g. a separate
// Developer ID for some other project) — though that's also valid
// hygiene, it's outside charon's scope.
func isCharonSigningIdentity(name string) bool {
	if name == "Charon Self-Signed" {
		return true
	}
	if strings.HasPrefix(name, "Developer ID Application:") {
		return true
	}
	return false
}

// safeLabel turns an identity label into something usable as a
// Finding ID suffix (no spaces, no quotes).
func safeLabel(label string) string {
	r := strings.NewReplacer(" ", "-", "(", "", ")", "", `"`, "", ":", "")
	return r.Replace(label)
}
