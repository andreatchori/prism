package llm

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultTimeout    = 120 * time.Second
	defaultMaxRetries = 2
)

// Provider is a code-review model backend (Ollama, OpenAI, Anthropic, ...).
type Provider interface {
	// Analyze sends the prompt to the model and returns its textual response.
	Analyze(ctx context.Context, prompt string) (string, error)
	// Name identifies the provider (for logs).
	Name() string
}

// Options configures provider selection. Empty fields fall back to environment
// variables and then to sensible defaults.
type Options struct {
	Provider string
	Model    string
	// Fallback names an optional secondary provider used when the primary one
	// fails (either to construct or at call time). Empty disables fallback.
	Fallback string
}

// New builds a Provider from the given options / environment. API keys are read
// from the environment (OPENAI_API_KEY, ANTHROPIC_API_KEY, AZURE_OPENAI_API_KEY),
// never from config. When a fallback is configured, the returned provider tries
// the primary first and transparently falls back on failure.
func New(opts Options) (Provider, error) {
	provider := firstNonEmpty(opts.Provider, os.Getenv("PRISM_LLM_PROVIDER"), "ollama")
	primary, primaryErr := buildSingle(provider, opts.Model)

	fallbackName := firstNonEmpty(opts.Fallback, os.Getenv("PRISM_LLM_FALLBACK"))
	if fallbackName == "" || strings.EqualFold(fallbackName, provider) {
		return primary, primaryErr
	}

	secondary, secErr := buildSingle(fallbackName, "")
	switch {
	case primaryErr == nil && secErr == nil:
		return &fallbackProvider{primary: primary, secondary: secondary}, nil
	case primaryErr == nil:
		// Fallback unavailable (e.g. missing key): use primary only.
		return primary, nil
	case secErr == nil:
		// Primary unavailable: degrade to the fallback provider directly.
		return secondary, nil
	default:
		return nil, fmt.Errorf("no usable LLM provider: primary %q: %v; fallback %q: %v",
			provider, primaryErr, fallbackName, secErr)
	}
}

// buildSingle constructs one provider without any fallback wrapping.
func buildSingle(provider, model string) (Provider, error) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "ollama":
		return newOllama(model), nil
	case "openai":
		return newOpenAI(model)
	case "anthropic":
		return newAnthropic(model)
	case "azure", "azure-openai", "azure_openai":
		return newAzureOpenAI(model)
	default:
		return nil, fmt.Errorf("unknown LLM provider %q (use ollama|openai|anthropic|azure-openai)", provider)
	}
}

// Analyze keeps backward compatibility: it builds a provider from the
// environment and runs a single analysis.
func Analyze(prompt string) (string, error) {
	p, err := New(Options{})
	if err != nil {
		return "", err
	}
	return p.Analyze(context.Background(), prompt)
}

// withRetry runs fn, retrying transient failures with a linear backoff. The
// number of attempts is controlled by PRISM_LLM_MAX_RETRIES.
func withRetry(ctx context.Context, name string, fn func(context.Context) (string, error)) (string, error) {
	maxRetries := envInt("PRISM_LLM_MAX_RETRIES", defaultMaxRetries)

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(attempt) * time.Second
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}

		out, err := fn(ctx)
		if err == nil {
			return out, nil
		}
		lastErr = err
	}
	return "", fmt.Errorf("%s call failed after %d attempt(s): %w", name, maxRetries+1, lastErr)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return fallback
}
