package llm

import (
	"context"
	"encoding/json"
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
