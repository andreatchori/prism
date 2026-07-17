package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

type OllamaRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
}

type OllamaResponse struct {
	Response string `json:"response"`
}

const (
	defaultModel      = "deepseek-coder:6.7b"
	defaultTimeout    = 120 * time.Second
	defaultMaxRetries = 2
)

// Analyze sends the prompt to Ollama and returns the review. It applies a
// configurable timeout (OLLAMA_TIMEOUT_SECONDS) and retries transient failures
// (OLLAMA_MAX_RETRIES) with a short backoff.
func Analyze(prompt string) (string, error) {
	return AnalyzeContext(context.Background(), prompt)
}

// AnalyzeContext is like Analyze but honors an external context for cancellation.
func AnalyzeContext(ctx context.Context, prompt string) (string, error) {
	ollamaURL := os.Getenv("OLLAMA_URL")
	if ollamaURL == "" {
		ollamaURL = "http://localhost:11434"
	}

	model := os.Getenv("OLLAMA_MODEL")
	if model == "" {
		model = defaultModel
	}

	payload := OllamaRequest{
		Model:  model,
		Prompt: prompt,
		Stream: false,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	timeout := envDuration("OLLAMA_TIMEOUT_SECONDS", defaultTimeout)
	maxRetries := envInt("OLLAMA_MAX_RETRIES", defaultMaxRetries)
	client := &http.Client{Timeout: timeout}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(attempt) * time.Second
			log.Printf("Ollama retry %d/%d after %v (last error: %v)", attempt, maxRetries, backoff, lastErr)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}

		resp, err := doRequest(ctx, client, ollamaURL, body)
		if err != nil {
			lastErr = err
			continue
		}
		return resp, nil
	}

	return "", fmt.Errorf("Ollama call failed after %d attempt(s): %w", maxRetries+1, lastErr)
}

func doRequest(ctx context.Context, client *http.Client, ollamaURL string, body []byte) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ollamaURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to call Ollama: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("Ollama returned status %d: %s", resp.StatusCode, string(snippet))
	}

	var result OllamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode Ollama response: %w", err)
	}
	return result.Response, nil
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
