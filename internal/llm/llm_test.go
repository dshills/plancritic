package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestResolveProviderAnthropicPrefix(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	p, err := ResolveProvider("", "anthropic:claude-sonnet-4-6")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "anthropic" {
		t.Errorf("expected anthropic provider, got %s", p.Name())
	}
}

func TestResolveProviderClaudePrefix(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	p, err := ResolveProvider("", "claude-sonnet-4-6")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "anthropic" {
		t.Errorf("expected anthropic provider, got %s", p.Name())
	}
}

func TestResolveProviderOpenAIPrefix(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	p, err := ResolveProvider("", "openai:gpt-5.2")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "openai" {
		t.Errorf("expected openai provider, got %s", p.Name())
	}
}

func TestResolveProviderGPTPrefix(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	p, err := ResolveProvider("", "gpt-5.2")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "openai" {
		t.Errorf("expected openai provider, got %s", p.Name())
	}
}

func TestResolveProviderAutoDetectAnthropic(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	p, err := ResolveProvider("", "")
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
	p, err := ResolveProvider("", "")
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
	t.Setenv("GEMINI_API_KEY", "")
	_, err := ResolveProvider("", "")
	if err == nil {
		t.Error("expected error when no API keys set")
	}
}

func TestResolveProviderFlagAnthropic(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	p, err := ResolveProvider("anthropic", "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "anthropic" {
		t.Errorf("expected anthropic, got %s", p.Name())
	}
}

func TestResolveProviderFlagOpenAI(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	p, err := ResolveProvider("openai", "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "openai" {
		t.Errorf("expected openai, got %s", p.Name())
	}
}

func TestResolveProviderFlagWithModel(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	p, err := ResolveProvider("anthropic", "claude-opus-4")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "anthropic" {
		t.Errorf("expected anthropic, got %s", p.Name())
	}
}

func TestResolveProviderFlagOverridesModelPrefix(t *testing.T) {
	// --provider=openai should win even if model looks like "claude-..."
	t.Setenv("OPENAI_API_KEY", "test-key")
	p, err := ResolveProvider("openai", "claude-sonnet-4-6")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "openai" {
		t.Errorf("expected openai (from --provider flag), got %s", p.Name())
	}
}

func TestResolveProviderFlagStripsRedundantPrefix(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	// --provider=anthropic --model=anthropic:claude-sonnet-4-6 should strip the prefix
	p, err := ResolveProvider("anthropic", "anthropic:claude-sonnet-4-6")
	if err != nil {
		t.Fatal(err)
	}
	mo, ok := p.(*modelOverride)
	if !ok {
		t.Fatal("expected modelOverride wrapper")
	}
	if mo.model != "claude-sonnet-4-6" {
		t.Errorf("expected model 'claude-sonnet-4-6', got %q", mo.model)
	}
}

func TestResolveProviderFlagGemini(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "test-key")
	p, err := ResolveProvider("gemini", "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "gemini" {
		t.Errorf("expected gemini, got %s", p.Name())
	}
}

func TestResolveProviderFlagGoogle(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "test-key")
	p, err := ResolveProvider("google", "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "gemini" {
		t.Errorf("expected gemini, got %s", p.Name())
	}
}

func TestResolveProviderGeminiPrefix(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "test-key")
	p, err := ResolveProvider("", "gemini-2.5-flash")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "gemini" {
		t.Errorf("expected gemini, got %s", p.Name())
	}
}

func TestResolveProviderGeminiExplicitPrefix(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "test-key")
	p, err := ResolveProvider("", "gemini:gemini-2.5-pro")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "gemini" {
		t.Errorf("expected gemini, got %s", p.Name())
	}
}

func TestResolveProviderAutoDetectGemini(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "test-key")
	p, err := ResolveProvider("", "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "gemini" {
		t.Errorf("expected gemini, got %s", p.Name())
	}
}

func TestResolveProviderFlagUnknown(t *testing.T) {
	_, err := ResolveProvider("azure", "")
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
	if !strings.Contains(err.Error(), "unknown provider") {
		t.Errorf("expected 'unknown provider' error, got: %s", err.Error())
	}
}

func TestMockProvider(t *testing.T) {
	m := &MockProvider{Response: `{"test": true}`}
	got, _, err := m.Generate(context.Background(), "prompt", Settings{})
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
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &AnthropicProvider{apiKey: "test-key", apiURL: srv.URL, client: srv.Client()}
	got, _, err := p.Generate(context.Background(), "test prompt", Settings{Temperature: 0.2})
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"result": "ok"}` {
		t.Errorf("unexpected response: %s", got)
	}
}

func TestAnthropicGenerateSegmentsCacheControl(t *testing.T) {
	var captured anthropicRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		resp := map[string]any{
			"content":     []map[string]string{{"type": "text", "text": `{"ok": true}`}},
			"stop_reason": "end_turn",
			"usage": map[string]int{
				"input_tokens":                100,
				"output_tokens":               50,
				"cache_creation_input_tokens": 800,
				"cache_read_input_tokens":     0,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &AnthropicProvider{apiKey: "test-key", apiURL: srv.URL, client: srv.Client()}
	segs := []Segment{
		{Text: "static prefix\n", CacheMark: true},
		{Text: "context files\n", CacheMark: true},
		{Text: "plan content\n"},
	}
	_, usage, err := p.GenerateSegments(context.Background(), segs, Settings{})
	if err != nil {
		t.Fatal(err)
	}

	if len(captured.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(captured.Messages))
	}
	blocks := captured.Messages[0].Content
	if len(blocks) != 3 {
		t.Fatalf("expected 3 content blocks, got %d", len(blocks))
	}
	if blocks[0].CacheControl == nil || blocks[0].CacheControl.Type != "ephemeral" {
		t.Error("block 0 should have ephemeral cache_control")
	}
	if blocks[1].CacheControl == nil || blocks[1].CacheControl.Type != "ephemeral" {
		t.Error("block 1 should have ephemeral cache_control")
	}
	if blocks[2].CacheControl != nil {
		t.Error("block 2 (plan) must NOT have cache_control")
	}

	if usage.CacheCreationInputTokens != 800 {
		t.Errorf("expected cache_creation=800, got %d", usage.CacheCreationInputTokens)
	}
	if usage.InputTokens != 100 || usage.OutputTokens != 50 {
		t.Errorf("unexpected token counts: %+v", usage)
	}
}

func TestAnthropicGenerateSegmentsOmitsEmpty(t *testing.T) {
	var captured anthropicRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		resp := anthropicResponse{Content: []anthropicContentBlock{{Type: "text", Text: "ok"}}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &AnthropicProvider{apiKey: "test-key", apiURL: srv.URL, client: srv.Client()}
	_, _, err := p.GenerateSegments(context.Background(), []Segment{
		{Text: "prefix", CacheMark: true},
		{Text: "", CacheMark: true},
		{Text: "tail"},
	}, Settings{})
	if err != nil {
		t.Fatal(err)
	}
	if len(captured.Messages[0].Content) != 2 {
		t.Errorf("empty segment should be dropped; got %d blocks", len(captured.Messages[0].Content))
	}
}

func TestAnthropicImplementsSegmentedProvider(t *testing.T) {
	var _ SegmentedProvider = (*AnthropicProvider)(nil)
}

func TestGeminiImplementsCachingProvider(t *testing.T) {
	var _ SegmentedProvider = (*GeminiProvider)(nil)
	var _ CachingProvider = (*GeminiProvider)(nil)
}

func TestGeminiCreateCacheRequest(t *testing.T) {
	var captured geminiCacheCreateRequest
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if got := r.URL.Query().Get("key"); got != "" {
			t.Errorf("Gemini cache API key should not be in URL query, got %q", got)
		}
		if got := r.Header.Get("x-goog-api-key"); got != "test-key" {
			t.Errorf("Gemini cache API key header = %q, want test-key", got)
		}
		_ = json.NewDecoder(r.Body).Decode(&captured)
		resp := geminiCacheCreateResponse{
			Name:       "cachedContents/test-xyz",
			Model:      "models/gemini-2.5-flash",
			ExpireTime: time.Now().Add(1 * time.Hour),
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &GeminiProvider{apiKey: "test-key", apiURL: srv.URL, client: srv.Client()}
	// Build a cacheable prefix well above the min-size threshold.
	prefix := strings.Repeat("static rule text. ", 400) // ~7000 chars
	segs := []Segment{
		{Text: prefix, CacheMark: true},
		{Text: "uncached tail", CacheMark: false},
	}

	handle, err := p.CreateCache(context.Background(), segs, "gemini-2.5-flash", 1*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if handle.Name != "cachedContents/test-xyz" {
		t.Errorf("unexpected handle name: %q", handle.Name)
	}
	if !strings.Contains(gotPath, "cachedContents") {
		t.Errorf("expected cachedContents endpoint, got path %q", gotPath)
	}
	if captured.Model != "models/gemini-2.5-flash" {
		t.Errorf("model should be qualified, got %q", captured.Model)
	}
	if captured.TTL != "3600s" {
		t.Errorf("expected ttl=3600s, got %q", captured.TTL)
	}
	if len(captured.Contents) != 1 || len(captured.Contents[0].Parts) != 1 {
		t.Fatalf("unexpected contents shape: %+v", captured.Contents)
	}
	if captured.Contents[0].Parts[0].Text != prefix {
		t.Error("cache should contain only CacheMark=true segment text")
	}
}

func TestGeminiRejectsInterleavedCacheMarks(t *testing.T) {
	p := &GeminiProvider{apiKey: "test-key", apiURL: "http://never-called", client: &http.Client{}}

	// CreateCache: marked→unmarked→marked must error (would reorder prompt).
	prefix := strings.Repeat("x", GeminiMinCacheChars)
	_, err := p.CreateCache(context.Background(), []Segment{
		{Text: prefix, CacheMark: true},
		{Text: "middle", CacheMark: false},
		{Text: "also cached", CacheMark: true},
	}, "gemini-2.5-flash", 1*time.Hour)
	if err == nil || !strings.Contains(err.Error(), "contiguous prefix") {
		t.Errorf("expected contiguous-prefix error from CreateCache, got: %v", err)
	}

	// GenerateSegments with CachedContentName: same rule applies.
	_, _, err = p.GenerateSegments(context.Background(), []Segment{
		{Text: "a", CacheMark: true},
		{Text: "b", CacheMark: false},
		{Text: "c", CacheMark: true},
	}, Settings{CachedContentName: "cachedContents/x"})
	if err == nil || !strings.Contains(err.Error(), "contiguous prefix") {
		t.Errorf("expected contiguous-prefix error from GenerateSegments, got: %v", err)
	}
}

func TestContiguousCachePrefixEnd(t *testing.T) {
	cases := []struct {
		name    string
		segs    []Segment
		wantEnd int
		wantOk  bool
	}{
		{"all marked", []Segment{{CacheMark: true}, {CacheMark: true}}, 2, true},
		{"prefix then tail", []Segment{{CacheMark: true}, {CacheMark: true}, {CacheMark: false}}, 2, true},
		{"no marks", []Segment{{CacheMark: false}, {CacheMark: false}}, 0, true},
		{"empty", nil, 0, true},
		{"interleaved", []Segment{{CacheMark: true}, {CacheMark: false}, {CacheMark: true}}, 0, false},
		{"trailing only", []Segment{{CacheMark: false}, {CacheMark: true}}, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			end, ok := contiguousCachePrefixEnd(tc.segs)
			if ok != tc.wantOk || (ok && end != tc.wantEnd) {
				t.Errorf("got (%d, %v), want (%d, %v)", end, ok, tc.wantEnd, tc.wantOk)
			}
		})
	}
}

func TestGeminiCreateCacheRejectsSmallPrefix(t *testing.T) {
	p := &GeminiProvider{apiKey: "test-key", apiURL: "http://never-called", client: &http.Client{}}
	_, err := p.CreateCache(context.Background(), []Segment{
		{Text: "tiny", CacheMark: true},
	}, "gemini-2.5-flash", 1*time.Hour)
	if err == nil {
		t.Fatal("expected error for prefix below min size")
	}
	if !strings.Contains(err.Error(), "too small") {
		t.Errorf("expected size error, got: %v", err)
	}
}

func TestGeminiGenerateSegmentsWithCachedContent(t *testing.T) {
	var captured geminiRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		resp := geminiResponse{
			Candidates: []geminiCandidate{
				{Content: geminiContent{Parts: []geminiPart{{Text: `{"ok": true}`}}}},
			},
			UsageMetadata: geminiUsageMetadata{
				PromptTokenCount:        500,
				CandidatesTokenCount:    50,
				CachedContentTokenCount: 3000,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &GeminiProvider{apiKey: "test-key", apiURL: srv.URL, client: srv.Client()}
	segs := []Segment{
		{Text: "STATIC PREFIX — should NOT be re-sent", CacheMark: true},
		{Text: "VARIABLE TAIL — should be sent", CacheMark: false},
	}
	_, usage, err := p.GenerateSegments(context.Background(), segs, Settings{
		CachedContentName: "cachedContents/abc123",
	})
	if err != nil {
		t.Fatal(err)
	}

	if captured.CachedContent != "cachedContents/abc123" {
		t.Errorf("expected cachedContent=%q, got %q", "cachedContents/abc123", captured.CachedContent)
	}
	if len(captured.Contents) != 1 || len(captured.Contents[0].Parts) != 1 {
		t.Fatalf("unexpected contents shape: %+v", captured.Contents)
	}
	sent := captured.Contents[0].Parts[0].Text
	if strings.Contains(sent, "STATIC PREFIX") {
		t.Error("cached segment must not be re-sent in contents when CachedContentName is set")
	}
	if !strings.Contains(sent, "VARIABLE TAIL") {
		t.Error("variable segment should be sent in contents")
	}
	if usage.CacheReadInputTokens != 3000 {
		t.Errorf("expected cache_read=3000, got %d", usage.CacheReadInputTokens)
	}
}

func TestGeminiGenerateSegmentsWithoutCachedContent(t *testing.T) {
	var captured geminiRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		resp := geminiResponse{
			Candidates: []geminiCandidate{
				{Content: geminiContent{Parts: []geminiPart{{Text: "ok"}}}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &GeminiProvider{apiKey: "test-key", apiURL: srv.URL, client: srv.Client()}
	segs := []Segment{
		{Text: "prefix ", CacheMark: true},
		{Text: "tail", CacheMark: false},
	}
	_, _, err := p.GenerateSegments(context.Background(), segs, Settings{})
	if err != nil {
		t.Fatal(err)
	}
	if captured.CachedContent != "" {
		t.Errorf("cachedContent must be empty when CachedContentName is unset, got %q", captured.CachedContent)
	}
	sent := captured.Contents[0].Parts[0].Text
	if !strings.Contains(sent, "prefix") || !strings.Contains(sent, "tail") {
		t.Errorf("all segments should be sent when not using cache; got %q", sent)
	}
}

func TestOpenAIProviderGenerate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing Authorization header")
		}

		var reqBody openaiRequest
		_ = json.NewDecoder(r.Body).Decode(&reqBody)
		if reqBody.ResponseFormat == nil || reqBody.ResponseFormat.Type != "json_object" {
			t.Error("expected json_object response format")
		}

		resp := openaiResponse{
			Choices: []openaiChoice{
				{Message: openaiMessage{Content: `{"result": "ok"}`}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &OpenAIProvider{apiKey: "test-key", apiURL: srv.URL, client: srv.Client()}
	got, _, err := p.Generate(context.Background(), "test prompt", Settings{Temperature: 0.2})
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"result": "ok"}` {
		t.Errorf("unexpected response: %s", got)
	}
}

func TestGeminiProviderGenerate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Error("missing Content-Type header")
		}
		if got := r.URL.Query().Get("key"); got != "" {
			t.Errorf("Gemini API key should not be in URL query, got %q", got)
		}
		if got := r.Header.Get("x-goog-api-key"); got != "test-key" {
			t.Errorf("Gemini API key header = %q, want test-key", got)
		}

		resp := geminiResponse{
			Candidates: []geminiCandidate{
				{
					Content: geminiContent{
						Parts: []geminiPart{
							{Text: `{"result": "ok"}`},
						},
					},
					FinishReason: "STOP",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &GeminiProvider{apiKey: "test-key", apiURL: srv.URL, client: srv.Client()}
	got, _, err := p.Generate(context.Background(), "test prompt", Settings{Temperature: 0.2})
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"result": "ok"}` {
		t.Errorf("unexpected response: %s", got)
	}
}

func TestGeminiNon200Status(t *testing.T) {
	noSleep(t) // 429 is retried; do not wait for real backoff
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error": "rate limited"}`))
	}))
	defer srv.Close()

	p := &GeminiProvider{apiKey: "test-key", apiURL: srv.URL, client: srv.Client()}
	_, _, err := p.Generate(context.Background(), "prompt", Settings{})
	if err == nil {
		t.Fatal("expected error for non-200 status")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error should contain status code 429, got: %s", err.Error())
	}
}

func TestGeminiMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	p := &GeminiProvider{apiKey: "test-key", apiURL: srv.URL, client: srv.Client()}
	_, _, err := p.Generate(context.Background(), "prompt", Settings{})
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if !strings.Contains(err.Error(), "parse response") {
		t.Errorf("error should mention parse, got: %s", err.Error())
	}
}

func TestGeminiNoCandidates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := geminiResponse{Candidates: []geminiCandidate{}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &GeminiProvider{apiKey: "test-key", apiURL: srv.URL, client: srv.Client()}
	_, _, err := p.Generate(context.Background(), "prompt", Settings{})
	if err == nil {
		t.Fatal("expected error for no candidates")
	}
	if !strings.Contains(err.Error(), "no candidates") {
		t.Errorf("error should mention 'no candidates', got: %s", err.Error())
	}
}

func TestGeminiTruncation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := geminiResponse{
			Candidates: []geminiCandidate{
				{
					Content: geminiContent{
						Parts: []geminiPart{{Text: `{"partial": true}`}},
					},
					FinishReason: "MAX_TOKENS",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &GeminiProvider{apiKey: "test-key", apiURL: srv.URL, client: srv.Client()}
	_, _, err := p.Generate(context.Background(), "prompt", Settings{MaxTokens: 100})
	if err == nil {
		t.Fatal("expected error for truncated response")
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Errorf("error should mention 'truncated', got: %s", err.Error())
	}
}

func TestGeminiSeedPassthrough(t *testing.T) {
	seed := 42
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody geminiRequest
		_ = json.NewDecoder(r.Body).Decode(&reqBody)

		if reqBody.GenerationConfig.Seed == nil {
			t.Error("expected seed to be set")
		} else if *reqBody.GenerationConfig.Seed != 42 {
			t.Errorf("expected seed 42, got %d", *reqBody.GenerationConfig.Seed)
		}

		resp := geminiResponse{
			Candidates: []geminiCandidate{
				{Content: geminiContent{Parts: []geminiPart{{Text: `{"ok": true}`}}}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &GeminiProvider{apiKey: "test-key", apiURL: srv.URL, client: srv.Client()}
	_, _, err := p.Generate(context.Background(), "prompt", Settings{Seed: &seed})
	if err != nil {
		t.Fatal(err)
	}
}

// --- SanitizeJSON tests ---

func TestSanitizeJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "valid JSON unchanged",
			input: `{"key": "value"}`,
			want:  `{"key": "value"}`,
		},
		{
			name:  "valid escapes preserved",
			input: `{"key": "line1\nline2\ttab\\backslash\"quote"}`,
			want:  `{"key": "line1\nline2\ttab\\backslash\"quote"}`,
		},
		{
			name:  "invalid \\s escaped",
			input: `{"pattern": "\\s+"}`,
			want:  `{"pattern": "\\s+"}`,
		},
		{
			name:  "bare invalid escape",
			input: `{"regex": "\s\d\w"}`,
			want:  `{"regex": "\\s\\d\\w"}`,
		},
		{
			name:  "mixed valid and invalid",
			input: `{"msg": "line\nnew\sthing"}`,
			want:  `{"msg": "line\nnew\\sthing"}`,
		},
		{
			name:  "unicode escape preserved",
			input: `{"char": "\u0041"}`,
			want:  `{"char": "\u0041"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeJSON(tt.input)
			if got != tt.want {
				t.Errorf("SanitizeJSON(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
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
		{
			name:  "prose before code fence",
			input: "Here is the corrected JSON:\n```json\n{\"key\": \"value\"}\n```",
			want:  `{"key": "value"}`,
		},
		{
			name:  "prose before bare fence",
			input: "Sure, here you go:\n```\n{\"key\": \"value\"}\n```\nHope this helps!",
			want:  `{"key": "value"}`,
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
	noSleep(t) // 429 is retried; do not wait for real backoff
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error": "rate limited"}`))
	}))
	defer srv.Close()

	p := &AnthropicProvider{apiKey: "test-key", apiURL: srv.URL, client: srv.Client()}
	_, _, err := p.Generate(context.Background(), "prompt", Settings{})
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
		_, _ = w.Write([]byte(`not json at all`))
	}))
	defer srv.Close()

	p := &AnthropicProvider{apiKey: "test-key", apiURL: srv.URL, client: srv.Client()}
	_, _, err := p.Generate(context.Background(), "prompt", Settings{})
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if !strings.Contains(err.Error(), "parse response") {
		t.Errorf("error should mention parse, got: %s", err.Error())
	}
}

func TestAnthropicTruncation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := anthropicResponse{
			Content: []anthropicContentBlock{
				{Type: "text", Text: `{"partial": true}`},
			},
			StopReason: "max_tokens",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &AnthropicProvider{apiKey: "test-key", apiURL: srv.URL, client: srv.Client()}
	_, _, err := p.Generate(context.Background(), "prompt", Settings{MaxTokens: 100})
	if err == nil {
		t.Fatal("expected error for truncated response")
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Errorf("error should mention 'truncated', got: %s", err.Error())
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
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &AnthropicProvider{apiKey: "test-key", apiURL: srv.URL, client: srv.Client()}
	_, _, err := p.Generate(context.Background(), "prompt", Settings{})
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
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &AnthropicProvider{apiKey: "test-key", apiURL: srv.URL, client: srv.Client()}
	_, _, err := p.Generate(context.Background(), "prompt", Settings{})
	if err == nil {
		t.Fatal("expected error for empty content blocks")
	}
	if !strings.Contains(err.Error(), "no text content") {
		t.Errorf("error should mention 'no text content', got: %s", err.Error())
	}
}

// --- OpenAI error path tests ---

func TestOpenAINon200Status(t *testing.T) {
	noSleep(t) // 500 is retried; do not wait for real backoff
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error": "server error"}`))
	}))
	defer srv.Close()

	p := &OpenAIProvider{apiKey: "test-key", apiURL: srv.URL, client: srv.Client()}
	_, _, err := p.Generate(context.Background(), "prompt", Settings{})
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
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	p := &OpenAIProvider{apiKey: "test-key", apiURL: srv.URL, client: srv.Client()}
	_, _, err := p.Generate(context.Background(), "prompt", Settings{})
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
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &OpenAIProvider{apiKey: "test-key", apiURL: srv.URL, client: srv.Client()}
	_, _, err := p.Generate(context.Background(), "prompt", Settings{})
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
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &OpenAIProvider{apiKey: "test-key", apiURL: srv.URL, client: srv.Client()}
	_, _, err := p.Generate(context.Background(), "prompt", Settings{MaxTokens: 100})
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
		_ = json.NewDecoder(r.Body).Decode(&reqBody)

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
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &OpenAIProvider{apiKey: "test-key", apiURL: srv.URL, client: srv.Client()}
	_, _, err := p.Generate(context.Background(), "prompt", Settings{Seed: &seed})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOpenAISeedOmittedWhenNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var raw map[string]json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&raw)

		if _, hasSeed := raw["seed"]; hasSeed {
			t.Error("seed should be omitted from request when nil")
		}

		resp := openaiResponse{
			Choices: []openaiChoice{
				{Message: openaiMessage{Content: `{"ok": true}`}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &OpenAIProvider{apiKey: "test-key", apiURL: srv.URL, client: srv.Client()}
	_, _, err := p.Generate(context.Background(), "prompt", Settings{})
	if err != nil {
		t.Fatal(err)
	}
}

func TestEffectiveModel(t *testing.T) {
	anth := &AnthropicProvider{}
	tests := []struct {
		name      string
		p         Provider
		requested string
		want      string
	}{
		{"provider default", anth, "", anthropicDefaultModel},
		{"requested wins over default", anth, "claude-x", "claude-x"},
		{"override wrapper wins", &modelOverride{Provider: anth, model: "claude-y"}, "ignored", "claude-y"},
		{"mock has no default", &MockProvider{}, "", ""},
		{"mock with requested", &MockProvider{}, "m", "m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EffectiveModel(tt.p, tt.requested); got != tt.want {
				t.Errorf("EffectiveModel = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- structured output ---

const testSchema = `{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`

func captureAnthropicRequest(t *testing.T, model string, schema json.RawMessage) map[string]any {
	t.Helper()
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		resp := anthropicResponse{Content: []anthropicContentBlock{{Type: "text", Text: "{}"}}, StopReason: "end_turn"}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()
	p := &AnthropicProvider{apiKey: "k", apiURL: srv.URL, client: srv.Client()}
	if _, _, err := p.Generate(context.Background(), "hi", Settings{Model: model, OutputSchema: schema}); err != nil {
		t.Fatal(err)
	}
	return captured
}

func TestAnthropicStructuredOutputGatedByModel(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{"claude-sonnet-5-5", true},
		{"claude-sonnet-5", true},
		{"claude-opus-5-5", true},
		{"claude-opus-4-8", true},
		{"claude-haiku-4-5", true},
		{"claude-fable-5-1", true},
		{"claude-sonnet-4-5", true},
		{"claude-sonnet-4-5-20250929", true},
		{"claude-sonnet-4-6", true}, // confirmed live
		{"claude-opus-4-6", true},   // confirmed live
		{"claude-opus-4-7", true},
		{"claude-sonnet-4-20250514", false},
		{"claude-opus-4-20250514", false},
		{"claude-3-7-sonnet-latest", false},
		{"claude-3-5-haiku-20241022", false},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			req := captureAnthropicRequest(t, tt.model, json.RawMessage(testSchema))
			oc, present := req["output_config"]
			if present != tt.want {
				t.Fatalf("output_config present=%v, want %v", present, tt.want)
			}
			if !tt.want {
				return
			}
			format := oc.(map[string]any)["format"].(map[string]any)
			if format["type"] != "json_schema" {
				t.Errorf("format.type = %v", format["type"])
			}
			if _, ok := format["schema"].(map[string]any); !ok {
				t.Errorf("format.schema missing or not an object: %v", format["schema"])
			}
		})
	}
}

func TestAnthropicNoSchemaNoOutputConfig(t *testing.T) {
	req := captureAnthropicRequest(t, "claude-sonnet-5-5", nil)
	if _, ok := req["output_config"]; ok {
		t.Error("output_config must be absent when no schema is supplied")
	}
}

func TestOpenAIStructuredOutput(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		resp := openaiResponse{Choices: []openaiChoice{{Message: openaiMessage{Role: "assistant", Content: "{}"}, FinishReason: "stop"}}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()
	p := &OpenAIProvider{apiKey: "k", apiURL: srv.URL, client: srv.Client()}

	if _, _, err := p.Generate(context.Background(), "hi", Settings{OutputSchema: json.RawMessage(testSchema)}); err != nil {
		t.Fatal(err)
	}
	rf := captured["response_format"].(map[string]any)
	if rf["type"] != "json_schema" {
		t.Fatalf("response_format.type = %v", rf["type"])
	}
	js := rf["json_schema"].(map[string]any)
	if js["strict"] != true || js["name"] == "" {
		t.Errorf("json_schema should be strict and named: %v", js)
	}
	if _, ok := js["schema"].(map[string]any); !ok {
		t.Errorf("json_schema.schema missing: %v", js["schema"])
	}

	if _, _, err := p.Generate(context.Background(), "hi", Settings{}); err != nil {
		t.Fatal(err)
	}
	if rf := captured["response_format"].(map[string]any); rf["type"] != "json_object" {
		t.Errorf("without a schema response_format.type should be json_object, got %v", rf["type"])
	}
}

func TestGeminiStructuredOutput(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		resp := geminiResponse{Candidates: []geminiCandidate{{Content: geminiContent{Parts: []geminiPart{{Text: "{}"}}}, FinishReason: "STOP"}}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()
	p := &GeminiProvider{apiKey: "k", apiURL: srv.URL, client: srv.Client()}

	if _, _, err := p.Generate(context.Background(), "hi", Settings{OutputSchema: json.RawMessage(testSchema)}); err != nil {
		t.Fatal(err)
	}
	gc := captured["generationConfig"].(map[string]any)
	if _, ok := gc["responseJsonSchema"].(map[string]any); !ok {
		t.Errorf("generationConfig.responseJsonSchema missing: %v", gc)
	}
	if gc["responseMimeType"] != "application/json" {
		t.Errorf("responseMimeType should still be application/json, got %v", gc["responseMimeType"])
	}

	if _, _, err := p.Generate(context.Background(), "hi", Settings{}); err != nil {
		t.Fatal(err)
	}
	if gc := captured["generationConfig"].(map[string]any); gc["responseJsonSchema"] != nil {
		t.Error("responseJsonSchema must be absent when no schema is supplied")
	}
}

func TestAnthropicTemperatureGatedByModel(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{"claude-sonnet-4-6", true},
		{"claude-opus-4-6", true},
		{"claude-haiku-4-5", true},
		{"claude-sonnet-5-5", false},
		{"claude-sonnet-5", false},
		{"claude-opus-5-5", false},
		{"claude-opus-4-8", false},
		{"claude-opus-4-7", false},
		{"claude-fable-5-1", false},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			req := captureAnthropicRequest(t, tt.model, nil)
			_, present := req["temperature"]
			if present != tt.want {
				t.Errorf("temperature present=%v, want %v", present, tt.want)
			}
		})
	}
}

func TestOpenAIStructuredOutputGatedByModel(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		resp := openaiResponse{Choices: []openaiChoice{{Message: openaiMessage{Role: "assistant", Content: "{}"}, FinishReason: "stop"}}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()
	p := &OpenAIProvider{apiKey: "k", apiURL: srv.URL, client: srv.Client()}

	tests := []struct {
		model string
		want  string
	}{
		{"gpt-5.2", "json_schema"},
		{"gpt-5-mini", "json_schema"},
		{"gpt-4o", "json_schema"},
		{"gpt-4o-mini", "json_schema"},
		{"gpt-4.1-mini", "json_schema"},
		{"o3", "json_schema"},
		{"gpt-4o-2024-08-06", "json_schema"},
		{"gpt-4-turbo", "json_object"},
		{"gpt-4", "json_object"},
		{"gpt-3.5-turbo", "json_object"},
		{"gpt-4o-2024-05-13", "json_object"},
		{"chatgpt-4o-latest", "json_object"},
		{"o1-mini", "json_object"},
		{"o1-preview", "json_object"},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			if _, _, err := p.Generate(context.Background(), "hi", Settings{Model: tt.model, OutputSchema: json.RawMessage(testSchema)}); err != nil {
				t.Fatal(err)
			}
			rf := captured["response_format"].(map[string]any)
			if rf["type"] != tt.want {
				t.Errorf("response_format.type = %v, want %v", rf["type"], tt.want)
			}
		})
	}
}

// --- runtime fallback when a provider rejects a feature ---

func TestAnthropicRetriesWithoutRejectedFeatures(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case b["output_config"] != nil:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"output_config: Extra inputs are not permitted"}}`))
		case b["temperature"] != nil:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("{\"type\":\"error\",\"error\":{\"type\":\"invalid_request_error\",\"message\":\"`temperature` is deprecated for this model.\"}}"))
		default:
			_ = json.NewEncoder(w).Encode(anthropicResponse{Content: []anthropicContentBlock{{Type: "text", Text: "{}"}}, StopReason: "end_turn"})
		}
	}))
	defer srv.Close()
	p := &AnthropicProvider{apiKey: "k", apiURL: srv.URL, client: srv.Client()}

	// Sonnet 4.6 is believed to accept both, so the first request carries both.
	out, _, err := p.Generate(context.Background(), "hi", Settings{Model: "claude-sonnet-4-6", Temperature: 0.2, OutputSchema: json.RawMessage(testSchema)})
	if err != nil {
		t.Fatalf("expected fallback to succeed, got %v", err)
	}
	if out != "{}" {
		t.Errorf("unexpected output %q", out)
	}
	if len(bodies) != 3 {
		t.Fatalf("expected 3 requests (schema+temp, temp only, neither), got %d", len(bodies))
	}
	if bodies[1]["output_config"] != nil || bodies[1]["temperature"] == nil {
		t.Errorf("second request should drop only output_config: %v", bodies[1])
	}
	if bodies[2]["output_config"] != nil || bodies[2]["temperature"] != nil {
		t.Errorf("third request should carry neither feature: %v", bodies[2])
	}
}

func TestAnthropicUnrelated400IsNotRetried(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens: must be positive"}}`))
	}))
	defer srv.Close()
	p := &AnthropicProvider{apiKey: "k", apiURL: srv.URL, client: srv.Client()}
	_, _, err := p.Generate(context.Background(), "hi", Settings{Model: "claude-sonnet-4-6", OutputSchema: json.RawMessage(testSchema)})
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("expected a 400 error, got %v", err)
	}
	if calls != 1 {
		t.Errorf("an unrelated 400 must not be retried, got %d calls", calls)
	}
}

func TestOpenAIRetriesInJSONModeWhenSchemaRejected(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		w.Header().Set("Content-Type", "application/json")
		if rf, _ := b["response_format"].(map[string]any); rf["type"] == "json_schema" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Invalid parameter: 'response_format' of type 'json_schema' is not supported with this model.","type":"invalid_request_error"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(openaiResponse{Choices: []openaiChoice{{Message: openaiMessage{Role: "assistant", Content: "{}"}, FinishReason: "stop"}}})
	}))
	defer srv.Close()
	p := &OpenAIProvider{apiKey: "k", apiURL: srv.URL, client: srv.Client()}
	if _, _, err := p.Generate(context.Background(), "hi", Settings{Model: "gpt-5.2", OutputSchema: json.RawMessage(testSchema)}); err != nil {
		t.Fatalf("expected fallback to succeed, got %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(bodies))
	}
	if rf := bodies[1]["response_format"].(map[string]any); rf["type"] != "json_object" {
		t.Errorf("retry should use json_object, got %v", rf)
	}
}

func TestGeminiRetriesWithoutSchemaWhenRejected(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		w.Header().Set("Content-Type", "application/json")
		if gc, _ := b["generationConfig"].(map[string]any); gc["responseJsonSchema"] != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":400,"message":"Invalid JSON payload received. Unknown name \"response_json_schema\" at 'generation_config'","status":"INVALID_ARGUMENT"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(geminiResponse{Candidates: []geminiCandidate{{Content: geminiContent{Parts: []geminiPart{{Text: "{}"}}}, FinishReason: "STOP"}}})
	}))
	defer srv.Close()
	p := &GeminiProvider{apiKey: "k", apiURL: srv.URL, client: srv.Client()}
	if _, _, err := p.Generate(context.Background(), "hi", Settings{OutputSchema: json.RawMessage(testSchema)}); err != nil {
		t.Fatalf("expected fallback to succeed, got %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(bodies))
	}
	if gc := bodies[1]["generationConfig"].(map[string]any); gc["responseJsonSchema"] != nil {
		t.Errorf("retry should drop responseJsonSchema: %v", gc)
	}
}

// --- truncation salvage ---

func TestSalvageJSON(t *testing.T) {
	issue := func(id string) string {
		return `{"id":"` + id + `","severity":"WARN","category":"AMBIGUITY","title":"t","description":"d","evidence":[{"source":"plan","path":"p","line_start":1,"line_end":1}],"impact":"i","recommendation":"r","blocking":false,"tags":[]}`
	}
	full := `{"summary":{"verdict":"EXECUTABLE_AS_IS"},"questions":[],"issues":[` + issue("ISSUE-0001") + `,` + issue("ISSUE-0002") + `]}`

	tests := []struct {
		name       string
		in         string
		wantOK     bool
		wantIssues int
	}{
		{"already valid", full, true, 2},
		{"cut mid second issue", `{"summary":{"verdict":"EXECUTABLE_AS_IS"},"questions":[],"issues":[` + issue("ISSUE-0001") + `,{"id":"ISSUE-0002","sev`, true, 1},
		{"cut right after comma", `{"summary":{"verdict":"EXECUTABLE_AS_IS"},"questions":[],"issues":[` + issue("ISSUE-0001") + `,`, true, 1},
		{"cut inside a string with escaped quote", `{"summary":{"verdict":"EXECUTABLE_AS_IS"},"questions":[],"issues":[` + issue("ISSUE-0001") + `,{"id":"ISSUE-0002","title":"say \"hi\" and then`, true, 1},
		{"cut after questions, before issues", `{"summary":{"verdict":"EXECUTABLE_AS_IS"},"questions":[],"issues":[{"id":"ISSUE-00`, true, 0},
		{"cut inside summary", `{"summary":{"verdict":"EXECUTABLE_AS`, false, 0},
		{"summary last and cut off", `{"issues":[` + issue("ISSUE-0001") + `],"questions":[],"summary":{"verd`, true, 1},
		{"garbage", `not json`, false, 0},
		{"empty", ``, false, 0},
		{"unbalanced close", `]}`, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := SalvageJSON(tt.in)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (got %q)", ok, tt.wantOK, got)
			}
			if !ok {
				return
			}
			var doc struct {
				Issues []map[string]any `json:"issues"`
			}
			if err := json.Unmarshal([]byte(got), &doc); err != nil {
				t.Fatalf("salvaged output is not valid JSON: %v\n%s", err, got)
			}
			if len(doc.Issues) != tt.wantIssues {
				t.Errorf("salvaged %d issues, want %d:\n%s", len(doc.Issues), tt.wantIssues, got)
			}
		})
	}
}

func TestSalvageJSONRootArray(t *testing.T) {
	got, ok := SalvageJSON(`[{"id":1},{"id":`)
	if !ok || got != `[{"id":1}]` {
		t.Errorf("root array should be salvaged to [{\"id\":1}], got ok=%v %q", ok, got)
	}
}

func TestProvidersReturnTypedTruncationError(t *testing.T) {
	t.Run("anthropic", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(anthropicResponse{Content: []anthropicContentBlock{{Type: "text", Text: `{"partial`}}, StopReason: "max_tokens"})
		}))
		defer srv.Close()
		p := &AnthropicProvider{apiKey: "k", apiURL: srv.URL, client: srv.Client()}
		_, _, err := p.Generate(context.Background(), "x", Settings{MaxTokens: 7})
		var te *TruncatedError
		if !errors.As(err, &te) || te.Partial != `{"partial` || te.MaxTokens != 7 {
			t.Errorf("want TruncatedError with partial text, got %#v", err)
		}
	})
	t.Run("openai", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(openaiResponse{Choices: []openaiChoice{{Message: openaiMessage{Content: `{"partial`}, FinishReason: "length"}}})
		}))
		defer srv.Close()
		p := &OpenAIProvider{apiKey: "k", apiURL: srv.URL, client: srv.Client()}
		_, _, err := p.Generate(context.Background(), "x", Settings{MaxTokens: 7})
		var te *TruncatedError
		if !errors.As(err, &te) || te.Partial != `{"partial` {
			t.Errorf("want TruncatedError with partial text, got %#v", err)
		}
	})
	t.Run("gemini", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(geminiResponse{Candidates: []geminiCandidate{{Content: geminiContent{Parts: []geminiPart{{Text: `{"partial`}}}, FinishReason: "MAX_TOKENS"}}})
		}))
		defer srv.Close()
		p := &GeminiProvider{apiKey: "k", apiURL: srv.URL, client: srv.Client()}
		_, _, err := p.Generate(context.Background(), "x", Settings{MaxTokens: 7})
		var te *TruncatedError
		if !errors.As(err, &te) || te.Partial != `{"partial` {
			t.Errorf("want TruncatedError with partial text, got %#v", err)
		}
	})
}

// --- effort, fast tier, thinking ---

func TestValidEffortAndFastModel(t *testing.T) {
	for _, e := range append([]string{""}, ValidEfforts...) {
		if !ValidEffort(e) {
			t.Errorf("%q should be valid", e)
		}
	}
	for _, e := range []string{"LOW", "ultra", "1"} {
		if ValidEffort(e) {
			t.Errorf("%q should be invalid", e)
		}
	}
	if FastModel("anthropic") == "" || FastModel("openai") == "" || FastModel("gemini") == "" || FastModel("mock") != "" {
		t.Error("fast tier should be defined for the three providers only")
	}
}

func TestAnthropicEffortAndThinking(t *testing.T) {
	capture := func(model, effort string, schema json.RawMessage) map[string]any {
		var captured map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&captured)
			_ = json.NewEncoder(w).Encode(anthropicResponse{Content: []anthropicContentBlock{{Type: "text", Text: "{}"}}, StopReason: "end_turn"})
		}))
		defer srv.Close()
		p := &AnthropicProvider{apiKey: "k", apiURL: srv.URL, client: srv.Client()}
		if _, _, err := p.Generate(context.Background(), "hi", Settings{Model: model, Effort: effort, OutputSchema: schema}); err != nil {
			t.Fatal(err)
		}
		return captured
	}

	req := capture("claude-opus-5-5", "low", json.RawMessage(testSchema))
	oc := req["output_config"].(map[string]any)
	if oc["effort"] != "low" || oc["format"] == nil {
		t.Errorf("effort should sit beside the format in output_config: %v", oc)
	}
	if req["thinking"] != nil {
		t.Error("5.x models think by default; no thinking block should be sent")
	}

	req = capture("claude-sonnet-4-6", "high", nil)
	if oc := req["output_config"].(map[string]any); oc["effort"] != "high" || oc["format"] != nil {
		t.Errorf("effort without a schema should produce output_config with effort only: %v", oc)
	}
	if th, _ := req["thinking"].(map[string]any); th["type"] != "adaptive" {
		t.Errorf("4.6 models need adaptive thinking turned on for effort to apply: %v", req["thinking"])
	}

	req = capture("claude-opus-5-5", "", nil)
	if req["output_config"] != nil || req["thinking"] != nil {
		t.Error("no effort and no schema: neither output_config nor thinking should be sent")
	}
}

func TestAnthropicDropsOnlyEffortWhenRejected(t *testing.T) {
	noSleep(t)
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		if oc, _ := b["output_config"].(map[string]any); oc["effort"] != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"output_config.effort: not supported on this model"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(anthropicResponse{Content: []anthropicContentBlock{{Type: "text", Text: "{}"}}, StopReason: "end_turn"})
	}))
	defer srv.Close()
	p := &AnthropicProvider{apiKey: "k", apiURL: srv.URL, client: srv.Client()}
	if _, _, err := p.Generate(context.Background(), "hi", Settings{Model: "claude-opus-5-5", Effort: "max", OutputSchema: json.RawMessage(testSchema)}); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 {
		t.Fatalf("expected one retry, got %d requests", len(bodies))
	}
	oc := bodies[1]["output_config"].(map[string]any)
	if oc["effort"] != nil || oc["format"] == nil {
		t.Errorf("retry should drop effort but keep the schema: %v", oc)
	}
}

func TestOpenAIReasoningEffortAndTemperatureFallback(t *testing.T) {
	noSleep(t)
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		if b["temperature"] != nil && strings.HasPrefix(b["model"].(string), "gpt-5") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Unsupported value: 'temperature' does not support 0.2 with this model. Only the default (1) value is supported.","type":"invalid_request_error"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(openaiResponse{Choices: []openaiChoice{{Message: openaiMessage{Content: "{}"}, FinishReason: "stop"}}})
	}))
	defer srv.Close()
	p := &OpenAIProvider{apiKey: "k", apiURL: srv.URL, client: srv.Client()}

	if _, _, err := p.Generate(context.Background(), "hi", Settings{Model: "gpt-5.2", Effort: "xhigh", Temperature: 0.2}); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || bodies[1]["temperature"] != nil {
		t.Errorf("a rejected temperature should be dropped on retry: %d requests, second=%v", len(bodies), bodies[len(bodies)-1])
	}
	if bodies[0]["reasoning_effort"] != "xhigh" || bodies[1]["reasoning_effort"] != "xhigh" {
		t.Errorf("xhigh should pass through and survive the temperature retry: %v", bodies)
	}

	bodies = nil
	if _, _, err := p.Generate(context.Background(), "hi", Settings{Model: "gpt-4o", Effort: "low", Temperature: 0.2}); err != nil {
		t.Fatal(err)
	}
	if bodies[0]["reasoning_effort"] != nil {
		t.Error("non-reasoning models must not be sent reasoning_effort")
	}
}

func TestOpenAIReasoningEffortStepsDownThenDrops(t *testing.T) {
	noSleep(t)
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		if b["reasoning_effort"] != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Unsupported value for reasoning_effort"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(openaiResponse{Choices: []openaiChoice{{Message: openaiMessage{Content: "{}"}, FinishReason: "stop"}}})
	}))
	defer srv.Close()
	p := &OpenAIProvider{apiKey: "k", apiURL: srv.URL, client: srv.Client()}

	if _, _, err := p.Generate(context.Background(), "hi", Settings{Model: "o3", Effort: "max"}); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 3 || bodies[0]["reasoning_effort"] != "xhigh" || bodies[1]["reasoning_effort"] != "high" || bodies[2]["reasoning_effort"] != nil {
		t.Errorf("max -> xhigh should step down to high, then drop: %v", bodies)
	}

	bodies = nil
	if _, _, err := p.Generate(context.Background(), "hi", Settings{Model: "o3", Effort: "medium"}); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || bodies[1]["reasoning_effort"] != nil {
		t.Errorf("a rejected non-xhigh effort should be dropped directly: %v", bodies)
	}
}

func TestGeminiThinkingLevel(t *testing.T) {
	noSleep(t)
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		gc := b["generationConfig"].(map[string]any)
		if tc, _ := gc["thinkingConfig"].(map[string]any); tc["thinkingLevel"] == "high" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":400,"message":"Invalid value at 'generation_config.thinking_config.thinking_level'","status":"INVALID_ARGUMENT"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(geminiResponse{Candidates: []geminiCandidate{{Content: geminiContent{Parts: []geminiPart{{Text: "{}"}}}, FinishReason: "STOP"}}})
	}))
	defer srv.Close()
	p := &GeminiProvider{apiKey: "k", apiURL: srv.URL, client: srv.Client()}

	if _, _, err := p.Generate(context.Background(), "hi", Settings{Model: "gemini-3-flash-preview", Effort: "medium"}); err != nil {
		t.Fatal(err)
	}
	gc := bodies[0]["generationConfig"].(map[string]any)
	if tc, _ := gc["thinkingConfig"].(map[string]any); tc["thinkingLevel"] != "medium" || tc["thinkingBudget"] != nil {
		t.Errorf("Gemini 3: effort medium should become thinkingLevel medium: %v", gc)
	}

	bodies = nil
	if _, _, err := p.Generate(context.Background(), "hi", Settings{Model: FastModel("gemini"), Effort: "low"}); err != nil {
		t.Fatal(err)
	}
	gc = bodies[0]["generationConfig"].(map[string]any)
	if tc, _ := gc["thinkingConfig"].(map[string]any); tc["thinkingBudget"] != float64(1024) || tc["thinkingLevel"] != nil {
		t.Errorf("Gemini 2.5 (the fast tier): effort low should become thinkingBudget 1024: %v", gc)
	}
	if _, _, err := p.Generate(context.Background(), "hi", Settings{Model: "models/gemini-2.5-pro", Effort: "max"}); err != nil {
		t.Fatal(err)
	}
	gc = bodies[len(bodies)-1]["generationConfig"].(map[string]any)
	if tc, _ := gc["thinkingConfig"].(map[string]any); tc["thinkingBudget"] != float64(24576) {
		t.Errorf("Gemini 2.5: effort max should become the top budget: %v", gc)
	}

	bodies = nil
	if _, _, err := p.Generate(context.Background(), "hi", Settings{Model: "gemini-3-flash-preview", Effort: "max"}); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || bodies[1]["generationConfig"].(map[string]any)["thinkingConfig"] != nil {
		t.Errorf("a rejected thinking level should be dropped on retry: %v", bodies)
	}
}
