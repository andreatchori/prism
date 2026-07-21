package reviewer

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/andreatchori/prism/internal/config"
	"github.com/andreatchori/prism/internal/engine"
	"github.com/andreatchori/prism/internal/llm"
)

const (
	suggestionsStartMarker = "PRISM_SUGGESTIONS_START"
	suggestionsEndMarker   = "PRISM_SUGGESTIONS_END"
)

// Suggestion is a rule-based, one-click applicable code change proposed for a
// specific file and line range.
type Suggestion struct {
	File      string `json:"file"`
	Line      uint   `json:"line"`
	EndLine   uint   `json:"end_line"`
	Code      string `json:"code"`
	Rationale string `json:"rationale"`
}

// Result holds the review body and whether critical issues were found.
type Result struct {
	Body        string
	HasCritical bool
	// Findings are the deterministic engine findings (with file/line), usable
	// for inline review comments. Empty when the engine is unavailable.
	Findings []engine.Finding
	// Suggestions are rule-based code proposals (opt-in via propose_changes).
	Suggestions []Suggestion
}

// Review runs the Rust deterministic engine first, then the configured LLM.
// The effective cfg (possibly a per-repo merge) is written to a temp file so the
// Rust engine sees the same rules as the Go reviewer.
func Review(diff string, cfg *config.Config) (*Result, error) {
	configPath, err := config.WriteTemp(cfg)
	if err != nil {
		log.Printf("Could not write temp rules for Rust engine: %v", err)
		configPath = os.Getenv("PRISM_CONFIG")
		if configPath == "" {
			configPath = "config/examples/rules.toml"
		}
	} else {
		defer os.Remove(configPath)
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

	var findings []engine.Finding
	if engineReport != nil {
		findings = engineReport.Findings
	}

	// Deterministic, rule-based suggestions can be produced without the LLM.
	var deterministic []Suggestion
	if cfg.Behavior.ProposeChanges {
		deterministic = deterministicSuggestions(diff, cfg)
	}

	prompt := buildPrompt(diff, cfg, engineReport)

	var llmBody string
	provider, err := llm.New(llm.Options{Provider: cfg.LLM.Provider, Model: cfg.LLM.Model, Fallback: cfg.LLM.Fallback})
	if err == nil {
		log.Printf("LLM provider: %s", provider.Name())
		llmBody, err = provider.Analyze(context.Background(), prompt)
	} else {
		log.Printf("LLM provider unavailable: %v", err)
	}
	if err != nil {
		// If LLM fails but we have deterministic findings, still return them
		if engineReport != nil && len(engineReport.Findings) > 0 {
			body := engine.FormatMarkdown(engineReport)
			body += "\n\nPRISM_VERDICT: FAIL\n"
			return &Result{Body: body, HasCritical: engineReport.HasCritical, Findings: findings, Suggestions: deterministic}, nil
		}
		if len(deterministic) > 0 {
			return &Result{Body: engine.FormatMarkdown(engineReport), HasCritical: false, Findings: findings, Suggestions: deterministic}, nil
		}
		return nil, err
	}

	var suggestions []Suggestion
	if cfg.Behavior.ProposeChanges {
		var llmSuggestions []Suggestion
		llmSuggestions, llmBody = extractSuggestions(llmBody)
		llmSuggestions = validateSuggestions(llmSuggestions, diff)
		suggestions = mergeSuggestions(deterministic, llmSuggestions)
		if len(suggestions) > 0 {
			log.Printf("Reviewer: %d rule-based suggestion(s) proposed (%d deterministic, %d from LLM)",
				len(suggestions), len(deterministic), len(llmSuggestions))
		}
	}

	body := mergeBodies(engine.FormatMarkdown(engineReport), llmBody)
	hasCritical := HasCriticalIssues(llmBody)
	if engineReport != nil && engineReport.HasCritical {
		hasCritical = true
	}

	return &Result{
		Body:        body,
		HasCritical: hasCritical,
		Findings:    findings,
		Suggestions: suggestions,
	}, nil
}

// extractSuggestions parses the machine-readable suggestions block (JSONL between
// markers) and returns the suggestions plus the body with that block removed.
func extractSuggestions(body string) ([]Suggestion, string) {
	start := strings.Index(body, suggestionsStartMarker)
	if start == -1 {
		return nil, body
	}
	end := strings.Index(body, suggestionsEndMarker)
	if end == -1 || end < start {
		return nil, body
	}

	block := body[start+len(suggestionsStartMarker) : end]
	cleaned := strings.TrimSpace(body[:start]) + "\n" + strings.TrimSpace(body[end+len(suggestionsEndMarker):])

	var suggestions []Suggestion
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var s Suggestion
		if err := json.Unmarshal([]byte(line), &s); err != nil {
			log.Printf("Reviewer: skipping malformed suggestion: %v", err)
			continue
		}
		if s.File == "" || s.Line == 0 || strings.TrimSpace(s.Code) == "" {
			continue
		}
		if s.EndLine == 0 || s.EndLine < s.Line {
			s.EndLine = s.Line
		}
		suggestions = append(suggestions, s)
	}
	return suggestions, strings.TrimSpace(cleaned)
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

	suggestionsSection := ""
	if cfg.Behavior.ProposeChanges {
		suggestionsSection = fmt.Sprintf(`

## Proposed changes (rule-based)
When a rule above is violated and you can fix it, propose a concrete replacement.
After the verdict line, output a machine-readable block, one compact JSON object
per line, between the markers below. Only include lines that appear as added
(prefixed with '+') in the diff. Use the file path and the new-file line number.
The "code" field must contain the full replacement for the given line range,
without the leading '+'. Keep suggestions minimal and directly tied to a rule.

%s
{"file":"path/to/file","line":12,"end_line":12,"code":"fixed line here","rationale":"which rule"}
%s

If you have no concrete fix, output the two markers with nothing between them.`,
			suggestionsStartMarker, suggestionsEndMarker)
	}

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
%s
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
		suggestionsSection,
	)
}
