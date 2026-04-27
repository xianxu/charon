package security

import "fmt"

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

// Finding is one item the audit produced. ID is stable across runs and
// is the key into remedy text. Affects holds app names / paths the
// finding pertains to (may be empty).
type Finding struct {
	ID        string
	Severity  Severity
	Title     string
	Detail    string
	RemedyRef string
	Affects   []string
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
