package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadValidConfig(t *testing.T) {
	path := filepath.Join("..", "..", "config", "examples", "rules.toml")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Reviewer.Name != "Prism Bot" {
		t.Errorf("Reviewer.Name = %q, want Prism Bot", cfg.Reviewer.Name)
	}
	if len(cfg.Rules.MustHave.Items) == 0 {
		t.Error("expected must_have rules")
	}
	if !cfg.Behavior.BlockOnCritical {
		t.Error("expected block_on_critical = true")
	}
}

func TestLoadMissingName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.toml")
	content := `
[reviewer]
name = ""
language = "en"

[rules.must_have]
items = ["something"]
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for empty reviewer.name")
	}
}

func TestLoadNoRules(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "norules.toml")
	content := `
[reviewer]
name = "Bot"
language = "en"

[rules.must_have]
items = []

[rules.forbidden]
items = []
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error when no rules are defined")
	}
}
