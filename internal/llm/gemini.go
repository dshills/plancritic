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
	"time"
)

const (
	geminiAPIBaseURL = "https://generativelanguage.googleapis.com/v1beta"
	// GeminiDefaultModel is the model used when Settings.Model is empty.
	// Exported so callers (e.g. cache-key hashing) can normalize against
	// the same default the provider uses internally.
	GeminiDefaultModel = "gemini-2.5-flash"
	geminiDefaultModel = GeminiDefaultModel
	// GeminiMinCacheChars is the local skip threshold (in characters)
	// before attempting to create a Gemini context cache. It is tuned
	// to the default Gemini 2.5 Flash/Pro minimum of ~1024 input
	// tokens, approximated at 4 chars/token. Older models (Gemini 1.5
	// Flash/Pro) require far more (~32k tokens); for those, requests
	// below the true minimum will be rejected by the API and the
	// caller (see cmd/plancritic.ensureGeminiCache) falls back to an
	// uncached generate call. Keeping this threshold low avoids
	// missing caching opportunities on the default model.
	GeminiMinCacheChars = 4096
)

// GeminiProvider implements Provider using the Google Gemini API.
type GeminiProvider struct {
	apiKey string
	apiURL string // base URL, e.g. "https://generativelanguage.googleapis.com/v1beta"
	client *http.Client
}

// NewGemini creates a Gemini provider using the GEMINI_API_KEY env var.
func NewGemini() (*GeminiProvider, error) {
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY environment variable not set")
	}
	return &GeminiProvider{apiKey: key, apiURL: geminiAPIBaseURL, client: &http.Client{Timeout: 5 * time.Minute}}, nil
}

func (g *GeminiProvider) Name() string { return "gemini" }

func (g *GeminiProvider) Generate(ctx context.Context, prompt string, s Settings) (string, Usage, error) {
	return g.GenerateSegments(ctx, []Segment{{Text: prompt}}, s)
}

// GenerateSegments sends a request composed of ordered segments. If
// Settings.CachedContentName is set, the contiguous prefix of segments
// whose CacheMark is true is assumed to already live in the server-side
// cache and is not re-sent; the cache resource is referenced via the
// cachedContent field instead. Gemini's cache model prepends cached
// content to the request contents, so cache-marked segments MUST form
// a contiguous prefix — otherwise the model would see the prompt in
// reordered form. Interleaved marks return an error.
func (g *GeminiProvider) GenerateSegments(ctx context.Context, segments []Segment, s Settings) (string, Usage, error) {
	model := s.Model
	if model == "" {
		model = geminiDefaultModel
	}

	maxTokens := s.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 16384
	}

	tail := segments
	if s.CachedContentName != "" {
		end, ok := contiguousCachePrefixEnd(segments)
		if !ok {
			return "", Usage{}, fmt.Errorf("gemini: cache-marked segments must form a contiguous prefix")
		}
		tail = segments[end:]
	}

	var body strings.Builder
	for _, seg := range tail {
		body.WriteString(seg.Text)
	}

	reqBody := geminiRequest{
		Contents: []geminiContent{
			{Parts: []geminiPart{{Text: body.String()}}},
		},
		GenerationConfig: geminiGenerationConfig{
			Temperature:      s.Temperature,
			MaxOutputTokens:  maxTokens,
			ResponseMIMEType: "application/json",
		},
	}
	if s.Seed != nil {
		reqBody.GenerationConfig.Seed = s.Seed
	}
	if s.CachedContentName != "" {
		reqBody.CachedContent = s.CachedContentName
	}

	raw, err := json.Marshal(reqBody)
	if err != nil {
		return "", Usage{}, fmt.Errorf("gemini: marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/models/%s:generateContent", g.apiURL, strings.TrimPrefix(model, "models/"))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return "", Usage{}, fmt.Errorf("gemini: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", g.apiKey)

	resp, err := g.client.Do(req)
	if err != nil {
		return "", Usage{}, fmt.Errorf("gemini: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", Usage{}, fmt.Errorf("gemini: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", Usage{}, fmt.Errorf("gemini: API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result geminiResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", Usage{}, fmt.Errorf("gemini: parse response: %w", err)
	}

	usage := Usage{
		InputTokens:          result.UsageMetadata.PromptTokenCount,
		OutputTokens:         result.UsageMetadata.CandidatesTokenCount,
		CacheReadInputTokens: result.UsageMetadata.CachedContentTokenCount,
	}

	if len(result.Candidates) == 0 {
		return "", usage, fmt.Errorf("gemini: no candidates in response")
	}

	candidate := result.Candidates[0]
	var out strings.Builder
	for _, part := range candidate.Content.Parts {
		out.WriteString(part.Text)
	}
	if candidate.FinishReason == "MAX_TOKENS" {
		return out.String(), usage, fmt.Errorf("gemini: response truncated (hit maxOutputTokens=%d)", maxTokens)
	}
	if out.Len() == 0 {
		return "", usage, fmt.Errorf("gemini: no text content in response")
	}
	return out.String(), usage, nil
}

// CreateCache uploads the cacheable prefix as a Gemini context-cache
// resource. Only the contiguous leading block of CacheMark=true
// segments is cached; marked segments that appear after an unmarked
// one return an error because Gemini's cache model prepends cached
// content and would therefore reorder the prompt.
func (g *GeminiProvider) CreateCache(ctx context.Context, segments []Segment, model string, ttl time.Duration) (CacheHandle, error) {
	if model == "" {
		model = geminiDefaultModel
	}

	end, ok := contiguousCachePrefixEnd(segments)
	if !ok {
		return CacheHandle{}, fmt.Errorf("gemini: cache-marked segments must form a contiguous prefix")
	}

	var body strings.Builder
	for _, seg := range segments[:end] {
		body.WriteString(seg.Text)
	}
	if body.Len() == 0 {
		return CacheHandle{}, fmt.Errorf("gemini: no cacheable segments provided")
	}
	if body.Len() < GeminiMinCacheChars {
		return CacheHandle{}, fmt.Errorf("gemini: cacheable prefix too small (%d chars, need ≥%d)", body.Len(), GeminiMinCacheChars)
	}

	reqBody := geminiCacheCreateRequest{
		Model: "models/" + strings.TrimPrefix(model, "models/"),
		Contents: []geminiContent{
			{Role: "user", Parts: []geminiPart{{Text: body.String()}}},
		},
		TTL: fmt.Sprintf("%ds", int(ttl.Seconds())),
	}

	raw, err := json.Marshal(reqBody)
	if err != nil {
		return CacheHandle{}, fmt.Errorf("gemini: marshal cache request: %w", err)
	}

	url := fmt.Sprintf("%s/cachedContents", g.apiURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return CacheHandle{}, fmt.Errorf("gemini: create cache request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", g.apiKey)

	resp, err := g.client.Do(req)
	if err != nil {
		return CacheHandle{}, fmt.Errorf("gemini: cache request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return CacheHandle{}, fmt.Errorf("gemini: read cache response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return CacheHandle{}, fmt.Errorf("gemini: cache API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result geminiCacheCreateResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return CacheHandle{}, fmt.Errorf("gemini: parse cache response: %w", err)
	}

	if result.Name == "" {
		return CacheHandle{}, fmt.Errorf("gemini: cache response missing name")
	}
	expiresAt := result.ExpireTime
	if expiresAt.IsZero() {
		expiresAt = time.Now().Add(ttl)
	}
	return CacheHandle{Name: result.Name, ExpiresAt: expiresAt}, nil
}

// contiguousCachePrefixEnd returns the index one past the last segment
// in the contiguous leading block of CacheMark=true segments, along
// with ok=false if any cache-marked segment appears after an unmarked
// one. When no segments are marked, returns (0, true).
func contiguousCachePrefixEnd(segments []Segment) (int, bool) {
	end := 0
	seenUnmarked := false
	for _, s := range segments {
		if s.CacheMark {
			if seenUnmarked {
				return 0, false
			}
			end++
		} else {
			seenUnmarked = true
		}
	}
	return end, true
}

type geminiRequest struct {
	Contents         []geminiContent        `json:"contents"`
	GenerationConfig geminiGenerationConfig `json:"generationConfig"`
	CachedContent    string                 `json:"cachedContent,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiGenerationConfig struct {
	Temperature      float64 `json:"temperature"`
	MaxOutputTokens  int     `json:"maxOutputTokens"`
	ResponseMIMEType string  `json:"responseMimeType,omitempty"`
	Seed             *int    `json:"seed,omitempty"`
}

type geminiResponse struct {
	Candidates    []geminiCandidate   `json:"candidates"`
	UsageMetadata geminiUsageMetadata `json:"usageMetadata"`
}

type geminiUsageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
}

type geminiCacheCreateRequest struct {
	Model    string          `json:"model"`
	Contents []geminiContent `json:"contents"`
	TTL      string          `json:"ttl"`
}

type geminiCacheCreateResponse struct {
	Name       string    `json:"name"`
	Model      string    `json:"model"`
	ExpireTime time.Time `json:"expireTime"`
}
