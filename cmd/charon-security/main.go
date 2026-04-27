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
		WillReadTCC:      !flagNoTCC,
		WillCheckCharon:  true,
		WillPromptRevoke: !flagNoTCC && self.BundleID != "",
	}
	security.PrintPreflight(os.Stderr, self, opts)

	if !flagYes {
		if !security.ConfirmDefaultDeny("Continue with the audit?") {
			fmt.Fprintln(os.Stderr, "aborted.")
			return nil
		}
	}

	report := security.Report{}

	// M2 will populate privilege-free checks; M4 will populate TCC; M5
	// charon-specific. For M1 we wire the flow end-to-end with a single
	// placeholder finding so the rollup + visual fallback are exercisable.
	if flagNoTCC {
		// The walk is fundamentally interactive: it pauses for the user
		// at each pane. Skip it under --yes (declared non-interactive)
		// or when there's no tty available.
		if !flagYes && security.IsInteractive() {
			security.RunVisualWalk(os.Stderr)
		} else {
			fmt.Fprintln(os.Stderr, "(skipping visual TCC walk; re-run interactively without --yes for the System Settings audit)")
		}
	} else {
		report.Findings = append(report.Findings, security.Finding{
			ID:       "m1-stub",
			Severity: security.SevInfo,
			Title:    "M1 skeleton wired; no checks implemented yet",
			Detail:   "M2 lands SIP/sudo/launchd; M4 lands TCC; M5 lands charon ACLs.",
		})
	}

	printSummary(report)

	if flagStrict {
		// Promote every finding's severity by one before rollup.
		for i := range report.Findings {
			if report.Findings[i].Severity < security.SevCritical {
				report.Findings[i].Severity++
			}
		}
	}
	os.Exit(report.ExitCode())
	return nil
}

func runRemedy(args []string) error {
	// M8 fills this in. For M1 we print a placeholder so the subcommand
	// is reachable.
	fmt.Println("Remedy text lands in M8. See docs/threat-model.md for now.")
	return nil
}

func printSummary(r security.Report) {
	counts := r.Counts()
	fmt.Fprintf(os.Stderr, "\nAudit summary: %d findings  ", len(r.Findings))
	fmt.Fprintf(os.Stderr, "(critical=%d  important=%d  info=%d  hygiene=%d)\n",
		counts[security.SevCritical], counts[security.SevImportant],
		counts[security.SevInfo], counts[security.SevHygiene])
	for _, f := range r.Findings {
		fmt.Fprintf(os.Stderr, "  [%s] %s — %s\n", f.Severity, f.ID, f.Title)
	}
}
