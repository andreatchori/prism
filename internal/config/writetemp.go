package config

import (
	"fmt"
	"os"

	"github.com/pelletier/go-toml/v2"
)

// WriteTemp marshals cfg to a temporary TOML file and returns its path.
// The caller must remove the file when done (e.g. defer os.Remove(path)).
func WriteTemp(cfg *Config) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("config is nil")
	}
	data, err := toml.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshal config: %w", err)
	}
	f, err := os.CreateTemp("", "prism-rules-*.toml")
	if err != nil {
		return "", err
	}
	path := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}
