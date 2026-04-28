// Command charon-security audits a personal Mac for the hygiene
// baseline charon's threat model assumes (see docs/threat-model.md):
// SIP enabled, no TCC grants on terminals/IDEs, no suspicious launchd
// agents, charon's keychain ACLs intact.
//
// Designed to be packaged as Charon Security.app so TCC attributes
// permissions to com.charon.security specifically; run from
// `make security` after `make security-install`.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
	"github.com/xianxu/charon/internal/security"
)

var (
	flagNoTCC   bool
	flagNoColor bool
	flagJSON    bool
	flagStrict  bool
	flagYes     bool
)

func main() {
	root := &cobra.Command{
		Use:   "charon-security",
		Short: "Audit macOS hygiene for agentic-coding workflows",
		Long: "Charon Security audits the local Mac for the environmental " +
			"assumptions charon's threat model relies on: SIP enabled, no " +
			"excessive TCC grants on terminals/IDEs, charon's keychain ACL " +
			"boundary intact. See docs/threat-model.md.",
	}
	root.PersistentFlags().BoolVar(&flagNoColor, "no-color", false, "disable colored output")
	root.PersistentFlags().BoolVar(&flagJSON, "json", false, "emit findings as JSON (overrides text output)")

	root.AddCommand(checkCmd())
	root.AddCommand(remedyCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func checkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Run the audit and report findings",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCheck()
		},
	}
	cmd.Flags().BoolVar(&flagNoTCC, "no-tcc", false,
		"skip TCC.db reads (no FDA needed); fall back to manual System Settings walk")
	cmd.Flags().BoolVar(&flagStrict, "strict", false,
		"promote every severity tier up by one before exit-code rollup")
	cmd.Flags().BoolVar(&flagYes, "yes", false,
		"skip the pre-flight consent gate (for non-interactive runs)")
	return cmd
}

func remedyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remedy [finding-id]",
		Short: "Print remediation steps (all findings, or one by ID)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRemedy(args)
		},
	}
}

func runCheck() error {
	self, err := security.LoadSelfInfo()
	if err != nil {
		return fmt.Errorf("inspect self: %w", err)
	}

	opts := security.PreflightOptions{
		// Toggled on as M6 lands. Keeping these honest now is the
		// difference between a transparency block and a fairy tale.
		WillReadTCC:      !flagNoTCC,
		WillCheckCharon:  true,
		WillPromptRevoke: false,
	}
	security.PrintPreflight(os.Stderr, self, opts)

	if flagYes {
		fmt.Fprintln(os.Stderr, "(--yes specified, skipping consent gate)")
	} else {
		if !security.ConfirmDefaultDeny("Continue with the audit?") {
			fmt.Fprintln(os.Stderr, "aborted.")
			return nil
		}
	}

	report := security.Report{}
	report.Findings = append(report.Findings, security.CheckSIP()...)
	report.Findings = append(report.Findings, security.CheckSudoCache()...)
	report.Findings = append(report.Findings, security.CheckLaunchdAgents()...)

	apps := security.DetectInstalledApps()
	fmt.Fprintf(os.Stderr, "\nDetected %d known terminals/editors/IDEs:\n", len(apps))
	for _, a := range apps {
		fmt.Fprintf(os.Stderr, "  %-30s %s  (%s)\n", a.BundleID, a.Path, a.Category)
	}
	report.Findings = append(report.Findings, security.CheckCodesignEntitlements(apps)...)

	if !flagNoTCC {
		tccFindings := security.CheckTCC(apps)
		report.Findings = append(report.Findings, tccFindings...)
		// If we couldn't read TCC.db because of missing FDA, offer to
		// open the System Settings pane right now — saves the user
		// digging for instructions in the remedy.
		offerFDAGrantIfNeeded(tccFindings, self)
	}

	report.Findings = append(report.Findings, security.CheckCharonKeychainACLs()...)
	report.Findings = append(report.Findings, security.CheckCharonSigningKeyACL()...)

	if flagNoTCC {
		if !flagYes && security.IsInteractive() {
			security.RunVisualWalk(os.Stderr)
		} else {
			fmt.Fprintln(os.Stderr, "(skipping visual TCC walk; re-run interactively without --yes for the System Settings audit)")
		}
	}

	if flagStrict {
		// Promote every finding's severity by one before rollup.
		for i := range report.Findings {
			if report.Findings[i].Severity < security.SevCritical {
				report.Findings[i].Severity++
			}
		}
	}

	out := os.Stderr
	if flagJSON {
		out = os.Stdout
	}
	if err := report.Print(out, security.PrintOptions{
		NoColor: flagNoColor,
		JSON:    flagJSON,
	}); err != nil {
		return err
	}

	os.Exit(report.ExitCode())
	return nil
}

// offerFDAGrantIfNeeded looks for the tcc-no-fda-* findings produced
// by CheckTCC and, when running interactively, walks the user through
// adding the .app to the FDA pane. No-op on --yes (non-interactive)
// or when running outside a .app bundle (where granting FDA wouldn't
// be scoped to com.charon.security).
func offerFDAGrantIfNeeded(findings []security.Finding, self security.SelfInfo) {
	needsFDA := false
	for _, f := range findings {
		if strings.HasPrefix(f.ID, "tcc-no-fda-") {
			needsFDA = true
			break
		}
	}
	if !needsFDA || flagYes || !security.IsInteractive() {
		return
	}
	if self.BundleID == "" {
		fmt.Fprintln(os.Stderr, "\nNote: running outside a .app bundle. Granting FDA now would attach to your terminal, not to charon-security. Run via `make security` for proper TCC attribution.")
		return
	}
	fmt.Fprintf(os.Stderr, "\nFull Disk Access not granted to %s.\n", self.BundleID)
	if !security.ConfirmDefaultYes("Open System Settings → Full Disk Access now?") {
		return
	}
	if err := exec.Command("open",
		"x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles").Run(); err != nil {
		fmt.Fprintf(os.Stderr, "(could not open System Settings: %v)\n", err)
		return
	}
	if self.BundlePath != "" {
		// Reveal the .app in Finder so the user can drag-drop.
		_ = exec.Command("open", "-R", self.BundlePath).Run()
	}
	fmt.Fprintln(os.Stderr, "\nIn the System Settings pane:")
	fmt.Fprintln(os.Stderr, "  1. Drag \"Charon Security.app\" from Finder into the list, OR click + and pick it.")
	fmt.Fprintln(os.Stderr, "  2. Toggle the switch ON.")
	fmt.Fprintln(os.Stderr, "  3. Re-run `make security` to read TCC.db.")
}

func runRemedy(args []string) error {
	opts := security.RenderOptions{NoColor: flagNoColor}
	if len(args) == 0 {
		security.PrintAllRemedies(os.Stdout, opts)
		return nil
	}
	ref := args[0]
	entry := security.LookupRemedy(ref)
	if entry == nil {
		security.PrintUnknownRef(os.Stderr, ref)
		os.Exit(1)
	}
	security.PrintRemedy(os.Stdout, entry, opts)
	return nil
}

