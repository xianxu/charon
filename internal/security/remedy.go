package security

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// RemedyEntry is the long-form prose for one finding class. The Ref
// matches Finding.RemedyRef so multiple findings collapse to one
// remedy entry (e.g. every codesign-weak-* finding shares ref="codesign").
type RemedyEntry struct {
	Ref     string
	Title   string
	Why     string // why charon's threat model cares
	Fix     string // concrete steps / commands
	SeeAlso string // pointers into docs/threat-model.md or external refs
}

// Remedies is the curated remedy catalog. Order is meaningful for the
// "print all" playbook — group by area (system → tcc → charon).
var Remedies = []RemedyEntry{
	{
		Ref:   "sip",
		Title: "System Integrity Protection (SIP)",
		Why: "SIP is macOS's kernel-level barrier against root-equivalent code modifying system files, attaching debuggers to signed binaries, or loading unsigned kexts. Charon's threat model assumes SIP is on (assumption 3 in docs/threat-model.md). Without SIP, an attacker with sudo can attach lldb to a running charon, read decrypted secrets straight from process memory, replace charon's binary on disk, or load a malicious dylib — defeating every layer below it.",
		Fix: "Verify:\n" +
			"  csrutil status   # expect: System Integrity Protection status: enabled.\n\n" +
			"If disabled:\n" +
			"  1. Reboot into Recovery:\n" +
			"     - Apple Silicon: hold power until \"Loading startup options\"\n" +
			"     - Intel: hold Cmd-R during boot\n" +
			"  2. Utilities → Terminal\n" +
			"  3. csrutil enable\n" +
			"  4. Reboot.\n\n" +
			"If \"Custom Configuration\": csrutil status lists which subsystem is\nrelaxed. Common case is dev work that ran `csrutil enable --without\ndebug` (or similar) and forgot to undo. Re-enable fully when finished.",
		SeeAlso: "docs/threat-model.md → Adversary C (Local root / SIP-disabled).",
	},
	{
		Ref:   "sudo",
		Title: "Cached sudo credentials in this shell",
		Why: "sudo caches authentication per-tty for ~5 minutes by default. Any subprocess in that tty — including an agent you launch — can call `sudo -n <anything>` and succeed without prompting. The cache is per-tty, not per-process, so the agent doesn't have to be a descendant of the original sudo command. Footgun: you `sudo make install`, then in the same window run `agent-cli` — that agent now has unattended sudo for the next few minutes.",
		Fix: "Immediate:\n" +
			"  sudo -k        # invalidates the cached credential in this tty\n\n" +
			"Habit: launch agent shells from a freshly opened terminal window\nwhere you haven't sudo'd.\n\n" +
			"For a stricter default, edit /etc/sudoers via `sudo visudo` and add:\n" +
			"  Defaults timestamp_timeout=0\n" +
			"This disables caching entirely — every sudo prompts.",
	},
	{
		Ref:   "launchd",
		Title: "Third-party launchd plists",
		Why: "Plists in ~/Library/LaunchAgents (user-scope) and /Library/LaunchAgents, /Library/LaunchDaemons (system-scope) make their owners auto-start at login or boot. A compromised tool can install one to persist across reboots; most users never audit. Charon's own com.charon.proxy.plist shows up here, which is expected — the audit can't tell yours from someone else's, so it lists everything non-Apple/Homebrew/Docker for you to review.",
		Fix: "Inspect each plist's ProgramArguments / Program key:\n" +
			"  defaults read ~/Library/LaunchAgents/<name>.plist\n" +
			"  # or for binary plists:\n" +
			"  /usr/libexec/PlistBuddy -c 'Print' ~/Library/LaunchAgents/<name>.plist\n\n" +
			"Remove what you don't recognize:\n" +
			"  launchctl bootout gui/$(id -u) ~/Library/LaunchAgents/<name>.plist\n" +
			"  rm ~/Library/LaunchAgents/<name>.plist\n\n" +
			"For system-scope plists, sudo and `launchctl bootout system/<label>`.\n\n" +
			"Recognized noise on a charon dev box:\n" +
			"  com.charon.proxy.plist                — charon's own service\n" +
			"  com.google.GoogleUpdater.wake.plist   — Chrome/Drive auto-updater",
		SeeAlso: "docs/threat-model.md → A7 (persistence beachhead).",
	},
	{
		Ref:   "codesign",
		Title: "Terminal/IDE ships hardened-runtime-weakening entitlements",
		Why: "Apple's hardened runtime is a per-app opt-in that blocks DYLD_INSERT_LIBRARIES, requires entitlements for debugger attach, and blocks library hijacking. Apps that need to load user code (shells reading dotfiles, IDEs loading plugins, debugger frontends) sometimes ship with weakening entitlements like:\n\n" +
			"  com.apple.security.cs.allow-dyld-environment-variables\n" +
			"  com.apple.security.cs.disable-library-validation\n" +
			"  com.apple.security.cs.allow-unsigned-executable-memory\n" +
			"  com.apple.security.cs.allow-jit\n\n" +
			"When charon runs alongside such a terminal/IDE, A5-class injection becomes viable: an agent can load a dylib into the parent process's address space, satisfy charon's DR by association, and read keychain entries silently.",
		Fix: "You can't fix this without repackaging the third-party app. Practical mitigations:\n\n" +
			"  - Inspect any app yourself:\n" +
			"      codesign -d --entitlements - --xml /Applications/<App>.app\n\n" +
			"  - Prefer a stricter terminal for agentic work. Apple Terminal.app and\n" +
			"    iTerm2 generally use hardened runtime without weakening entitlements.\n\n" +
			"  - If a particular IDE needs the entitlements for a plugin you use,\n" +
			"    run that IDE for non-credential work, and use a stricter terminal\n" +
			"    when launching agents that talk to charon.",
		SeeAlso: "docs/threat-model.md → A5 (in-process injection).",
	},

	// --- TCC family (M4 will fill these in for real findings) ---

	{
		Ref:   "tcc-fda",
		Title: "Terminal/IDE has Full Disk Access",
		Why: "TCC permissions inherit from the launching process. If Terminal.app has Full Disk Access, every shell command spawned from Terminal — including the AI agent — has FDA. With FDA the agent can read ~/Library/Keychains/login.keychain-db raw, attempt offline brute-force of the master key, and access adjacent secrets (Mail, Messages, Safari cookies, ~/.ssh, Notes). Charon's M4 keychain ACL still gates the Security-framework API path, but the broader process boundary collapses.\n\n" +
			"This is the single most damaging TCC grant for agentic workflows.",
		Fix: "Open the pane:\n" +
			"  open \"x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles\"\n\n" +
			"Or: System Settings → Privacy & Security → Full Disk Access.\n\n" +
			"Toggle off every terminal/editor/IDE listed. Keep FDA only for tools\nthat genuinely need it (Time Machine helpers, Arq, Backblaze, etc.).\n\n" +
			"Nuclear reset:\n" +
			"  tccutil reset SystemPolicyAllFiles\n\n" +
			"Apps that legitimately need FDA will re-prompt next use; everything else\nstays revoked. After two weeks of normal use you'll have a minimal set.",
		SeeAlso: "docs/threat-model.md → Adversary B (TCC-grants), B1.",
	},
	{
		Ref:   "tcc-a11y",
		Title: "Terminal/IDE has Accessibility",
		Why: "Arguably worse than FDA for charon's threat model — and almost no one audits it. A process with Accessibility can synthesize keystrokes and mouse clicks. If an agent triggers a keychain Allow/Deny dialog, the agent (running inside an Accessibility-granted terminal) can click \"Allow\" itself, defeating the M4 ACL boundary entirely. The whole point of layer 3 (Allow/Deny prompt) is that a human looks at it; Accessibility lets the attacker BE the human.",
		Fix: "Open the pane:\n" +
			"  open \"x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility\"\n\n" +
			"Toggle off every terminal, editor, and IDE.\n\n" +
			"Window managers and input remappers (Rectangle, Hammerspoon,\nBetterTouchTool, Karabiner) legitimately need Accessibility — that's\nfine, since those tools don't run shells.\n\n" +
			"Reset all:\n" +
			"  tccutil reset Accessibility\n\n" +
			"Then re-grant only to dedicated automation apps.",
	},
	{
		Ref:   "tcc-screen",
		Title: "Terminal/IDE has Screen Recording",
		Why: "A process with Screen Recording can capture whatever charon's TUI or audit log prints — including, during debugging or normal operation, token prefixes, account headers, scope strings. Less catastrophic than FDA or Accessibility but a steady leak channel for sensitive output that the user assumed was ephemeral.",
		Fix: "Open the pane:\n" +
			"  open \"x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture\"\n\n" +
			"Toggle off terminals/IDEs unless you have an active reason for them\nto record screen content (e.g., demo recording, screencasting).\n\n" +
			"Reset:\n" +
			"  tccutil reset ScreenCapture",
	},
	{
		Ref:   "tcc-events",
		Title: "Terminal/IDE has Automation/AppleEvents grants to credential apps",
		Why: "AppleEvents lets app A drive app B's UI. If your terminal has Automation rights to control Keychain Access, 1Password, Bitwarden, or Mail, an agent running inside that terminal can script those apps to extract secrets via their UI — bypassing direct keychain ACLs entirely. The credential app sees the requests as legitimate user-initiated actions because they come from a process the user previously authorized.",
		Fix: "Open the pane:\n" +
			"  open \"x-apple.systempreferences:com.apple.preference.security?Privacy_Automation\"\n\n" +
			"This pane is hierarchical: each entry shows app A → controllable apps.\nPay particular attention to terminal/IDE entries that can drive Keychain\nAccess, password managers, or Mail. Toggle those off.\n\n" +
			"Reset all AppleEvents grants:\n" +
			"  tccutil reset AppleEvents",
	},

	// --- Charon-specific (M5 fills the wiring; this prose is stable) ---

	{
		Ref:   "charon-signing-acl",
		Title: "Charon signing key has populated trusted-applications list",
		Why: "The `Charon Self-Signed` private key in your login keychain should have an empty trusted-applications list — every use should prompt Allow/Deny. If /usr/bin/codesign is in the list, codesign can sign arbitrary binaries with charon's identity without prompting, defeating defense layer 5 (A10 in the threat model). An attacker who shells out to `codesign --sign \"Charon Self-Signed\" /tmp/agent-impostor` then gets a Mach-O whose DR matches charon's M4 ACL predicate.\n\n" +
			"The bootstrap script intentionally omits `-T /usr/bin/codesign`. The list typically gets polluted by clicking \"Always Allow\" on a codesign prompt during a previous `make install` — the warning in the bootstrap output and README exists for exactly this reason.",
		Fix: "Inspect:\n" +
			"  open /System/Applications/Utilities/Keychain\\ Access.app\n" +
			"  → search \"Charon Self-Signed\"\n" +
			"  → right-click the *private key* → Get Info → Access Control tab\n\n" +
			"Expected: \"Confirm before allowing access\" selected, lower list empty.\n\n" +
			"If codesign or any app is in the list:\n" +
			"  - Remove it manually from that pane, OR\n" +
			"  - Regenerate the identity entirely:\n" +
			"      make signing-identity   # bootstrap a fresh self-signed cert\n" +
			"      make install            # re-sign + re-create keychain entries\n" +
			"    Old charon entries (signed by the previous cert) become unreadable.\n" +
			"    Recovery path: revoke + re-auth your OAuth accounts.\n\n" +
			"During every future `make install`, click ALLOW (single-use), never\n\"Always Allow\". The latter re-adds codesign to the trust list.",
		SeeAlso: "docs/threat-model.md → A10 (signing key abuse via codesign).",
	},
	{
		Ref:   "charon-entries-acl",
		Title: "Charon keychain entry is missing or has weak ACL",
		Why: "Each entry in the `charon` keychain namespace should have a SecAccess whose trusted-applications list pins to charon's designated requirement (identifier \"com.charon.cli\" and certificate leaf hash). An entry without that ACL is readable by any process running as your user via `security find-generic-password` — exactly the bypass M4 was supposed to eliminate.\n\n" +
			"Common cause: a stale `charon serve` daemon running an older binary wrote the entry before M4 landed (or after a code change that regressed the SetItemAdd path). The entry inherits the writer's behavior, not the current binary's.",
		Fix: "Stop any stale charon serve instances:\n" +
			"  launchctl bootout gui/$(id -u)/com.charon.proxy  # or kill the PID\n" +
			"  pkill -f \"charon serve\"\n\n" +
			"Re-run any operation that writes the affected entry — the new write\ngoes through the ACL'd path.\n\n" +
			"For OAuth tokens specifically, the safest reset is revoke + re-auth\nthe affected account: that drops the old entry and creates a new one\nwith a fresh ACL.\n\n" +
			"Manual inspection of one entry:\n" +
			"  security find-generic-password -s charon -a <account> -g\n\n" +
			"Healthy output includes an `Access:` block listing the trusted\napplication. Missing or empty `Access:` line → the entry has no ACL.",
		SeeAlso: "docs/threat-model.md → Defense layer 3 (Keychain ACL).",
	},
}

// remedyByRef is the lookup index, populated lazily on first use.
var remedyByRef = func() map[string]*RemedyEntry {
	m := make(map[string]*RemedyEntry, len(Remedies))
	for i := range Remedies {
		m[Remedies[i].Ref] = &Remedies[i]
	}
	return m
}()

// LookupRemedy returns the RemedyEntry for a given Ref, or nil if no
// entry exists.
func LookupRemedy(ref string) *RemedyEntry {
	return remedyByRef[ref]
}

// AllRemedyRefs returns the canonical refs in the order they appear
// in Remedies. Used for "print all" output and for "unknown ref"
// suggestions.
func AllRemedyRefs() []string {
	out := make([]string, len(Remedies))
	for i, r := range Remedies {
		out[i] = r.Ref
	}
	return out
}

// PrintRemedy renders one entry as multi-section text. Stable format
// regardless of caller, so docs can quote it.
func PrintRemedy(w io.Writer, e *RemedyEntry) {
	fmt.Fprintf(w, "═══ %s  (%s)\n", e.Title, e.Ref)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Why:")
	fmt.Fprintln(w, indent(e.Why, "  "))
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Fix:")
	fmt.Fprintln(w, indent(e.Fix, "  "))
	if e.SeeAlso != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "See also:", e.SeeAlso)
	}
	fmt.Fprintln(w)
}

// PrintAllRemedies prints every entry as a single playbook. Order
// follows Remedies (curated, not alphabetical).
func PrintAllRemedies(w io.Writer) {
	for i := range Remedies {
		PrintRemedy(w, &Remedies[i])
	}
}

// PrintUnknownRef prints a friendly error listing valid refs. Used by
// `charon-security remedy <bogus>`.
func PrintUnknownRef(w io.Writer, ref string) {
	fmt.Fprintf(w, "Unknown remedy ref: %q\n\nKnown refs:\n", ref)
	refs := AllRemedyRefs()
	sort.Strings(refs)
	for _, r := range refs {
		fmt.Fprintf(w, "  %s\n", r)
	}
}

// indent prefixes every line of s with prefix. Trailing newline (if
// any) is preserved without prefixing past it.
func indent(s, prefix string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l == "" {
			continue
		}
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}
