package reviewer

import "testing"

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
