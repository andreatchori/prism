package reviewer

import (
	"testing"

	"github.com/andreatchori/prism/internal/config"
)

const sampleDiff = `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,3 +1,5 @@
 package main
+import "fmt"
+
 func main() {
+	fmt.Println("hello")
 }
`

func TestParseAddedLines(t *testing.T) {
	added := parseAddedLines(sampleDiff)
	if len(added) != 3 {
		t.Fatalf("expected 3 added lines, got %d: %+v", len(added), added)
	}
	if added[0].File != "main.go" || added[0].Line != 2 || added[0].Content != `import "fmt"` {
		t.Errorf("unexpected first added line: %+v", added[0])
	}
	// "fmt.Println" line is the 5th line in the new file
	last := added[len(added)-1]
	if last.Line != 5 {
		t.Errorf("expected last added line at 5, got %d", last.Line)
	}
}

func TestAddedLineSet(t *testing.T) {
	set := addedLineSet(sampleDiff)
	if !set["main.go"][2] || !set["main.go"][5] {
		t.Errorf("expected added lines 2 and 5 in set, got %+v", set)
	}
	if set["main.go"][1] {
		t.Error("context line 1 should not be in the added set")
	}
}

func TestDeterministicSuggestions(t *testing.T) {
	cfg := &config.Config{
		Suggestions: []config.SuggestionRule{
			{Pattern: `fmt\.Println\(`, Replacement: "log.Println(", Message: "use logger"},
		},
	}
	out := deterministicSuggestions(sampleDiff, cfg)
	if len(out) != 1 {
		t.Fatalf("expected 1 deterministic suggestion, got %d: %+v", len(out), out)
	}
	if out[0].File != "main.go" || out[0].Line != 5 {
		t.Errorf("unexpected suggestion target: %+v", out[0])
	}
	if out[0].Code != "\tlog.Println(\"hello\")" {
		t.Errorf("unexpected fixed code: %q", out[0].Code)
	}
	if out[0].Rationale != "use logger" {
		t.Errorf("unexpected rationale: %q", out[0].Rationale)
	}
}

func TestValidateSuggestions(t *testing.T) {
	suggestions := []Suggestion{
		{File: "main.go", Line: 5, EndLine: 5, Code: "x"},  // valid (added line)
		{File: "main.go", Line: 1, EndLine: 1, Code: "x"},  // invalid (context line)
		{File: "other.go", Line: 5, EndLine: 5, Code: "x"}, // invalid (unknown file)
	}
	out := validateSuggestions(suggestions, sampleDiff)
	if len(out) != 1 {
		t.Fatalf("expected 1 valid suggestion, got %d: %+v", len(out), out)
	}
	if out[0].Line != 5 {
		t.Errorf("expected the added-line suggestion to survive, got %+v", out[0])
	}
}

func TestMergeSuggestions(t *testing.T) {
	det := []Suggestion{{File: "a.go", Line: 1, Code: "det"}}
	llm := []Suggestion{
		{File: "a.go", Line: 1, Code: "llm-dup"}, // dropped, same key as deterministic
		{File: "a.go", Line: 2, Code: "llm-new"},
	}
	out := mergeSuggestions(det, llm)
	if len(out) != 2 {
		t.Fatalf("expected 2 merged suggestions, got %d", len(out))
	}
	if out[0].Code != "det" {
		t.Errorf("deterministic suggestion should take precedence, got %q", out[0].Code)
	}
}
