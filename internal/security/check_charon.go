package security

import (
	"errors"
	"fmt"

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
