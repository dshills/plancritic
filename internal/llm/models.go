package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	openaiModelsAPIURL    = "https://api.openai.com/v1/models"
	anthropicModelsAPIURL = "https://api.anthropic.com/v1/models"
	geminiModelsAPIURL    = "https://generativelanguage.googleapis.com/v1beta/models"
	modelsHTTPClient      = &http.Client{Timeout: 20 * time.Second}
	modelsURLMu           sync.RWMutex
	modelsCacheMu         sync.Mutex
	modelsCache           = map[string]modelsCacheEntry{}
	modelsInFlight        = map[string]*modelsInFlightEntry{}
)

const anthropicModelsAPIVersion = "2023-06-01"

type modelsCacheEntry struct {
	models    []ModelInfo
	expiresAt time.Time
}

type modelsInFlightEntry struct {
	done   chan struct{}
	models []ModelInfo
	err    error
}

func SetOpenAIModelsAPIURL(u string) {
	modelsURLMu.Lock()
	openaiModelsAPIURL = u
	modelsURLMu.Unlock()
	clearModelsCache()
}
func SetAnthropicModelsAPIURL(u string) {
	modelsURLMu.Lock()
	anthropicModelsAPIURL = u
	modelsURLMu.Unlock()
	clearModelsCache()
}
func SetGeminiModelsAPIURL(u string) {
	modelsURLMu.Lock()
	geminiModelsAPIURL = u
	modelsURLMu.Unlock()
	clearModelsCache()
}

func OpenAIModelsAPIURLForTest() string {
	modelsURLMu.RLock()
	defer modelsURLMu.RUnlock()
	return openaiModelsAPIURL
}

type ModelInfo struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
}

func IsSupportedProvider(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "anthropic", "openai", "gemini":
		return true
	default:
		return false
	}
}

func ListModels(ctx context.Context, provider string) ([]ModelInfo, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	modelsCacheMu.Lock()
	entry, ok := modelsCache[provider]
	if ok && time.Now().Before(entry.expiresAt) {
		models := cloneModels(entry.models)
		modelsCacheMu.Unlock()
		return models, nil
	}
	if inFlight := modelsInFlight[provider]; inFlight != nil {
		modelsCacheMu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-inFlight.done:
			if inFlight.err != nil {
				return nil, inFlight.err
			}
			return cloneModels(inFlight.models), nil
		}
	}
	inFlight := &modelsInFlightEntry{done: make(chan struct{})}
	modelsInFlight[provider] = inFlight
	modelsCacheMu.Unlock()
	completed := false
	defer func() {
		if completed {
			return
		}
		modelsCacheMu.Lock()
		inFlight.err = fmt.Errorf("model list failed")
		delete(modelsInFlight, provider)
		close(inFlight.done)
		modelsCacheMu.Unlock()
	}()

	var (
		models []ModelInfo
		err    error
	)
	fetchCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	switch provider {
	case "anthropic":
		models, err = listAnthropicModels(fetchCtx)
	case "openai":
		models, err = listOpenAIModels(fetchCtx)
	case "gemini":
		models, err = listGeminiModels(fetchCtx)
	default:
		err = fmt.Errorf("unknown provider %q", provider)
	}

	modelsCacheMu.Lock()
	inFlight.models = cloneModels(models)
	inFlight.err = err
	if err == nil {
		modelsCache[provider] = modelsCacheEntry{
			models:    cloneModels(models),
			expiresAt: time.Now().Add(time.Hour),
		}
	}
	delete(modelsInFlight, provider)
	completed = true
	close(inFlight.done)
	modelsCacheMu.Unlock()
	if err != nil {
		return nil, err
	}
	return cloneModels(models), nil
}

func listOpenAIModels(ctx context.Context) ([]ModelInfo, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY environment variable not set")
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	err := getModelsJSON(ctx, modelsAPIURL("openai"), map[string]string{"Authorization": "Bearer " + apiKey}, &payload)
	if err != nil {
		return nil, err
	}
	models := make([]ModelInfo, 0, len(payload.Data))
	for _, item := range payload.Data {
		if isUsableOpenAIReviewModel(item.ID) {
			models = append(models, ModelInfo{ID: item.ID})
		}
	}
	return sortedModels(models), nil
}

func listAnthropicModels(ctx context.Context) ([]ModelInfo, error) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY environment variable not set")
	}
	var payload struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	headers := map[string]string{"x-api-key": apiKey, "anthropic-version": anthropicModelsAPIVersion}
	err := getModelsJSON(ctx, modelsAPIURL("anthropic"), headers, &payload)
	if err != nil {
		return nil, err
	}
	models := make([]ModelInfo, 0, len(payload.Data))
	for _, item := range payload.Data {
		if isUsableAnthropicReviewModel(item.ID) {
			models = append(models, ModelInfo{ID: item.ID, DisplayName: item.DisplayName})
		}
	}
	return sortedModels(models), nil
}

func listGeminiModels(ctx context.Context) ([]ModelInfo, error) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY environment variable not set")
	}
	var payload struct {
		Models []struct {
			Name                       string   `json:"name"`
			BaseModelID                string   `json:"baseModelId"`
			DisplayName                string   `json:"displayName"`
			SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	endpoint, err := url.Parse(modelsAPIURL("gemini"))
	if err != nil {
		return nil, fmt.Errorf("invalid Gemini models URL: %w", err)
	}
	q := endpoint.Query()
	q.Set("pageSize", "1000")
	endpoint.RawQuery = q.Encode()
	err = getModelsJSON(ctx, endpoint.String(), map[string]string{"x-goog-api-key": apiKey}, &payload)
	if err != nil {
		return nil, err
	}
	models := make([]ModelInfo, 0, len(payload.Models))
	for _, item := range payload.Models {
		id := strings.TrimPrefix(item.Name, "models/")
		if isUsableGeminiReviewModel(id, item.SupportedGenerationMethods) {
			models = append(models, ModelInfo{ID: id, DisplayName: item.DisplayName})
		}
	}
	return sortedModels(dedupeModels(models)), nil
}

func getModelsJSON(ctx context.Context, endpoint string, headers map[string]string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("creating HTTP request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := modelsHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	const maxBodyBytes = 512 * 1024
	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return fmt.Errorf("reading response body: %w", err)
	}
	if len(respBytes) > maxBodyBytes {
		return fmt.Errorf("HTTP response exceeded %d bytes", maxBodyBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncateString(string(respBytes), 200))
	}
	if err := json.Unmarshal(respBytes, dst); err != nil {
		return fmt.Errorf("parsing response JSON (HTTP %d): %w", resp.StatusCode, err)
	}
	return nil
}

func modelsAPIURL(provider string) string {
	modelsURLMu.RLock()
	defer modelsURLMu.RUnlock()
	switch provider {
	case "anthropic":
		return anthropicModelsAPIURL
	case "gemini":
		return geminiModelsAPIURL
	default:
		return openaiModelsAPIURL
	}
}

func isUsableOpenAIReviewModel(id string) bool {
	id = strings.ToLower(id)
	if hasAnyPrefix(id, "text-embedding-", "embedding-", "dall-e-", "tts-", "whisper-", "omni-moderation-", "text-moderation-") {
		return false
	}
	if strings.Contains(id, "audio") ||
		strings.Contains(id, "realtime") ||
		strings.Contains(id, "transcribe") ||
		strings.Contains(id, "transcription") ||
		strings.Contains(id, "image") ||
		strings.Contains(id, "vision") ||
		strings.Contains(id, "speech") ||
		strings.Contains(id, "moderation") ||
		strings.Contains(id, "embedding") {
		return false
	}
	return strings.HasPrefix(id, "gpt-") || isOpenAIReasoningModel(id)
}

func isOpenAIReasoningModel(id string) bool {
	return strings.HasPrefix(id, "o1") || strings.HasPrefix(id, "o3") || strings.HasPrefix(id, "o4")
}

func isUsableAnthropicReviewModel(id string) bool {
	id = strings.ToLower(id)
	return strings.HasPrefix(id, "claude-") &&
		!strings.Contains(id, "embedding") &&
		!strings.Contains(id, "image") &&
		!strings.Contains(id, "audio")
}

func isUsableGeminiReviewModel(id string, methods []string) bool {
	id = strings.ToLower(id)
	if strings.Contains(id, "embedding") ||
		strings.Contains(id, "embed") ||
		strings.Contains(id, "imagen") ||
		strings.Contains(id, "image") ||
		strings.Contains(id, "veo") ||
		strings.Contains(id, "aqa") ||
		strings.Contains(id, "tts") ||
		strings.Contains(id, "audio") {
		return false
	}
	return supportsGenerateContent(methods)
}

func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

func supportsGenerateContent(methods []string) bool {
	for _, method := range methods {
		if method == "generateContent" || method == "streamGenerateContent" {
			return true
		}
	}
	return false
}

func sortedModels(models []ModelInfo) []ModelInfo {
	sort.Slice(models, func(i, j int) bool {
		return models[i].ID < models[j].ID
	})
	return models
}

func dedupeModels(models []ModelInfo) []ModelInfo {
	seen := map[string]bool{}
	out := make([]ModelInfo, 0, len(models))
	for _, model := range models {
		if model.ID == "" || seen[model.ID] {
			continue
		}
		seen[model.ID] = true
		out = append(out, model)
	}
	return out
}

func clearModelsCache() {
	modelsCacheMu.Lock()
	defer modelsCacheMu.Unlock()
	modelsCache = map[string]modelsCacheEntry{}
	modelsInFlight = map[string]*modelsInFlightEntry{}
}

func cloneModels(models []ModelInfo) []ModelInfo {
	if len(models) == 0 {
		return []ModelInfo{}
	}
	out := make([]ModelInfo, len(models))
	copy(out, models)
	return out
}

func truncateString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	for max > 0 && (s[max]&0xc0) == 0x80 {
		max--
	}
	return s[:max] + "..."
}
