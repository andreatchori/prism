package reviewer

import (
	"fmt"
	"strings"

	"github.com/andreatchori/prism/internal/config"
	"github.com/andreatchori/prism/internal/llm"
)

// Result holds the review body and whether critical issues were found.
type Result struct {
	Body        string
	HasCritical bool
}

// Review builds the prompt from the config rules and calls Ollama
func Review(diff string, cfg *config.Config) (*Result, error) {
	prompt := buildPrompt(diff, cfg)
	body, err := llm.Analyze(prompt)
	if err != nil {
		return nil, err
	}

	return &Result{
		Body:        body,
		HasCritical: HasCriticalIssues(body),
	}, nil
}

// HasCriticalIssues reports whether the model marked the review as FAIL.
func HasCriticalIssues(body string) bool {
	upper := strings.ToUpper(body)
	if strings.Contains(upper, "PRISM_VERDICT: FAIL") {
		return true
	}
	if strings.Contains(upper, "PRISM_VERDICT: PASS") {
		return false
	}
	// Fallback when the model omits the verdict line
	return strings.Contains(body, "Critical issues") &&
		!strings.Contains(strings.ToLower(body), "no critical")
}

func buildPrompt(diff string, cfg *config.Config) string {
	mustHave := strings.Join(cfg.Rules.MustHave.Items, "\n- ")
	forbidden := strings.Join(cfg.Rules.Forbidden.Items, "\n- ")
	security := strings.Join(cfg.Rules.Security.Items, "\n- ")
	performance := strings.Join(cfg.Rules.Performance.Items, "\n- ")

	var instructions []string
	if cfg.Behavior.SuggestFixes {
		instructions = append(instructions, "Provide corrected code snippets for each issue")
	}
	if cfg.Behavior.PraiseGoodCode {
		instructions = append(instructions, "Praise what is well done")
	} else {
		instructions = append(instructions, "Skip praise; focus on issues only")
	}
	if cfg.Behavior.BlockOnCritical {
		instructions = append(instructions, "Mark PRISM_VERDICT as FAIL if any critical/security issue is found")
	}
	instructions = append(instructions, "Be concise and actionable")

	return fmt.Sprintf(`
You are %s, a code reviewer with a %s tone.
Respond in %s.

## Rules that MUST be respected
- %s

## Forbidden patterns (flag these immediately)
- %s

## Security (highest priority)
- %s

## Performance
- %s

## Instructions
- %s

---
Here is the Pull Request diff to review:

%s

---
Structure your response as:
1. Positives (if applicable)
2. Critical issues (these block the PR)
3. Warnings
4. Suggestions

End your response with exactly one of these lines:
PRISM_VERDICT: PASS
PRISM_VERDICT: FAIL
`,
		cfg.Reviewer.Name,
		cfg.Reviewer.Tone,
		cfg.Reviewer.Language,
		mustHave,
		forbidden,
		security,
		performance,
		strings.Join(instructions, "\n- "),
		diff,
	)
}
