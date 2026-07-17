package reviewer

import (
	"strings"
	"testing"
)

func TestExtractSuggestions(t *testing.T) {
	body := `Some review text.

PRISM_VERDICT: FAIL
PRISM_SUGGESTIONS_START
{"file":"main.go","line":12,"end_line":12,"code":"fmt.Println(x)","rationale":"no debug print"}
{"file":"util.go","line":3,"end_line":5,"code":"a\nb\nc","rationale":"multi"}
garbage line that is not json
{"file":"","line":1,"code":"skip"}
PRISM_SUGGESTIONS_END`

	suggestions, cleaned := extractSuggestions(body)
	if len(suggestions) != 2 {
		t.Fatalf("expected 2 suggestions, got %d", len(suggestions))
	}
	if suggestions[0].File != "main.go" || suggestions[0].Line != 12 {
		t.Errorf("unexpected first suggestion: %+v", suggestions[0])
	}
	if suggestions[1].EndLine != 5 || suggestions[1].Code != "a\nb\nc" {
		t.Errorf("unexpected second suggestion: %+v", suggestions[1])
	}
	if strings.Contains(cleaned, "PRISM_SUGGESTIONS_START") || strings.Contains(cleaned, "main.go") {
		t.Errorf("cleaned body should not contain the suggestions block: %q", cleaned)
	}
	if !strings.Contains(cleaned, "Some review text.") {
		t.Errorf("cleaned body should keep the review text: %q", cleaned)
	}
}

func TestExtractSuggestionsNoBlock(t *testing.T) {
	body := "Just a normal review.\nPRISM_VERDICT: PASS"
	suggestions, cleaned := extractSuggestions(body)
	if suggestions != nil {
		t.Errorf("expected no suggestions, got %+v", suggestions)
	}
	if cleaned != body {
		t.Errorf("body should be unchanged, got %q", cleaned)
	}
}

func TestHasCriticalIssues(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{"explicit fail", "Looks bad\nPRISM_VERDICT: FAIL\n", true},
		{"explicit pass", "All good\nPRISM_VERDICT: PASS\n", false},
		{"fail case insensitive", "PRISM_VERDICT: fail", true},
		{"fallback critical", "## Critical issues\n- secret found", true},
		{"fallback no critical", "No critical issues found.\nJust warnings.", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasCriticalIssues(tt.body); got != tt.want {
				t.Errorf("HasCriticalIssues() = %v, want %v", got, tt.want)
			}
		})
	}
}
