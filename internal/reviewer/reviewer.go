package reviewer

import (
	"fmt"
	"strings"

	"github.com/andreatchori/prism/internal/config"
	"github.com/andreatchori/prism/internal/llm"
)

// Review builds the prompt from the config rules and calls Ollama
func Review(diff string, cfg *config.Config) (string, error) {
	prompt := buildPrompt(diff, cfg)
	return llm.Analyze(prompt)
}

func buildPrompt(diff string, cfg *config.Config) string {
	mustHave := strings.Join(cfg.Rules.MustHave.Items, "\n- ")
	forbidden := strings.Join(cfg.Rules.Forbidden.Items, "\n- ")
	security := strings.Join(cfg.Rules.Security.Items, "\n- ")
	performance := strings.Join(cfg.Rules.Performance.Items, "\n- ")

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
- If suggest_fixes is enabled, provide corrected code snippets
- Praise what is well done
- Be concise and actionable

---
Here is the Pull Request diff to review:

%s

---
Structure your response as:
1. ✅ Positives
2. 🚨 Critical issues (blocks the PR)
3. ⚠️  Warnings
4. 💡 Suggestions
`,
		cfg.Reviewer.Name,
		cfg.Reviewer.Tone,
		cfg.Reviewer.Language,
		mustHave,
		forbidden,
		security,
		performance,
		diff,
	)
}
