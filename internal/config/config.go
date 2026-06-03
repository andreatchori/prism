package config

import (
	"fmt"
	"os"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	Reviewer Reviewer `toml:"reviewer"`
	Rules    Rules    `toml:"rules"`
	Behavior Behavior `toml:"behavior"`
}

type Reviewer struct {
	Name     string `toml:"name"`
	Language string `toml:"language"`
	Tone     string `toml:"tone"`
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
	return nil
}
