package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Overlay holds optional per-repo overrides. Pointer / nil sections mean "keep
// the base value". Rule sets and suggestions replace the base when present.
type Overlay struct {
	Reviewer    *ReviewerOverlay  `toml:"reviewer"`
	Rules       *RulesOverlay     `toml:"rules"`
	Behavior    *BehaviorOverlay  `toml:"behavior"`
	LLM         *LLMOverlay       `toml:"llm"`
	Suggestions *[]SuggestionRule `toml:"suggestions"`
}

type ReviewerOverlay struct {
	Name     *string `toml:"name"`
	Language *string `toml:"language"`
	Tone     *string `toml:"tone"`
}

type RulesOverlay struct {
	MustHave    *RuleSet `toml:"must_have"`
	Forbidden   *RuleSet `toml:"forbidden"`
	Security    *RuleSet `toml:"security"`
	Performance *RuleSet `toml:"performance"`
}

type BehaviorOverlay struct {
	BlockOnCritical *bool `toml:"block_on_critical"`
	SuggestFixes    *bool `toml:"suggest_fixes"`
	PraiseGoodCode  *bool `toml:"praise_good_code"`
	MaxDiffLines    *int  `toml:"max_diff_lines"`
	ProposeChanges  *bool `toml:"propose_changes"`
}

type LLMOverlay struct {
	Provider *string `toml:"provider"`
	Model    *string `toml:"model"`
	Fallback *string `toml:"fallback"`
}

// LoadOverlay reads a partial TOML override file. An empty/missing file is an error.
func LoadOverlay(path string) (*Overlay, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ov Overlay
	if err := toml.Unmarshal(data, &ov); err != nil {
		return nil, fmt.Errorf("could not parse overlay %s: %w", path, err)
	}
	return &ov, nil
}

// Merge returns a deep copy of base with overlay applied. Base is never mutated.
func Merge(base *Config, ov *Overlay) *Config {
	if base == nil {
		return nil
	}
	out := cloneConfig(base)
	if ov == nil {
		return out
	}

	if ov.Reviewer != nil {
		if ov.Reviewer.Name != nil {
			out.Reviewer.Name = *ov.Reviewer.Name
		}
		if ov.Reviewer.Language != nil {
			out.Reviewer.Language = *ov.Reviewer.Language
		}
		if ov.Reviewer.Tone != nil {
			out.Reviewer.Tone = *ov.Reviewer.Tone
		}
	}

	if ov.Rules != nil {
		if ov.Rules.MustHave != nil {
			out.Rules.MustHave = cloneRuleSet(*ov.Rules.MustHave)
		}
		if ov.Rules.Forbidden != nil {
			out.Rules.Forbidden = cloneRuleSet(*ov.Rules.Forbidden)
		}
		if ov.Rules.Security != nil {
			out.Rules.Security = cloneRuleSet(*ov.Rules.Security)
		}
		if ov.Rules.Performance != nil {
			out.Rules.Performance = cloneRuleSet(*ov.Rules.Performance)
		}
	}

	if ov.Behavior != nil {
		if ov.Behavior.BlockOnCritical != nil {
			out.Behavior.BlockOnCritical = *ov.Behavior.BlockOnCritical
		}
		if ov.Behavior.SuggestFixes != nil {
			out.Behavior.SuggestFixes = *ov.Behavior.SuggestFixes
		}
		if ov.Behavior.PraiseGoodCode != nil {
			out.Behavior.PraiseGoodCode = *ov.Behavior.PraiseGoodCode
		}
		if ov.Behavior.MaxDiffLines != nil {
			out.Behavior.MaxDiffLines = *ov.Behavior.MaxDiffLines
		}
		if ov.Behavior.ProposeChanges != nil {
			out.Behavior.ProposeChanges = *ov.Behavior.ProposeChanges
		}
	}

	if ov.LLM != nil {
		if ov.LLM.Provider != nil {
			out.LLM.Provider = *ov.LLM.Provider
		}
		if ov.LLM.Model != nil {
			out.LLM.Model = *ov.LLM.Model
		}
		if ov.LLM.Fallback != nil {
			out.LLM.Fallback = *ov.LLM.Fallback
		}
	}

	if ov.Suggestions != nil {
		out.Suggestions = append([]SuggestionRule(nil), (*ov.Suggestions)...)
	}

	return out
}

func cloneConfig(c *Config) *Config {
	out := *c
	out.Rules.MustHave = cloneRuleSet(c.Rules.MustHave)
	out.Rules.Forbidden = cloneRuleSet(c.Rules.Forbidden)
	out.Rules.Security = cloneRuleSet(c.Rules.Security)
	out.Rules.Performance = cloneRuleSet(c.Rules.Performance)
	out.Suggestions = append([]SuggestionRule(nil), c.Suggestions...)
	return &out
}

func cloneRuleSet(r RuleSet) RuleSet {
	return RuleSet{Items: append([]string(nil), r.Items...)}
}

var unsafeFileChars = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// RepoFileName turns a repo key like "github:acme/api" into a safe filename
// stem "github__acme__api" (without .toml).
func RepoFileName(repoKey string) string {
	key := strings.TrimSpace(strings.ToLower(repoKey))
	key = strings.ReplaceAll(key, ":", "__")
	key = strings.ReplaceAll(key, "/", "__")
	key = unsafeFileChars.ReplaceAllString(key, "_")
	key = strings.Trim(key, "_")
	if key == "" {
		return "unknown"
	}
	return key
}

// ReposDir returns the directory that holds per-repo overlays.
func ReposDir() string {
	if d := os.Getenv("PRISM_REPOS_DIR"); d != "" {
		return d
	}
	return "config/repos"
}

// Resolve applies an optional per-repo overlay on top of base.
// repoKey examples: "github:acme/api", "gitlab:group/project", "azure:org/project/repo",
// "bitbucket:workspace/slug". Returns the effective config, whether an overlay
// was applied, and the overlay path used (empty when none).
func Resolve(base *Config, repoKey string) (cfg *Config, overlayPath string, err error) {
	if base == nil {
		return nil, "", fmt.Errorf("base config is nil")
	}
	if strings.TrimSpace(repoKey) == "" {
		return cloneConfig(base), "", nil
	}

	path := filepath.Join(ReposDir(), RepoFileName(repoKey)+".toml")
	ov, err := LoadOverlay(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cloneConfig(base), "", nil
		}
		return nil, path, fmt.Errorf("load overlay %s: %w", path, err)
	}

	merged := Merge(base, ov)
	if err := merged.validate(); err != nil {
		return nil, path, fmt.Errorf("merged config for %s invalid: %w", repoKey, err)
	}
	return merged, path, nil
}
