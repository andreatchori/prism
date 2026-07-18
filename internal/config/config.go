package config

import (
	"fmt"
	"os"
	"regexp"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	Reviewer Reviewer `toml:"reviewer"`
	Rules    Rules    `toml:"rules"`
	Behavior Behavior `toml:"behavior"`
	LLM      LLM      `toml:"llm"`
	// Suggestions are deterministic, manager-defined auto-fixes applied to added
	// diff lines (regex -> replacement). They require behavior.propose_changes.
	Suggestions []SuggestionRule `toml:"suggestions"`
}

// SuggestionRule is a deterministic proposal: added lines matching Pattern are
// rewritten via Replacement (Go regexp semantics, e.g. $1 backrefs).
type SuggestionRule struct {
	Pattern     string `toml:"pattern"`
	Replacement string `toml:"replacement"`
	Message     string `toml:"message"`
}

type Reviewer struct {
	Name     string `toml:"name"`
	Language string `toml:"language"`
	Tone     string `toml:"tone"`
}

// LLM selects which model backend powers the review. API keys are always read
// from the environment, never from this file.
type LLM struct {
	Provider string `toml:"provider"` // ollama | openai | anthropic | azure-openai
	Model    string `toml:"model"`
	// Fallback names an optional secondary provider used when the primary fails.
	Fallback string `toml:"fallback"`
}

type Rules struct {
	MustHave    RuleSet `toml:"must_have"`
	Forbidden   RuleSet `toml:"forbidden"`
	Security    RuleSet `toml:"security"`
	Performance RuleSet `toml:"performance"`
}

type RuleSet struct {
	Items []string `toml:"items"`
}

type Behavior struct {
	BlockOnCritical bool `toml:"block_on_critical"`
	SuggestFixes    bool `toml:"suggest_fixes"`
	PraiseGoodCode  bool `toml:"praise_good_code"`
	MaxDiffLines    int  `toml:"max_diff_lines"`
	// ProposeChanges, when true, makes Prism emit one-click applicable code
	// suggestions (GitHub "suggested changes") based on the configured rules.
	ProposeChanges bool `toml:"propose_changes"`
}

// Load reads and parses the rules.toml config file
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("could not read config file: %w", err)
	}

	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("could not parse config file: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

// validate checks that the config has the minimum required fields
func (c *Config) validate() error {
	if c.Reviewer.Name == "" {
		return fmt.Errorf("reviewer.name is required")
	}
	if c.Reviewer.Language == "" {
		return fmt.Errorf("reviewer.language is required")
	}
	if len(c.Rules.Forbidden.Items) == 0 && len(c.Rules.MustHave.Items) == 0 {
		return fmt.Errorf("at least one rule must be defined")
	}
	for i, s := range c.Suggestions {
		if s.Pattern == "" {
			return fmt.Errorf("suggestions[%d].pattern is required", i)
		}
		if _, err := regexp.Compile(s.Pattern); err != nil {
			return fmt.Errorf("suggestions[%d].pattern is not a valid regexp: %w", i, err)
		}
	}
	return nil
}
