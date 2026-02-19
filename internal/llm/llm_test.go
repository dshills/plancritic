package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResolveProviderAnthropicPrefix(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	p, err := ResolveProvider("anthropic:claude-sonnet-4-6")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "anthropic" {
		t.Errorf("expected anthropic provider, got %s", p.Name())
	}
}

func TestResolveProviderClaudePrefix(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	p, err := ResolveProvider("claude-sonnet-4-6")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "anthropic" {
		t.Errorf("expected anthropic provider, got %s", p.Name())
	}
}

func TestResolveProviderOpenAIPrefix(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	p, err := ResolveProvider("openai:gpt-5.2")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "openai" {
		t.Errorf("expected openai provider, got %s", p.Name())
	}
}

func TestResolveProviderGPTPrefix(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	p, err := ResolveProvider("gpt-5.2")
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

// --- ExtractJSON table-driven tests ---

func TestExtractJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain JSON",
			input: `{"key": "value"}`,
			want:  `{"key": "value"}`,
		},
		{
			name:  "json code fence",
			input: "```json\n{\"key\": \"value\"}\n```",
			want:  `{"key": "value"}`,
		},
		{
			name:  "bare code fence",
			input: "```\n{\"key\": \"value\"}\n```",
			want:  `{"key": "value"}`,
		},
		{
			name:  "whitespace around fences",
			input: "  \n```json\n{\"key\": \"value\"}\n```\n  ",
			want:  `{"key": "value"}`,
		},
		{
			name:  "no closing fence",
			input: "```json\n{\"key\": \"value\"}",
			want:  `{"key": "value"}`,
		},
		{
			name:  "already trimmed",
			input: "  {\"a\": 1}  ",
			want:  `{"a": 1}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractJSON(tt.input)
			if got != tt.want {
				t.Errorf("ExtractJSON(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// --- Anthropic error path tests ---

func TestAnthropicNon200Status(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error": "rate limited"}`))
	}))
	defer srv.Close()

	p := &AnthropicProvider{apiKey: "test-key", apiURL: srv.URL, client: srv.Client()}
	_, err := p.Generate(context.Background(), "prompt", Settings{})
	if err == nil {
		t.Fatal("expected error for non-200 status")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error should contain status code 429, got: %s", err.Error())
	}
}

func TestAnthropicMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`not json at all`))
	}))
	defer srv.Close()

	p := &AnthropicProvider{apiKey: "test-key", apiURL: srv.URL, client: srv.Client()}
	_, err := p.Generate(context.Background(), "prompt", Settings{})
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if !strings.Contains(err.Error(), "parse response") {
		t.Errorf("error should mention parse, got: %s", err.Error())
	}
}

func TestAnthropicNoTextContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := anthropicResponse{
			Content: []anthropicContentBlock{
				{Type: "image", Text: ""},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &AnthropicProvider{apiKey: "test-key", apiURL: srv.URL, client: srv.Client()}
	_, err := p.Generate(context.Background(), "prompt", Settings{})
	if err == nil {
		t.Fatal("expected error for no text content")
	}
	if !strings.Contains(err.Error(), "no text content") {
		t.Errorf("error should mention 'no text content', got: %s", err.Error())
	}
}

func TestAnthropicEmptyContentBlocks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := anthropicResponse{
			Content: []anthropicContentBlock{},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &AnthropicProvider{apiKey: "test-key", apiURL: srv.URL, client: srv.Client()}
	_, err := p.Generate(context.Background(), "prompt", Settings{})
	if err == nil {
		t.Fatal("expected error for empty content blocks")
	}
	if !strings.Contains(err.Error(), "no text content") {
		t.Errorf("error should mention 'no text content', got: %s", err.Error())
	}
}

// --- OpenAI error path tests ---

func TestOpenAINon200Status(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "server error"}`))
	}))
	defer srv.Close()

	p := &OpenAIProvider{apiKey: "test-key", apiURL: srv.URL, client: srv.Client()}
	_, err := p.Generate(context.Background(), "prompt", Settings{})
	if err == nil {
		t.Fatal("expected error for non-200 status")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should contain status code 500, got: %s", err.Error())
	}
}

func TestOpenAIMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	p := &OpenAIProvider{apiKey: "test-key", apiURL: srv.URL, client: srv.Client()}
	_, err := p.Generate(context.Background(), "prompt", Settings{})
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if !strings.Contains(err.Error(), "parse response") {
		t.Errorf("error should mention parse, got: %s", err.Error())
	}
}

func TestOpenAIEmptyChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openaiResponse{Choices: []openaiChoice{}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &OpenAIProvider{apiKey: "test-key", apiURL: srv.URL, client: srv.Client()}
	_, err := p.Generate(context.Background(), "prompt", Settings{})
	if err == nil {
		t.Fatal("expected error for empty choices")
	}
	if !strings.Contains(err.Error(), "no choices") {
		t.Errorf("error should mention 'no choices', got: %s", err.Error())
	}
}

func TestOpenAITruncation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openaiResponse{
			Choices: []openaiChoice{
				{
					Message:      openaiMessage{Content: `{"partial": true}`},
					FinishReason: "length",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &OpenAIProvider{apiKey: "test-key", apiURL: srv.URL, client: srv.Client()}
	_, err := p.Generate(context.Background(), "prompt", Settings{MaxTokens: 100})
	if err == nil {
		t.Fatal("expected error for truncated response")
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Errorf("error should mention 'truncated', got: %s", err.Error())
	}
}

func TestOpenAISeedPassthrough(t *testing.T) {
	seed := 42
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody openaiRequest
		json.NewDecoder(r.Body).Decode(&reqBody)

		if reqBody.Seed == nil {
			t.Error("expected seed to be set in request")
		} else if *reqBody.Seed != 42 {
			t.Errorf("expected seed 42, got %d", *reqBody.Seed)
		}

		resp := openaiResponse{
			Choices: []openaiChoice{
				{Message: openaiMessage{Content: `{"ok": true}`}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &OpenAIProvider{apiKey: "test-key", apiURL: srv.URL, client: srv.Client()}
	_, err := p.Generate(context.Background(), "prompt", Settings{Seed: &seed})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOpenAISeedOmittedWhenNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var raw map[string]json.RawMessage
		json.NewDecoder(r.Body).Decode(&raw)

		if _, hasSeed := raw["seed"]; hasSeed {
			t.Error("seed should be omitted from request when nil")
		}

		resp := openaiResponse{
			Choices: []openaiChoice{
				{Message: openaiMessage{Content: `{"ok": true}`}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &OpenAIProvider{apiKey: "test-key", apiURL: srv.URL, client: srv.Client()}
	_, err := p.Generate(context.Background(), "prompt", Settings{})
	if err != nil {
		t.Fatal(err)
	}
}
