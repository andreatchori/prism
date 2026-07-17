package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Finding is a deterministic rule violation from the Rust engine.
type Finding struct {
	Severity string `json:"severity"`
	Category string `json:"category"`
	Rule     string `json:"rule"`
	File     string `json:"file"`
	Line     *uint  `json:"line"`
	Matched  string `json:"matched"`
}

// Report is the JSON report produced by `prism check --json --stdin`.
type Report struct {
	Files         int       `json:"files"`
	AddedLines    int       `json:"added_lines"`
	RemovedLines  int       `json:"removed_lines"`
	HasCritical   bool      `json:"has_critical"`
	CriticalCount int       `json:"critical_count"`
	WarningCount  int       `json:"warning_count"`
	Findings      []Finding `json:"findings"`
}

// Available reports whether a Rust engine binary can be located.
func Available() bool {
	_, err := resolveBinary()
	return err == nil
}

// AnalyzeDiff runs the Rust rules engine against a unified diff.
// If the binary is missing, returns (nil, nil) so callers can fall back to LLM-only.
func AnalyzeDiff(diff, configPath string) (*Report, error) {
	bin, err := resolveBinary()
	if err != nil {
		return nil, nil
	}

	if strings.TrimSpace(diff) == "" {
		return &Report{}, nil
	}

	args := []string{"check", "--stdin", "--json"}
	if configPath != "" {
		args = append(args, "--config", configPath)
	}

	cmd := exec.Command(bin, args...)
	cmd.Stdin = strings.NewReader(diff)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("rust engine failed: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}

	var report Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		return nil, fmt.Errorf("invalid engine JSON: %w (stdout=%q)", err, stdout.String())
	}
	return &report, nil
}

// FormatMarkdown renders deterministic findings for a PR comment.
func FormatMarkdown(report *Report) string {
	if report == nil || len(report.Findings) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("### Deterministic rules (Rust engine)\n\n")
	for _, f := range report.Findings {
		loc := f.File
		if f.Line != nil {
			loc = fmt.Sprintf("%s:%d", f.File, *f.Line)
		}
		b.WriteString(fmt.Sprintf("- **[%s]** (%s) %s `@ %s`\n", strings.ToUpper(f.Severity), f.Category, f.Rule, loc))
		b.WriteString(fmt.Sprintf("  - matched: `%s`\n", strings.TrimSpace(f.Matched)))
	}
	b.WriteString(fmt.Sprintf("\nSummary: %d critical, %d warning(s)\n", report.CriticalCount, report.WarningCount))
	return b.String()
}

func resolveBinary() (string, error) {
	if custom := os.Getenv("PRISM_ENGINE"); custom != "" {
		if info, err := os.Stat(custom); err == nil && !info.IsDir() {
			return custom, nil
		}
		return "", fmt.Errorf("PRISM_ENGINE not found: %s", custom)
	}

	candidates := []string{
		filepath.Join("rust", "target", "release", binaryName()),
		filepath.Join("rust", "target", "debug", binaryName()),
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, binaryName()),
			filepath.Join(dir, "rust", "target", "release", binaryName()),
		)
	}

	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			abs, err := filepath.Abs(c)
			if err != nil {
				return c, nil
			}
			return abs, nil
		}
	}

	if path, err := exec.LookPath(binaryName()); err == nil {
		return path, nil
	}

	return "", fmt.Errorf("rust engine binary not found (set PRISM_ENGINE)")
}

func binaryName() string {
	if runtime.GOOS == "windows" {
		return "prism.exe"
	}
	return "prism"
}
