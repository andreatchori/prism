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

const defaultOllamaModel = "deepseek-coder:6.7b"

type ollamaProvider struct {
	url    string
	model  string
	client *http.Client
}

type ollamaRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
}

type ollamaResponse struct {
	Response string `json:"response"`
}

func newOllama(model string) *ollamaProvider {
	url := os.Getenv("OLLAMA_URL")
	if url == "" {
		url = "http://localhost:11434"
	}
	return &ollamaProvider{
		url:    strings.TrimRight(url, "/"),
		model:  firstNonEmpty(model, os.Getenv("OLLAMA_MODEL"), defaultOllamaModel),
		client: &http.Client{Timeout: envDuration("OLLAMA_TIMEOUT_SECONDS", defaultTimeout)},
	}
}

func (o *ollamaProvider) Name() string { return "ollama" }

func (o *ollamaProvider) Analyze(ctx context.Context, prompt string) (string, error) {
	return withRetry(ctx, "ollama", func(ctx context.Context) (string, error) {
		return o.once(ctx, prompt)
	})
}

func (o *ollamaProvider) once(ctx context.Context, prompt string) (string, error) {
	body, err := json.Marshal(ollamaRequest{Model: o.model, Prompt: prompt, Stream: false})
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.url+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to call Ollama: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("Ollama returned status %d: %s", resp.StatusCode, string(snippet))
	}

	var result ollamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode Ollama response: %w", err)
	}
	return result.Response, nil
}
