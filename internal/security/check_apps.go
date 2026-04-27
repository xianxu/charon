package security

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DetectedApp is a known app found installed on the system.
type DetectedApp struct {
	KnownApp
	Path string // path to the .app bundle
}

// DetectInstalledApps locates each KnownApp on disk. mdfind (Spotlight)
// is the primary mechanism; falls back to scanning common install dirs
// for hosts with Spotlight off.
//
// Returns one entry per app found; an app present in multiple locations
// is reported once (first hit wins).
func DetectInstalledApps() []DetectedApp {
	out := make([]DetectedApp, 0, len(KnownApps))
	seen := map[string]bool{}

	for _, app := range KnownApps {
		path := findAppPath(app.BundleID)
		if path == "" {
			continue
		}
		if seen[app.BundleID] {
			continue
		}
		seen[app.BundleID] = true
		out = append(out, DetectedApp{KnownApp: app, Path: path})
	}
	return out
}

// findAppPath resolves a bundle ID to an .app path, or "" if not
// installed. Tries mdfind first; falls back to scanning Applications
// dirs.
func findAppPath(bundleID string) string {
	if p := mdfindBundle(bundleID); p != "" {
		return p
	}
	return scanApplicationsForBundle(bundleID)
}

func mdfindBundle(bundleID string) string {
	out, err := exec.Command("mdfind",
		"kMDItemCFBundleIdentifier == "+quoteForMdfind(bundleID)).Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, ".app") {
			if _, err := os.Stat(line); err == nil {
				return line
			}
		}
	}
	return ""
}

// quoteForMdfind wraps the bundle ID in single quotes for mdfind's
// query language. Bundle IDs are alphanumerics + dots + hyphens, no
// quote-escaping issues in practice.
func quoteForMdfind(s string) string { return "'" + s + "'" }

// scanApplicationsForBundle walks the standard Applications directories
// looking for an .app whose Info.plist has the matching bundle ID.
// Slower than mdfind, used as fallback only.
func scanApplicationsForBundle(bundleID string) string {
	dirs := []string{"/Applications", "/System/Applications"}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, "Applications"))
	}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".app") {
				continue
			}
			app := filepath.Join(dir, e.Name())
			id, err := readBundleID(filepath.Join(app, "Contents", "Info.plist"))
			if err == nil && id == bundleID {
				return app
			}
		}
	}
	return ""
}
