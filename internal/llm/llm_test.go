package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolveProviderAnthropicPrefix(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	p, err := ResolveProvider("anthropic:claude-sonnet-4-20250514")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "anthropic" {
		t.Errorf("expected anthropic provider, got %s", p.Name())
	}
}

func TestResolveProviderClaudePrefix(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	p, err := ResolveProvider("claude-sonnet-4-20250514")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "anthropic" {
		t.Errorf("expected anthropic provider, got %s", p.Name())
	}
}

func TestResolveProviderOpenAIPrefix(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	p, err := ResolveProvider("openai:gpt-4o")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "openai" {
		t.Errorf("expected openai provider, got %s", p.Name())
	}
}

func TestResolveProviderGPTPrefix(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	p, err := ResolveProvider("gpt-4o")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "openai" {
		t.Errorf("expected openai provider, got %s", p.Name())
	}
}

func TestResolveProviderAutoDetectAnthropic(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	p, err := ResolveProvider("")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "anthropic" {
		t.Errorf("expected anthropic, got %s", p.Name())
	}
}

func TestResolveProviderAutoDetectOpenAI(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "test-key")
	p, err := ResolveProvider("")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "openai" {
		t.Errorf("expected openai, got %s", p.Name())
	}
}

func TestResolveProviderNone(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	_, err := ResolveProvider("")
	if err == nil {
		t.Error("expected error when no API keys set")
	}
}

func TestMockProvider(t *testing.T) {
	m := &MockProvider{Response: `{"test": true}`}
	got, err := m.Generate(context.Background(), "prompt", Settings{})
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"test": true}` {
		t.Errorf("unexpected response: %s", got)
	}
}

func TestAnthropicProviderGenerate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "test-key" {
			t.Error("missing API key header")
		}
		if r.Header.Get("Anthropic-Version") == "" {
			t.Error("missing Anthropic-Version header")
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Error("missing Content-Type header")
		}

		resp := anthropicResponse{
			Content: []anthropicContentBlock{
				{Type: "text", Text: `{"result": "ok"}`},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &AnthropicProvider{apiKey: "test-key", apiURL: srv.URL, client: srv.Client()}
	got, err := p.Generate(context.Background(), "test prompt", Settings{Temperature: 0.2})
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"result": "ok"}` {
		t.Errorf("unexpected response: %s", got)
	}
}

func TestOpenAIProviderGenerate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing Authorization header")
		}

		var reqBody openaiRequest
		json.NewDecoder(r.Body).Decode(&reqBody)
		if reqBody.ResponseFormat == nil || reqBody.ResponseFormat.Type != "json_object" {
			t.Error("expected json_object response format")
		}

		resp := openaiResponse{
			Choices: []openaiChoice{
				{Message: openaiMessage{Content: `{"result": "ok"}`}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &OpenAIProvider{apiKey: "test-key", apiURL: srv.URL, client: srv.Client()}
	got, err := p.Generate(context.Background(), "test prompt", Settings{Temperature: 0.2})
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"result": "ok"}` {
		t.Errorf("unexpected response: %s", got)
	}
}
