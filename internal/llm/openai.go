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

const defaultOpenAIModel = "gpt-4o-mini"

type openaiProvider struct {
	baseURL string
	model   string
	key     string
	client  *http.Client
}

func newOpenAI(model string) (*openaiProvider, error) {
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY is required for the openai provider")
	}
	base := firstNonEmpty(os.Getenv("OPENAI_BASE_URL"), "https://api.openai.com/v1")
	return &openaiProvider{
		baseURL: strings.TrimRight(base, "/"),
		model:   firstNonEmpty(model, os.Getenv("OPENAI_MODEL"), defaultOpenAIModel),
		key:     key,
		client:  &http.Client{Timeout: envDuration("OPENAI_TIMEOUT_SECONDS", defaultTimeout)},
	}, nil
}

func (o *openaiProvider) Name() string { return "openai" }

func (o *openaiProvider) Analyze(ctx context.Context, prompt string) (string, error) {
	return withRetry(ctx, "openai", func(ctx context.Context) (string, error) {
		return o.once(ctx, prompt)
	})
}

func (o *openaiProvider) once(ctx context.Context, prompt string) (string, error) {
	payload := map[string]any{
		"model": o.model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.key)

	resp, err := o.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to call OpenAI: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("OpenAI returned status %d: %s", resp.StatusCode, string(snippet))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode OpenAI response: %w", err)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("OpenAI returned no choices")
	}
	return result.Choices[0].Message.Content, nil
}
