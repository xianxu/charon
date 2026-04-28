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
				BarItem:   BarKeychainEntries,
			})
		case ac > 0 && app > 1:
			findings = append(findings, Finding{
				ID:        "charon-entries-acl-many-" + service + "-" + account,
				Severity:  SevImportant,
				Title:     fmt.Sprintf("Keychain entry %q has %d trusted applications (expected 1)", label, app),
				Detail:    "Multiple apps in the trusted-applications list. Expected state is one entry: charon's own DR. Verify each via Keychain Access → Get Info → Access Control.",
				RemedyRef: "charon-entries-acl",
				Affects:   []string{label},
				BarItem:   BarKeychainEntries,
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
		ac, app, drs, err := keychain.InspectSigningKeyACLDetailed(label)
		if err != nil {
			if errors.Is(err, keychain.ErrSigningKeyNotFound) {
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
			findings = append(findings, Finding{
				ID:        "charon-signing-acl-missing-" + safeLabel(label),
				Severity:  SevCritical,
				Title:     fmt.Sprintf("Signing key %q has no SecAccess", label),
				Detail:    "Private key has no access controls. Inspect via Keychain Access; consider regenerating the identity.",
				RemedyRef: "charon-signing-acl",
				Affects:   []string{label},
				BarItem:   BarSigningKeyACL,
			})
		case app > 0:
			classified := classifyTrustedApps(drs)
			worst := worstTrustedAppVerdict(classified)
			sev := SevHygiene
			titleSuffix := "(all benign)"
			switch worst {
			case verdictCatastrophic:
				sev = SevCritical
				titleSuffix = "(includes catastrophic entries — see detail)"
			case verdictUnknown:
				sev = SevImportant
				titleSuffix = "(some entries unrecognized — see detail)"
			}
			findings = append(findings, Finding{
				ID:        "charon-signing-acl-trusted-apps-" + safeLabel(label),
				Severity:  sev,
				Title:     fmt.Sprintf("Signing key %q has %d trusted application(s) %s", label, app, titleSuffix),
				Detail:    formatTrustedAppsDetail(label, classified),
				RemedyRef: "charon-signing-acl",
				Affects:   []string{label},
				BarItem:   BarSigningKeyACL,
			})
			// Healthy state (ac > 0 && app == 0) — silent.
		}
	}
	return findings
}

// trustedAppVerdict captures the "is this entry safe?" classification
// for one trusted application's Designated Requirement.
type trustedAppVerdict int

const (
	verdictBenign       trustedAppVerdict = iota // recognized Apple default; safe
	verdictUnknown                               // not on the curated list; user judgment
	verdictCatastrophic                          // codesign / security CLI / similar; A10 case
)

func (v trustedAppVerdict) String() string {
	switch v {
	case verdictBenign:
		return "benign"
	case verdictCatastrophic:
		return "CATASTROPHIC"
	default:
		return "unknown"
	}
}

type classifiedTrustedApp struct {
	DR      string
	Verdict trustedAppVerdict
	Label   string // human-readable name for output
	Reason  string // why we classified it that way
}

// classifyTrustedApps walks each DR string and rules on it. The DR
// strings come from SecRequirementCopyString, which produces
// codesign-grammar predicates like:
//
//	identifier "com.apple.CertificateAssistant" and anchor apple
//	identifier "/usr/sbin/racoon"
//	identifier H"abc..." and anchor apple
//
// Pattern matching on identifier value covers the well-known cases.
func classifyTrustedApps(drs []string) []classifiedTrustedApp {
	out := make([]classifiedTrustedApp, 0, len(drs))
	for _, dr := range drs {
		out = append(out, classifyOne(dr))
	}
	return out
}

// catastrophicIdentifiers are the bundle IDs / paths that, if trusted
// for the signing key, defeat charon's layer-5 protection entirely.
// Any process running as the user can drive these to sign a Mach-O
// satisfying charon's M4 ACL DR predicate without prompting.
var catastrophicIdentifiers = []struct {
	pattern string
	label   string
	reason  string
}{
	{`"/usr/bin/codesign"`, "/usr/bin/codesign", "any process can sign a Mach-O satisfying charon's M4 ACL → A10 catastrophic"},
	{`"com.apple.codesign"`, "codesign (bundle ID)", "any process can sign as charon → A10 catastrophic"},
	{`"/usr/bin/security"`, "/usr/bin/security", "any process can read keychain entries via the security CLI"},
	{`"com.apple.security"`, "security (bundle ID)", "any process can read keychain entries via the security CLI"},
}

// benignIdentifiers are Apple system services that legitimately end up
// in default-generated key ACLs. Their presence isn't great hygiene
// but doesn't compromise charon's layer 5 (none of them are general-
// purpose code signers or keychain readers).
var benignIdentifiers = []struct {
	pattern string
	label   string
	reason  string
}{
	// Bundle-ID based DRs (newer Apple-issued or notarized apps)
	{`"com.apple.CertificateAssistant"`, "Certificate Assistant", "Apple's CSR generator — created the key, auto-trusted itself"},
	{`"com.apple.ServerManagerDaemon"`, "ServerManagerDaemon", "vestigial macOS Server daemon (Apple killed macOS Server in 2022)"},
	{`"com.apple.keychainaccess"`, "Keychain Access", "Apple's keychain UI"},
	{`"com.apple.SecurityAgent"`, "SecurityAgent", "Apple's auth dialog presenter"},
	{`"com.apple.systempreferences"`, "System Settings / System Preferences", "Apple's settings UI"},

	// Path-based DRs (legacy apps stored by absolute path; common
	// for Certificate Assistant and old daemons that ship pre-signed
	// without proper bundle IDs in the trust list).
	{`/System/Library/CoreServices/Certificate Assistant.app`, "Certificate Assistant", "Apple's CSR generator — created the key, auto-trusted itself"},
	{`"/usr/sbin/racoon"`, "racoon", "deprecated Apple IPsec daemon, vestigial entry"},
	{`/usr/sbin/racoon`, "racoon", "deprecated Apple IPsec daemon, vestigial entry"},
}

func classifyOne(dr string) classifiedTrustedApp {
	for _, c := range catastrophicIdentifiers {
		if strings.Contains(dr, c.pattern) {
			return classifiedTrustedApp{
				DR: dr, Verdict: verdictCatastrophic, Label: c.label, Reason: c.reason,
			}
		}
	}
	for _, b := range benignIdentifiers {
		if strings.Contains(dr, b.pattern) {
			return classifiedTrustedApp{
				DR: dr, Verdict: verdictBenign, Label: b.label, Reason: b.reason,
			}
		}
	}
	// Pull the identifier "..." substring as the label for unknowns.
	label := extractIdentifier(dr)
	if label == "" {
		label = "(unparseable DR)"
	}
	return classifiedTrustedApp{
		DR: dr, Verdict: verdictUnknown, Label: label,
		Reason: "not on the curated benign or catastrophic list — review manually",
	}
}

// extractIdentifier pulls the identifier value out of a DR string for
// display when classification fails. e.g.
//
//	`identifier "/usr/bin/foo" and anchor apple` → `/usr/bin/foo`
//	`identifier "com.example" and anchor apple` → `com.example`
//	`identifier H"abc..."` → `(hashed identifier)`
func extractIdentifier(dr string) string {
	const marker = `identifier "`
	i := strings.Index(dr, marker)
	if i < 0 {
		if strings.Contains(dr, `identifier H"`) {
			return "(hashed identifier — opaque)"
		}
		return ""
	}
	rest := dr[i+len(marker):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}

func worstTrustedAppVerdict(apps []classifiedTrustedApp) trustedAppVerdict {
	worst := verdictBenign
	for _, a := range apps {
		if a.Verdict > worst {
			worst = a.Verdict
		}
	}
	return worst
}

func formatTrustedAppsDetail(label string, apps []classifiedTrustedApp) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Trusted applications on the private key for %q:\n\n", label)
	for _, a := range apps {
		marker := "✓"
		switch a.Verdict {
		case verdictCatastrophic:
			marker = "✗ CATASTROPHIC"
		case verdictUnknown:
			marker = "? unknown"
		}
		fmt.Fprintf(&b, "  %s  %s — %s\n", marker, a.Label, a.Reason)
	}
	b.WriteString("\nAction: ")
	switch worstTrustedAppVerdict(apps) {
	case verdictCatastrophic:
		b.WriteString("entries marked CATASTROPHIC must be removed immediately. Open Keychain Access → identity → expand cert → right-click private key → Get Info → Access Control → highlight the offending row → click `−` → Save Changes. Consider regenerating the identity entirely if you don't trust the current state.")
	case verdictUnknown:
		b.WriteString("the unknown entries should be inspected manually in Keychain Access. If they're Apple system services with paths under /System or /usr, probably benign; otherwise treat with suspicion. Add them to internal/security/check_charon.go's classification lists once verified.")
	default:
		b.WriteString("all entries are recognized Apple defaults from key generation; no action required for safety. Strict hygiene: remove them anyway via Keychain Access (highlight each row, click `−`, Save Changes). The audit will then pass.")
	}
	return b.String()
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
