package security

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// Severity orders findings from least to most urgent. Exit codes and
// terminal coloring derive from this.
type Severity int

const (
	SevHygiene Severity = iota
	SevInfo
	SevImportant
	SevCritical
)

func (s Severity) String() string {
	switch s {
	case SevCritical:
		return "CRITICAL"
	case SevImportant:
		return "IMPORTANT"
	case SevInfo:
		return "INFO"
	case SevHygiene:
		return "HYGIENE"
	default:
		return fmt.Sprintf("Severity(%d)", int(s))
	}
}

// MarshalJSON serializes Severity as its uppercase string label so
// `--json` output is human-readable and stable.
func (s Severity) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

// Finding is one item the audit produced. ID is stable across runs and
// is the key into remedy text. Affects holds app names / paths the
// finding pertains to (may be empty).
type Finding struct {
	ID        string   `json:"id"`
	Severity  Severity `json:"severity"`
	Title     string   `json:"title"`
	Detail    string   `json:"detail,omitempty"`
	RemedyRef string   `json:"remedy_ref,omitempty"`
	Affects   []string `json:"affects,omitempty"`
}

// Report aggregates findings and produces the rollup exit code.
type Report struct {
	Findings []Finding
}

// ExitCode maps the worst finding's severity to a process exit code.
//
//	any Critical  -> 2
//	any Important -> 1
//	otherwise     -> 0
func (r Report) ExitCode() int {
	worst := SevHygiene
	for _, f := range r.Findings {
		if f.Severity > worst {
			worst = f.Severity
		}
	}
	switch worst {
	case SevCritical:
		return 2
	case SevImportant:
		return 1
	default:
		return 0
	}
}

// Counts returns per-severity counts for summary lines.
func (r Report) Counts() map[Severity]int {
	c := map[Severity]int{}
	for _, f := range r.Findings {
		c[f.Severity]++
	}
	return c
}

// PrintOptions controls Report.Print output shape.
type PrintOptions struct {
	NoColor bool // force ANSI off (default: auto-detect TTY on stderr)
	JSON    bool // emit JSON instead of human text
}

// Print renders the report to w using the requested format. JSON
// output is dependable for CI consumption; text output is colorized
// per severity when stderr is a TTY (and `--no-color` isn't set).
func (r Report) Print(w io.Writer, opts PrintOptions) error {
	if opts.JSON {
		return r.printJSON(w)
	}
	r.printText(w, opts.NoColor)
	return nil
}

// reportJSON is the top-level shape we emit, distinct from Report so
// we can attach Counts and ExitCode without leaking internal types
// (Severity-keyed maps don't serialize cleanly).
type reportJSON struct {
	Summary  reportSummaryJSON `json:"summary"`
	Findings []Finding         `json:"findings"`
}

type reportSummaryJSON struct {
	Total      int            `json:"total"`
	BySeverity map[string]int `json:"by_severity"`
	ExitCode   int            `json:"exit_code"`
}

func (r Report) printJSON(w io.Writer) error {
	counts := r.Counts()
	out := reportJSON{
		Summary: reportSummaryJSON{
			Total:    len(r.Findings),
			ExitCode: r.ExitCode(),
			BySeverity: map[string]int{
				"critical":  counts[SevCritical],
				"important": counts[SevImportant],
				"info":      counts[SevInfo],
				"hygiene":   counts[SevHygiene],
			},
		},
		Findings: r.Findings,
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// Severity colors are vetted against both light and dark terminal
// themes — pure-red is illegible on light, pure-yellow on light;
// these are the lipgloss adaptive picks.
var (
	styleCritical  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#a40000", Dark: "#ff5f5f"})
	styleImportant = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#af5f00", Dark: "#ffaf00"})
	styleInfo      = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#005f87", Dark: "#5fafff"})
	styleHygiene   = lipgloss.NewStyle().Faint(true)
	styleHint      = lipgloss.NewStyle().Faint(true)
)

func styleFor(s Severity) lipgloss.Style {
	switch s {
	case SevCritical:
		return styleCritical
	case SevImportant:
		return styleImportant
	case SevInfo:
		return styleInfo
	default:
		return styleHygiene
	}
}

func (r Report) printText(w io.Writer, noColor bool) {
	useColor := !noColor && term.IsTerminal(int(os.Stderr.Fd()))
	render := func(s Severity, text string) string {
		if !useColor {
			return text
		}
		return styleFor(s).Render(text)
	}
	hint := func(text string) string {
		if !useColor {
			return text
		}
		return styleHint.Render(text)
	}

	counts := r.Counts()
	fmt.Fprintf(w, "\nAudit summary: %d findings  (%s=%d  %s=%d  %s=%d  %s=%d)\n",
		len(r.Findings),
		render(SevCritical, "critical"), counts[SevCritical],
		render(SevImportant, "important"), counts[SevImportant],
		render(SevInfo, "info"), counts[SevInfo],
		render(SevHygiene, "hygiene"), counts[SevHygiene],
	)

	// Severity-desc sort puts Critical at the top so actionable
	// findings aren't buried after Hygiene noise. Stable secondary
	// sort by ID for deterministic ordering across runs.
	sorted := make([]Finding, len(r.Findings))
	copy(sorted, r.Findings)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Severity != sorted[j].Severity {
			return sorted[i].Severity > sorted[j].Severity
		}
		return sorted[i].ID < sorted[j].ID
	})

	for _, f := range sorted {
		tag := render(f.Severity, "["+f.Severity.String()+"]")
		fmt.Fprintf(w, "  %s %s — %s\n", tag, f.ID, f.Title)
		for _, a := range f.Affects {
			fmt.Fprintf(w, "      %s\n", a)
		}
		if f.RemedyRef != "" {
			fmt.Fprintf(w, "      %s\n", hint("→ details: charon-security remedy "+f.RemedyRef))
		}
	}
}
