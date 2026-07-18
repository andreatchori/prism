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

const defaultAzureAPIVersion = "2024-06-01"

type azureOpenAIProvider struct {
	endpoint   string
	deployment string
	apiVersion string
	key        string
	client     *http.Client
}

func newAzureOpenAI(model string) (*azureOpenAIProvider, error) {
	key := os.Getenv("AZURE_OPENAI_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("AZURE_OPENAI_API_KEY is required for the azure-openai provider")
	}
	endpoint := os.Getenv("AZURE_OPENAI_ENDPOINT")
	if endpoint == "" {
		return nil, fmt.Errorf("AZURE_OPENAI_ENDPOINT is required for the azure-openai provider")
	}
	deployment := firstNonEmpty(model, os.Getenv("AZURE_OPENAI_DEPLOYMENT"))
	if deployment == "" {
		return nil, fmt.Errorf("azure-openai deployment name is required (set [llm].model or AZURE_OPENAI_DEPLOYMENT)")
	}
	return &azureOpenAIProvider{
		endpoint:   strings.TrimRight(endpoint, "/"),
		deployment: deployment,
		apiVersion: firstNonEmpty(os.Getenv("AZURE_OPENAI_API_VERSION"), defaultAzureAPIVersion),
		key:        key,
		client:     &http.Client{Timeout: envDuration("AZURE_OPENAI_TIMEOUT_SECONDS", defaultTimeout)},
	}, nil
}

func (a *azureOpenAIProvider) Name() string { return "azure-openai" }

func (a *azureOpenAIProvider) Analyze(ctx context.Context, prompt string) (string, error) {
	return withRetry(ctx, "azure-openai", func(ctx context.Context) (string, error) {
		return a.once(ctx, prompt)
	})
}

func (a *azureOpenAIProvider) once(ctx context.Context, prompt string) (string, error) {
	payload := map[string]any{
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	endpoint := fmt.Sprintf("%s/openai/deployments/%s/chat/completions?api-version=%s",
		a.endpoint, a.deployment, a.apiVersion)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", a.key)

	resp, err := a.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to call Azure OpenAI: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("Azure OpenAI returned status %d: %s", resp.StatusCode, string(snippet))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode Azure OpenAI response: %w", err)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("Azure OpenAI returned no choices")
	}
	return result.Choices[0].Message.Content, nil
}
