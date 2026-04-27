package security

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// TCC service names we care about. Stable across macOS 11+.
const (
	tccFDA    = "kTCCServiceSystemPolicyAllFiles"
	tccA11y   = "kTCCServiceAccessibility"
	tccScreen = "kTCCServiceScreenCapture"
	tccEvents = "kTCCServiceAppleEvents"
)

// CredentialApps are high-value targets for AppleEvents grants — apps
// that hold or display credentials. A terminal/editor/IDE with
// Automation rights to one of these is a Critical finding (it can be
// scripted via AppleEvents to extract secrets, bypassing direct
// keychain ACLs).
var CredentialApps = map[string]string{
	"com.apple.keychainaccess":   "Keychain Access",
	"com.agilebits.onepassword":  "1Password",
	"com.agilebits.onepassword7": "1Password 7",
	"com.bitwarden.desktop":      "Bitwarden",
	"com.dashlane.dashlane":      "Dashlane",
	"com.lastpass.LastPass":      "LastPass",
}

// TCCRow is one row from the access table. Only the columns we
// consume are pulled — the schema has churned across macOS versions
// and a wide SELECT * would break on older or newer hosts.
type TCCRow struct {
	Service                  string `json:"service"`
	Client                   string `json:"client"`
	ClientType               int    `json:"client_type"`
	AuthValue                int    `json:"auth_value"`
	IndirectObjectIdentifier string `json:"indirect_object_identifier"`
}

// IsAllowed reports whether this auth_value means the grant is
// currently active. 2 = allowed, 3 = limited (e.g. partial folder
// access for SystemPolicy* services). 0 = denied, 1 = unknown.
func (r TCCRow) IsAllowed() bool {
	return r.AuthValue == 2 || r.AuthValue == 3
}

// ErrNoFDA is returned by ReadTCC when the database can't be opened
// because the running process lacks Full Disk Access. Callers can
// surface this as a "grant FDA and re-run" hint instead of a hard
// error.
var ErrNoFDA = errors.New("TCC.db not readable (Full Disk Access required)")

// TCCDatabasePath returns the canonical path for the user-scope or
// system-scope TCC database. Returns "" for unknown scope.
func TCCDatabasePath(scope string) string {
	switch scope {
	case "user":
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return filepath.Join(home, "Library/Application Support/com.apple.TCC/TCC.db")
	case "system":
		return "/Library/Application Support/com.apple.TCC/TCC.db"
	}
	return ""
}

// ReadTCC opens the given TCC.db read-only via /usr/bin/sqlite3 and
// returns the access rows we evaluate.
//
// Why shell-out instead of a Go SQLite library: the audit tool
// already shells out to csrutil, sudo, mdfind, codesign, PlistBuddy.
// Adding a 1 MB pure-Go SQLite dep for one read-only query against a
// stable schema is overkill; macOS ships sqlite3 in /usr/bin and
// supports -json output since 3.33 (Catalina+).
func ReadTCC(dbPath string) ([]TCCRow, error) {
	fi, err := os.Stat(dbPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // system-scope DB is sometimes absent
		}
		if os.IsPermission(err) {
			return nil, ErrNoFDA
		}
		return nil, err
	}
	if fi.IsDir() {
		return nil, fmt.Errorf("TCC.db path is a directory: %s", dbPath)
	}

	// Probe FDA attribution by opening the file directly first. If
	// this succeeds the running process has FDA via TCC; if a
	// subsequent sqlite3 shell-out then fails, we've isolated the
	// bug to child-process attribution rather than the grant itself.
	probe, openErr := os.Open(dbPath)
	if openErr != nil {
		if os.IsPermission(openErr) {
			return nil, ErrNoFDA
		}
		return nil, openErr
	}
	probe.Close()

	cmd := exec.Command("/usr/bin/sqlite3",
		"-readonly", "-json", dbPath,
		"SELECT service, client, client_type, auth_value, "+
			"IFNULL(indirect_object_identifier, '') AS indirect_object_identifier "+
			"FROM access")
	out, err := cmd.CombinedOutput()
	if err != nil {
		s := string(out)
		switch {
		case strings.Contains(s, "unable to open database"),
			strings.Contains(s, "authorization denied"),
			strings.Contains(s, "Operation not permitted"):
			// Direct os.Open succeeded above — so the parent bundle
			// HAS FDA. sqlite3 failing here means the child process
			// didn't inherit attribution. Surface that diagnosis
			// rather than the vague "FDA required" hint.
			return nil, fmt.Errorf("FDA attached to bundle but /usr/bin/sqlite3 child failed: %s", strings.TrimSpace(s))
		default:
			return nil, fmt.Errorf("sqlite3 query failed: %w: %s", err, strings.TrimSpace(s))
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	var rows []TCCRow
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, fmt.Errorf("parse TCC json: %w", err)
	}
	return rows, nil
}

// CheckTCC reads both TCC databases and emits findings for grants
// held by detected terminals/editors/IDEs. Path-based grants
// (client_type=1) are out of scope — terminals and IDEs are bundles.
//
// Severity matrix:
//
//	FDA           on terminal/editor/IDE → Critical
//	Accessibility on terminal/editor/IDE → Critical
//	ScreenCapture on terminal/editor/IDE → Important
//	AppleEvents   on terminal/editor/IDE → Important (Critical when target is a credential app)
//
// When a TCC database can't be read for FDA reasons, a single Info
// finding suggests granting FDA to the audit tool itself.
func CheckTCC(apps []DetectedApp) []Finding {
	byBundle := make(map[string]DetectedApp, len(apps))
	for _, a := range apps {
		byBundle[a.BundleID] = a
	}

	var findings []Finding
	for _, scope := range []string{"user", "system"} {
		path := TCCDatabasePath(scope)
		if path == "" {
			continue
		}
		rows, err := ReadTCC(path)
		switch {
		case errors.Is(err, ErrNoFDA):
			findings = append(findings, Finding{
				ID:        "tcc-no-fda-" + scope,
				Severity:  SevInfo,
				Title:     fmt.Sprintf("Cannot read %s-scope TCC.db (Full Disk Access required)", scope),
				Detail:    fmt.Sprintf("Grant Full Disk Access to com.charon.security via System Settings → Privacy & Security → Full Disk Access, then re-run. M6's auto-revoke prompt at end-of-run cleans up afterward.\n\nAlternatively run with --no-tcc to skip this check and use the visual System Settings walk."),
				RemedyRef: "tcc-fda",
				Affects:   []string{path},
			})
			continue
		case err != nil:
			findings = append(findings, Finding{
				ID:       "tcc-readerr-" + scope,
				Severity: SevInfo,
				Title:    fmt.Sprintf("Could not read %s-scope TCC.db", scope),
				Detail:   err.Error(),
				Affects:  []string{path},
			})
			continue
		}
		findings = append(findings, evaluateTCCRows(rows, byBundle, scope)...)
	}
	return findings
}

// evaluateTCCRows is the pure function — ZERO syscalls — that turns
// rows + known-app set into findings. Tests target this directly.
func evaluateTCCRows(rows []TCCRow, byBundle map[string]DetectedApp, scope string) []Finding {
	var findings []Finding
	for _, r := range rows {
		if !r.IsAllowed() || r.ClientType != 0 {
			continue
		}
		app, known := byBundle[r.Client]
		if !known {
			continue
		}
		f := Finding{
			Affects: []string{app.Path},
		}
		switch r.Service {
		case tccFDA:
			f.ID = "tcc-fda-" + r.Client
			f.Severity = SevCritical
			f.Title = fmt.Sprintf("%s has Full Disk Access", app.Name)
			f.RemedyRef = "tcc-fda"
		case tccA11y:
			f.ID = "tcc-a11y-" + r.Client
			f.Severity = SevCritical
			f.Title = fmt.Sprintf("%s has Accessibility", app.Name)
			f.RemedyRef = "tcc-a11y"
		case tccScreen:
			f.ID = "tcc-screen-" + r.Client
			f.Severity = SevImportant
			f.Title = fmt.Sprintf("%s has Screen Recording", app.Name)
			f.RemedyRef = "tcc-screen"
		case tccEvents:
			target := r.IndirectObjectIdentifier
			if target == "" {
				continue // bare AppleEvents permission with no target — no signal
			}
			f.ID = "tcc-events-" + r.Client + "-" + target
			f.Severity = SevImportant
			targetLabel := target
			if name, ok := CredentialApps[target]; ok {
				f.Severity = SevCritical
				targetLabel = name + " (" + target + ")"
			}
			f.Title = fmt.Sprintf("%s can drive %s via AppleEvents", app.Name, targetLabel)
			f.RemedyRef = "tcc-events"
		default:
			continue // service we don't audit
		}
		f.Detail = fmt.Sprintf("Service: %s\nScope: %s-scope TCC.db\nClient: %s (bundle)", r.Service, scope, r.Client)
		findings = append(findings, f)
	}
	return findings
}
