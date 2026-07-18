package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

const (
	defaultAnthropicModel     = "claude-3-5-sonnet-latest"
	defaultAnthropicMaxTokens = 4096
	anthropicVersion          = "2023-06-01"
)

type anthropicProvider struct {
	baseURL   string
	model     string
	key       string
	maxTokens int
	client    *http.Client
}

func newAnthropic(model string) (*anthropicProvider, error) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY is required for the anthropic provider")
	}
	base := firstNonEmpty(os.Getenv("ANTHROPIC_BASE_URL"), "https://api.anthropic.com/v1")
	return &anthropicProvider{
		baseURL:   strings.TrimRight(base, "/"),
		model:     firstNonEmpty(model, os.Getenv("ANTHROPIC_MODEL"), defaultAnthropicModel),
		key:       key,
		maxTokens: envInt("ANTHROPIC_MAX_TOKENS", defaultAnthropicMaxTokens),
		client:    &http.Client{Timeout: envDuration("ANTHROPIC_TIMEOUT_SECONDS", defaultTimeout)},
	}, nil
}

func (a *anthropicProvider) Name() string { return "anthropic" }

func (a *anthropicProvider) Analyze(ctx context.Context, prompt string) (string, error) {
	return withRetry(ctx, "anthropic", func(ctx context.Context) (string, error) {
		return a.once(ctx, prompt)
	})
}

func (a *anthropicProvider) once(ctx context.Context, prompt string) (string, error) {
	payload := map[string]any{
		"model":      a.model,
		"max_tokens": a.maxTokens,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", a.key)
	req.Header.Set("anthropic-version", anthropicVersion)

	resp, err := a.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to call Anthropic: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("Anthropic returned status %d: %s", resp.StatusCode, string(snippet))
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode Anthropic response: %w", err)
	}

	var sb strings.Builder
	for _, block := range result.Content {
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
	}
	if sb.Len() == 0 {
		return "", fmt.Errorf("Anthropic returned no text content")
	}
	return sb.String(), nil
}
