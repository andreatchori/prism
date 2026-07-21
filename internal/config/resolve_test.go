package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRepoFileName(t *testing.T) {
	cases := map[string]string{
		"github:acme/api":          "github__acme__api",
		"gitlab:group/sub/project": "gitlab__group__sub__project",
		"azure:org/proj/repo-guid": "azure__org__proj__repo-guid",
		"bitbucket:team/sandbox":   "bitbucket__team__sandbox",
		"GitHub:Acme/API":          "github__acme__api",
	}
	for in, want := range cases {
		if got := RepoFileName(in); got != want {
			t.Errorf("RepoFileName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMergeOverlay(t *testing.T) {
	base := &Config{
		Reviewer: Reviewer{Name: "Prism Bot", Language: "en", Tone: "strict"},
		Rules: Rules{
			MustHave:  RuleSet{Items: []string{"base must"}},
			Forbidden: RuleSet{Items: []string{"base forbid"}},
		},
		Behavior: Behavior{BlockOnCritical: true, ProposeChanges: true, MaxDiffLines: 1000},
		LLM:      LLM{Provider: "ollama", Model: "local"},
	}

	name := "Team Bot"
	provider := "anthropic"
	model := "claude"
	propose := false
	forbid := RuleSet{Items: []string{"repo forbid"}}
	ov := &Overlay{
		Reviewer: &ReviewerOverlay{Name: &name},
		Rules:    &RulesOverlay{Forbidden: &forbid},
		Behavior: &BehaviorOverlay{ProposeChanges: &propose},
		LLM:      &LLMOverlay{Provider: &provider, Model: &model},
	}

	merged := Merge(base, ov)
	if merged.Reviewer.Name != "Team Bot" || merged.Reviewer.Language != "en" {
		t.Errorf("reviewer merge unexpected: %+v", merged.Reviewer)
	}
	if len(merged.Rules.MustHave.Items) != 1 || merged.Rules.MustHave.Items[0] != "base must" {
		t.Errorf("must_have should stay from base: %+v", merged.Rules.MustHave)
	}
	if len(merged.Rules.Forbidden.Items) != 1 || merged.Rules.Forbidden.Items[0] != "repo forbid" {
		t.Errorf("forbidden should be replaced: %+v", merged.Rules.Forbidden)
	}
	if merged.Behavior.ProposeChanges || !merged.Behavior.BlockOnCritical {
		t.Errorf("behavior merge unexpected: %+v", merged.Behavior)
	}
	if merged.LLM.Provider != "anthropic" || merged.LLM.Model != "claude" {
		t.Errorf("llm merge unexpected: %+v", merged.LLM)
	}
	// Base must not be mutated
	if base.Reviewer.Name != "Prism Bot" || base.LLM.Provider != "ollama" {
		t.Error("base config was mutated")
	}
}

func TestResolveUsesOverlay(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRISM_REPOS_DIR", dir)

	base := &Config{
		Reviewer: Reviewer{Name: "Prism Bot", Language: "en"},
		Rules:    Rules{MustHave: RuleSet{Items: []string{"have something"}}},
		LLM:      LLM{Provider: "ollama"},
	}

	overlayPath := filepath.Join(dir, RepoFileName("github:acme/api")+".toml")
	content := `
[llm]
provider = "openai"
model = "gpt-4o-mini"

[behavior]
propose_changes = false
`
	if err := os.WriteFile(overlayPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, path, err := Resolve(base, "github:acme/api")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if path != overlayPath {
		t.Errorf("overlay path = %q, want %q", path, overlayPath)
	}
	if cfg.LLM.Provider != "openai" || cfg.LLM.Model != "gpt-4o-mini" {
		t.Errorf("llm = %+v", cfg.LLM)
	}
	if cfg.Behavior.ProposeChanges {
		t.Error("expected propose_changes=false from overlay")
	}
	if cfg.Reviewer.Name != "Prism Bot" {
		t.Error("reviewer should come from base")
	}
}

func TestResolveMissingOverlay(t *testing.T) {
	t.Setenv("PRISM_REPOS_DIR", t.TempDir())
	base := &Config{
		Reviewer: Reviewer{Name: "Prism Bot", Language: "en"},
		Rules:    Rules{Forbidden: RuleSet{Items: []string{"no secrets"}}},
	}
	cfg, path, err := Resolve(base, "github:no/such")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if path != "" {
		t.Errorf("expected no overlay path, got %q", path)
	}
	if cfg.Reviewer.Name != "Prism Bot" {
		t.Errorf("unexpected cfg: %+v", cfg.Reviewer)
	}
}

func TestWriteTempRoundTrip(t *testing.T) {
	cfg := &Config{
		Reviewer: Reviewer{Name: "Prism Bot", Language: "en", Tone: "friendly"},
		Rules:    Rules{MustHave: RuleSet{Items: []string{"doc comments"}}},
		Behavior: Behavior{BlockOnCritical: true, MaxDiffLines: 500},
		LLM:      LLM{Provider: "ollama", Model: "tiny"},
	}
	path, err := WriteTemp(cfg)
	if err != nil {
		t.Fatalf("WriteTemp: %v", err)
	}
	defer os.Remove(path)

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load temp: %v", err)
	}
	if loaded.Reviewer.Name != "Prism Bot" || loaded.LLM.Model != "tiny" {
		t.Errorf("round-trip mismatch: %+v", loaded)
	}
}
