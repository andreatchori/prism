package reviewer

import (
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"

	"github.com/andreatchori/prism/internal/config"
)

// addedLine is a line introduced by the diff, with its new-file line number.
type addedLine struct {
	File    string
	Line    uint
	Content string
}

var hunkHeaderRe = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// parseAddedLines walks a unified diff and returns every added ('+') line with
// its path and new-file line number. It ignores the "+++" file header lines.
func parseAddedLines(diff string) []addedLine {
	var out []addedLine
	var file string
	var newLine uint

	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++ "):
			file = normalizeDiffPath(strings.TrimPrefix(line, "+++ "))
		case strings.HasPrefix(line, "--- "):
			// old-file header; nothing to do
		case strings.HasPrefix(line, "@@"):
			if m := hunkHeaderRe.FindStringSubmatch(line); m != nil {
				if n, err := strconv.Atoi(m[1]); err == nil {
					newLine = uint(n)
				}
			}
		case strings.HasPrefix(line, "+"):
			if file != "" && newLine > 0 {
				out = append(out, addedLine{File: file, Line: newLine, Content: line[1:]})
			}
			newLine++
		case strings.HasPrefix(line, "-"):
			// removed line: does not advance the new-file counter
		case strings.HasPrefix(line, "\\"):
			// "\ No newline at end of file": ignore
		default:
			// context line
			if newLine > 0 {
				newLine++
			}
		}
	}
	return out
}

// addedLineSet indexes added lines by file and new-file line number.
func addedLineSet(diff string) map[string]map[uint]bool {
	set := make(map[string]map[uint]bool)
	for _, a := range parseAddedLines(diff) {
		if set[a.File] == nil {
			set[a.File] = make(map[uint]bool)
		}
		set[a.File][a.Line] = true
	}
	return set
}

// deterministicSuggestions applies the manager-defined regex->replacement rules
// to every added line, producing rule-based proposals without the LLM.
func deterministicSuggestions(diff string, cfg *config.Config) []Suggestion {
	if len(cfg.Suggestions) == 0 {
		return nil
	}

	type compiled struct {
		re   *regexp.Regexp
		repl string
		msg  string
	}
	rules := make([]compiled, 0, len(cfg.Suggestions))
	for _, s := range cfg.Suggestions {
		re, err := regexp.Compile(s.Pattern)
		if err != nil {
			log.Printf("Skipping invalid suggestion pattern %q: %v", s.Pattern, err)
			continue
		}
		rules = append(rules, compiled{re: re, repl: s.Replacement, msg: s.Message})
	}

	var out []Suggestion
	for _, a := range parseAddedLines(diff) {
		for _, r := range rules {
			if !r.re.MatchString(a.Content) {
				continue
			}
			fixed := r.re.ReplaceAllString(a.Content, r.repl)
			if fixed == a.Content {
				continue
			}
			rationale := r.msg
			if rationale == "" {
				rationale = "matches a configured suggestion rule"
			}
			out = append(out, Suggestion{
				File:      a.File,
				Line:      a.Line,
				EndLine:   a.Line,
				Code:      fixed,
				Rationale: rationale,
			})
			break // one suggestion per line
		}
	}
	return out
}

// validateSuggestions drops LLM suggestions that don't map onto added diff lines,
// which prevents proposals from landing on the wrong (or non-existent) line.
func validateSuggestions(suggestions []Suggestion, diff string) []Suggestion {
	if len(suggestions) == 0 {
		return nil
	}
	set := addedLineSet(diff)

	var out []Suggestion
	for _, s := range suggestions {
		lines, ok := set[s.File]
		if !ok || !lines[s.Line] {
			log.Printf("Dropping suggestion for %s:%d (not an added diff line)", s.File, s.Line)
			continue
		}
		if s.EndLine > s.Line && !lines[s.EndLine] {
			log.Printf("Dropping multi-line suggestion for %s:%d-%d (end not an added diff line)", s.File, s.Line, s.EndLine)
			continue
		}
		out = append(out, s)
	}
	return out
}

// mergeSuggestions combines deterministic and LLM suggestions, de-duplicating by
// file+line and giving precedence to deterministic proposals.
func mergeSuggestions(deterministic, llm []Suggestion) []Suggestion {
	seen := make(map[string]bool)
	var out []Suggestion
	for _, s := range deterministic {
		key := fmt.Sprintf("%s:%d", s.File, s.Line)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	for _, s := range llm {
		key := fmt.Sprintf("%s:%d", s.File, s.Line)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	return out
}

// normalizeDiffPath strips the "b/" prefix and any trailing tab-delimited
// metadata from a diff file header.
func normalizeDiffPath(p string) string {
	p = strings.TrimSpace(p)
	if i := strings.IndexByte(p, '\t'); i >= 0 {
		p = p[:i]
	}
	p = strings.TrimPrefix(p, "b/")
	return p
}
