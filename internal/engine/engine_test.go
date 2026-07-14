package engine

import "testing"

func TestFormatMarkdownEmpty(t *testing.T) {
	if got := FormatMarkdown(nil); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	if got := FormatMarkdown(&Report{}); got != "" {
		t.Fatalf("expected empty for no findings, got %q", got)
	}
}

func TestFormatMarkdownFindings(t *testing.T) {
	line := uint(12)
	report := &Report{
		HasCritical:   true,
		CriticalCount: 1,
		WarningCount:  0,
		Findings: []Finding{
			{
				Severity: "critical",
				Category: "forbidden",
				Rule:     "No hardcoded secrets",
				File:     "main.go",
				Line:     &line,
				Matched:  `apiKey := "sk-secret"`,
			},
		},
	}

	got := FormatMarkdown(report)
	if !containsAll(got, []string{"Deterministic rules", "CRITICAL", "main.go:12", "No hardcoded secrets"}) {
		t.Fatalf("unexpected markdown:\n%s", got)
	}
}

func containsAll(s string, parts []string) bool {
	for _, p := range parts {
		if !contains(s, p) {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
