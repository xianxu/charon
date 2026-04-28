package security

import (
	"strings"
	"testing"
)

func TestReportExitCode(t *testing.T) {
	cases := []struct {
		name     string
		findings []Finding
		want     int
	}{
		{"empty", nil, 0},
		{"hygiene-only", []Finding{{Severity: SevHygiene}}, 0},
		{"info-only", []Finding{{Severity: SevInfo}}, 0},
		{"important", []Finding{{Severity: SevInfo}, {Severity: SevImportant}}, 1},
		{"critical-wins", []Finding{{Severity: SevImportant}, {Severity: SevCritical}}, 2},
		{"critical-only", []Finding{{Severity: SevCritical}}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := Report{Findings: tc.findings}
			if got := r.ExitCode(); got != tc.want {
				t.Fatalf("ExitCode() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestReportPrintJSON(t *testing.T) {
	r := Report{Findings: []Finding{
		{ID: "f1", Severity: SevCritical, Title: "T1", RemedyRef: "ref"},
		{ID: "f2", Severity: SevHygiene, Title: "T2"},
	}}
	var buf strings.Builder
	if err := r.Print(&buf, PrintOptions{JSON: true}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		`"total": 2`,
		`"exit_code": 2`,
		`"critical": 1`,
		`"hygiene": 1`,
		`"severity": "CRITICAL"`,
		`"severity": "HYGIENE"`,
		`"remedy_ref": "ref"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("JSON output missing %q\n%s", want, out)
		}
	}
}

func TestReportPrintTextSortsBySeverity(t *testing.T) {
	r := Report{Findings: []Finding{
		{ID: "h", Severity: SevHygiene, Title: "Hygiene"},
		{ID: "c", Severity: SevCritical, Title: "Critical"},
		{ID: "i", Severity: SevInfo, Title: "Info"},
	}}
	var buf strings.Builder
	r.Print(&buf, PrintOptions{NoColor: true})
	out := buf.String()
	// Critical should appear before Info, Info before Hygiene.
	cIdx := strings.Index(out, "[CRITICAL]")
	iIdx := strings.Index(out, "[INFO]")
	hIdx := strings.Index(out, "[HYGIENE]")
	if !(cIdx >= 0 && cIdx < iIdx && iIdx < hIdx) {
		t.Fatalf("severity sort wrong: critical@%d info@%d hygiene@%d\n%s", cIdx, iIdx, hIdx, out)
	}
}

func TestReportCounts(t *testing.T) {
	r := Report{Findings: []Finding{
		{Severity: SevCritical}, {Severity: SevCritical},
		{Severity: SevImportant},
		{Severity: SevHygiene},
	}}
	c := r.Counts()
	if c[SevCritical] != 2 || c[SevImportant] != 1 || c[SevInfo] != 0 || c[SevHygiene] != 1 {
		t.Fatalf("counts = %+v", c)
	}
}
