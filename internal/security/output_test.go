package security

import "testing"

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
