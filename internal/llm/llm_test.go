package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewDefaultsToOllama(t *testing.T) {
	t.Setenv("PRISM_LLM_PROVIDER", "")
	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if p.Name() != "ollama" {
		t.Errorf("default provider = %s, want ollama", p.Name())
	}
}

func TestNewSelectsProviderFromOptions(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test")
	p, err := New(Options{Provider: "openai"})
	if err != nil {
		t.Fatalf("New(openai) error: %v", err)
	}
	if p.Name() != "openai" {
		t.Errorf("provider = %s, want openai", p.Name())
	}
}

func TestNewOpenAIRequiresKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	if _, err := New(Options{Provider: "openai"}); err == nil {
		t.Error("expected error when OPENAI_API_KEY is missing")
	}
}

func TestNewAnthropicRequiresKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	if _, err := New(Options{Provider: "anthropic"}); err == nil {
		t.Error("expected error when ANTHROPIC_API_KEY is missing")
	}
}

func TestNewUnknownProvider(t *testing.T) {
	if _, err := New(Options{Provider: "gemini"}); err == nil {
		t.Error("expected error for unknown provider")
	}
}

func TestOpenAIAnalyzeParsesResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("missing/incorrect auth header: %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"REVIEW OK"}}]}`))
	}))
	defer srv.Close()

	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("OPENAI_BASE_URL", srv.URL)

	p, err := New(Options{Provider: "openai", Model: "gpt-test"})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	out, err := p.Analyze(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Analyze error: %v", err)
	}
	if out != "REVIEW OK" {
		t.Errorf("got %q, want REVIEW OK", out)
	}
}

func TestAnthropicAnalyzeParsesResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("x-api-key") != "key" {
			t.Errorf("missing x-api-key header")
		}
		if r.Header.Get("anthropic-version") == "" {
			t.Errorf("missing anthropic-version header")
		}
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"CLAUDE OK"}]}`))
	}))
	defer srv.Close()

	t.Setenv("ANTHROPIC_API_KEY", "key")
	t.Setenv("ANTHROPIC_BASE_URL", srv.URL)

	p, err := New(Options{Provider: "anthropic"})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	out, err := p.Analyze(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Analyze error: %v", err)
	}
	if out != "CLAUDE OK" {
		t.Errorf("got %q, want CLAUDE OK", out)
	}
}

func TestAzureOpenAIRequiresConfig(t *testing.T) {
	t.Setenv("AZURE_OPENAI_API_KEY", "")
	t.Setenv("AZURE_OPENAI_ENDPOINT", "")
	if _, err := New(Options{Provider: "azure-openai"}); err == nil {
		t.Error("expected error when Azure OpenAI config is missing")
	}
}

func TestAzureOpenAIAnalyzeParsesResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("api-key") != "az-key" {
			t.Errorf("missing api-key header")
		}
		if r.URL.Query().Get("api-version") == "" {
			t.Errorf("missing api-version query param")
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"AZURE OK"}}]}`))
	}))
	defer srv.Close()

	t.Setenv("AZURE_OPENAI_API_KEY", "az-key")
	t.Setenv("AZURE_OPENAI_ENDPOINT", srv.URL)
	t.Setenv("AZURE_OPENAI_DEPLOYMENT", "gpt4o")

	p, err := New(Options{Provider: "azure-openai"})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	out, err := p.Analyze(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Analyze error: %v", err)
	}
	if out != "AZURE OK" {
		t.Errorf("got %q, want AZURE OK", out)
	}
}

func TestFallbackUsedWhenPrimaryConstructionFails(t *testing.T) {
	// Primary openai has no key -> construction fails; fallback ollama is usable.
	t.Setenv("OPENAI_API_KEY", "")
	p, err := New(Options{Provider: "openai", Fallback: "ollama"})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if p.Name() != "ollama" {
		t.Errorf("expected degradation to ollama, got %s", p.Name())
	}
}

func TestFallbackWrapsBothProviders(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test")
	p, err := New(Options{Provider: "openai", Fallback: "ollama"})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if _, ok := p.(*fallbackProvider); !ok {
		t.Errorf("expected a fallbackProvider, got %T (%s)", p, p.Name())
	}
}

func TestFallbackAnalyzeFallsBackOnError(t *testing.T) {
	failing := failingProvider{}
	ok := staticProvider{out: "SECONDARY"}
	fp := &fallbackProvider{primary: failing, secondary: ok}

	out, err := fp.Analyze(context.Background(), "x")
	if err != nil {
		t.Fatalf("Analyze error: %v", err)
	}
	if out != "SECONDARY" {
		t.Errorf("got %q, want SECONDARY", out)
	}
}

type failingProvider struct{}

func (failingProvider) Name() string { return "failing" }
func (failingProvider) Analyze(context.Context, string) (string, error) {
	return "", context.DeadlineExceeded
}

type staticProvider struct{ out string }

func (s staticProvider) Name() string { return "static" }
func (s staticProvider) Analyze(context.Context, string) (string, error) {
	return s.out, nil
}

func TestEnvProviderOverride(t *testing.T) {
	t.Setenv("PRISM_LLM_PROVIDER", "anthropic")
	t.Setenv("ANTHROPIC_API_KEY", "key")
	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if p.Name() != "anthropic" {
		t.Errorf("provider = %s, want anthropic", p.Name())
	}
}
