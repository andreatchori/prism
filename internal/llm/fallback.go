package llm

import (
	"context"
	"log"
)

// fallbackProvider tries the primary provider first and, on failure, transparently
// retries the request with the secondary provider.
type fallbackProvider struct {
	primary   Provider
	secondary Provider
}

func (f *fallbackProvider) Name() string {
	return f.primary.Name() + " (fallback: " + f.secondary.Name() + ")"
}

func (f *fallbackProvider) Analyze(ctx context.Context, prompt string) (string, error) {
	out, err := f.primary.Analyze(ctx, prompt)
	if err == nil {
		return out, nil
	}
	log.Printf("LLM primary provider %s failed, falling back to %s: %v",
		f.primary.Name(), f.secondary.Name(), err)
	return f.secondary.Analyze(ctx, prompt)
}
