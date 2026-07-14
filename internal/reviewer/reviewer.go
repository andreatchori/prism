package reviewer

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/andreatchori/prism/internal/config"
	"github.com/andreatchori/prism/internal/engine"
	"github.com/andreatchori/prism/internal/llm"
)

// Result holds the review body and whether critical issues were found.
type Result struct {
	Body        string
	HasCritical bool
}

// Review runs the Rust deterministic engine first, then Ollama for deeper analysis.
func Review(diff string, cfg *config.Config) (*Result, error) {
	configPath := os.Getenv("PRISM_CONFIG")
	if configPath == "" {
		configPath = "config/examples/rules.toml"
	}

	var engineReport *engine.Report
	if engine.Available() {
		report, err := engine.AnalyzeDiff(diff, configPath)
		if err != nil {
			log.Printf("Rust engine error (continuing with LLM only): %v", err)
		} else {
			engineReport = report
			log.Printf(
				"Rust engine: %d finding(s) (%d critical, %d warning)",
				len(report.Findings),
				report.CriticalCount,
				report.WarningCount,
			)
		}
	} else {
		log.Printf("Rust engine not available - LLM-only review (set PRISM_ENGINE to enable)")
	}

	prompt := buildPrompt(diff, cfg, engineReport)
	llmBody, err := llm.Analyze(prompt)
	if err != nil {
		// If LLM fails but we have deterministic findings, still return them
		if engineReport != nil && len(engineReport.Findings) > 0 {
			body := engine.FormatMarkdown(engineReport)
			body += "\n\nPRISM_VERDICT: FAIL\n"
			return &Result{Body: body, HasCritical: engineReport.HasCritical}, nil
		}
		return nil, err
	}

	body := mergeBodies(engine.FormatMarkdown(engineReport), llmBody)
	hasCritical := HasCriticalIssues(llmBody)
	if engineReport != nil && engineReport.HasCritical {
		hasCritical = true
	}

	return &Result{
		Body:        body,
		HasCritical: hasCritical,
	}, nil
}

func mergeBodies(engineSection, llmBody string) string {
	engineSection = strings.TrimSpace(engineSection)
	llmBody = strings.TrimSpace(llmBody)
	if engineSection == "" {
		return llmBody
	}
	if llmBody == "" {
		return engineSection
	}
	return engineSection + "\n\n---\n\n### LLM review (Ollama)\n\n" + llmBody
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

func buildPrompt(diff string, cfg *config.Config, engineReport *engine.Report) string {
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

	engineHint := "No deterministic findings were pre-reported."
	if engineReport != nil && len(engineReport.Findings) > 0 {
		engineHint = fmt.Sprintf(
			"A deterministic rules engine already found %d issue(s) (%d critical). Confirm them, do not contradict clear matches, and focus on anything it may have missed.\n\n%s",
			len(engineReport.Findings),
			engineReport.CriticalCount,
			engine.FormatMarkdown(engineReport),
		)
	}

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

## Pre-computed deterministic findings
%s

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
		engineHint,
		diff,
	)
}
