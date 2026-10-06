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
	anthropicAPIURL       = "https://api.anthropic.com/v1/messages"
	anthropicDefaultModel = "claude-opus-5-5"
	anthropicAPIVersion   = "2023-06-01"
)

// AnthropicProvider implements Provider using the Anthropic Messages API.
type AnthropicProvider struct {
	apiKey string
	apiURL string
	client *http.Client
}

// NewAnthropic creates an Anthropic provider using the ANTHROPIC_API_KEY env var.
func NewAnthropic() (*AnthropicProvider, error) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY environment variable not set")
	}
	return &AnthropicProvider{apiKey: key, apiURL: anthropicAPIURL, client: &http.Client{}}, nil
}

func (a *AnthropicProvider) Name() string { return "anthropic" }

// DefaultModel implements ModelDefaulter.
func (a *AnthropicProvider) DefaultModel() string { return anthropicDefaultModel }

func (a *AnthropicProvider) Generate(ctx context.Context, prompt string, s Settings) (string, Usage, error) {
	return a.GenerateSegments(ctx, []Segment{{Text: prompt}}, s)
}

// GenerateSegments sends a prompt composed of ordered segments, placing a
// cache_control breakpoint on any segment whose CacheMark is true.
func (a *AnthropicProvider) GenerateSegments(ctx context.Context, segments []Segment, s Settings) (string, Usage, error) {
	model := s.Model
	if model == "" {
		model = anthropicDefaultModel
	}

	maxTokens := s.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 16384
	}

	blocks := make([]anthropicContentBlock, 0, len(segments))
	for _, seg := range segments {
		if seg.Text == "" {
			continue
		}
		block := anthropicContentBlock{Type: "text", Text: seg.Text}
		if seg.CacheMark {
			block.CacheControl = &anthropicCacheControl{Type: "ephemeral"}
			// Agent loops routinely pause longer than the default 5-minute
			// cache lifetime between runs; a 1-hour entry costs 2x to
			// write but is read at 0.1x, so it pays off from the third
			// run in an hour.
			if s.CacheTTL >= time.Hour {
				block.CacheControl.TTL = "1h"
			}
		}
		blocks = append(blocks, block)
	}
	if len(blocks) == 0 {
		return "", Usage{}, fmt.Errorf("anthropic: empty prompt")
	}

	reqBody := anthropicRequest{
		Model:     model,
		MaxTokens: maxTokens,
		Messages: []anthropicMessage{
			{Role: "user", Content: blocks},
		},
	}
	if anthropicAcceptsTemperature(model) {
		reqBody.Temperature = &s.Temperature
	}
	if len(s.OutputSchema) > 0 && anthropicSupportsStructuredOutput(model) {
		reqBody.OutputConfig = &anthropicOutputConfig{
			Format: &anthropicOutputFormat{Type: "json_schema", Schema: s.OutputSchema},
		}
	}
	if s.Effort != "" {
		if reqBody.OutputConfig == nil {
			reqBody.OutputConfig = &anthropicOutputConfig{}
		}
		reqBody.OutputConfig.Effort = s.Effort
		// Effort controls thinking depth; on families where thinking is
		// off unless requested, turn adaptive thinking on so effort has
		// something to govern. Current-generation models already run
		// adaptive thinking by default.
		if anthropicThinkingOffByDefault(model) {
			reqBody.Thinking = &anthropicThinking{Type: "adaptive"}
		}
	}

	var respBody []byte
	for attempt := 0; ; attempt++ {
		res, err := sendWithRetry(ctx, "anthropic", s.OnRetry, func(ctx context.Context) (httpResult, error) {
			return a.post(ctx, reqBody)
		})
		if err != nil {
			return "", Usage{}, err
		}
		status, data := res.Status, res.Body
		if status == http.StatusOK {
			respBody = data
			break
		}
		// A 400 that names a feature this model does not accept is
		// retried without that feature, so a stale capability list
		// degrades to prompt-only JSON (or default sampling) instead of
		// failing the run. At most one retry per feature.
		if status == http.StatusBadRequest && attempt < 4 {
			msg := strings.ToLower(string(data))
			switch {
			case reqBody.OutputConfig != nil && reqBody.OutputConfig.Effort != "" && strings.Contains(msg, "effort"):
				reqBody.OutputConfig.Effort = ""
				if reqBody.OutputConfig.Format == nil {
					reqBody.OutputConfig = nil
				}
				continue
			case reqBody.Thinking != nil && strings.Contains(msg, "thinking"):
				reqBody.Thinking = nil
				continue
			case reqBody.OutputConfig != nil && strings.Contains(msg, "output_config"):
				reqBody.OutputConfig = nil
				continue
			case reqBody.Temperature != nil && strings.Contains(msg, "temperature"):
				reqBody.Temperature = nil
				continue
			case strings.Contains(msg, "ttl") && reqBody.clearCacheTTL():
				continue
			}
		}
		return "", Usage{}, fmt.Errorf("anthropic: API returned %d: %s", status, string(data))
	}

	var result anthropicResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", Usage{}, fmt.Errorf("anthropic: parse response: %w", err)
	}

	usage := Usage{
		InputTokens:              result.Usage.InputTokens,
		OutputTokens:             result.Usage.OutputTokens,
		CacheCreationInputTokens: result.Usage.CacheCreationInputTokens,
		CacheReadInputTokens:     result.Usage.CacheReadInputTokens,
	}

	var out strings.Builder
	for _, block := range result.Content {
		if block.Type == "text" {
			out.WriteString(block.Text)
		}
	}

	if result.StopReason == "max_tokens" {
		return out.String(), usage, &TruncatedError{Provider: "anthropic", MaxTokens: maxTokens, Partial: out.String()}
	}
	if out.Len() == 0 {
		return "", usage, fmt.Errorf("anthropic: no text content in response")
	}
	return out.String(), usage, nil
}

// post sends one Messages API request and returns the HTTP status and
// raw body. Transport and read failures are returned as errors; a
// non-200 status is not, so the caller can inspect the message.
func (a *AnthropicProvider) post(ctx context.Context, reqBody anthropicRequest) (httpResult, error) {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return httpResult{}, fmt.Errorf("anthropic: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.apiURL, bytes.NewReader(body))
	if err != nil {
		return httpResult{}, fmt.Errorf("anthropic: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", a.apiKey)
	req.Header.Set("Anthropic-Version", anthropicAPIVersion)

	resp, err := a.client.Do(req)
	if err != nil {
		return httpResult{}, fmt.Errorf("anthropic: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return httpResult{}, fmt.Errorf("anthropic: read response: %w", err)
	}
	return httpResult{Status: resp.StatusCode, Body: data, RetryAfter: resp.Header.Get("Retry-After")}, nil
}

type anthropicRequest struct {
	Model        string                 `json:"model"`
	MaxTokens    int                    `json:"max_tokens"`
	Temperature  *float64               `json:"temperature,omitempty"`
	Messages     []anthropicMessage     `json:"messages"`
	OutputConfig *anthropicOutputConfig `json:"output_config,omitempty"`
	Thinking     *anthropicThinking     `json:"thinking,omitempty"`
}

type anthropicOutputConfig struct {
	Format *anthropicOutputFormat `json:"format,omitempty"`
	Effort string                 `json:"effort,omitempty"`
}

type anthropicThinking struct {
	Type string `json:"type"`
}

// thinkingOffByDefaultPrefixes lists families that run without thinking
// unless it is requested explicitly. From the 5.x generation on,
// thinking is on by default.
var thinkingOffByDefaultPrefixes = []string{
	"claude-opus-4-6", "claude-opus-4-7", "claude-opus-4-8", "claude-sonnet-4-6",
}

func anthropicThinkingOffByDefault(model string) bool {
	m := strings.ToLower(model)
	for _, p := range thinkingOffByDefaultPrefixes {
		if strings.HasPrefix(m, p) {
			return true
		}
	}
	return false
}

type anthropicOutputFormat struct {
	Type   string          `json:"type"`
	Schema json.RawMessage `json:"schema"`
}

// noTemperatureModelPrefixes lists model families that return 400 when
// a temperature is supplied (sampling controls were removed starting
// with Opus 4.7 and the 5.x generation). Opus/Sonnet 4.6, Haiku 4.5, and
// older models still accept it. A model that unexpectedly rejects it is
// retried without by GenerateSegments.
var noTemperatureModelPrefixes = []string{
	"claude-fable-",
	"claude-mythos-",
	"claude-opus-5",
	"claude-opus-4-8",
	"claude-opus-4-7",
	"claude-sonnet-5",
}

func anthropicAcceptsTemperature(model string) bool {
	m := strings.ToLower(model)
	for _, p := range noTemperatureModelPrefixes {
		if strings.HasPrefix(m, p) {
			return false
		}
	}
	return true
}

// noStructuredOutputModelPrefixes lists model families that predate
// output_config.format. Every family from the 4.5 generation onward
// accepts it (confirmed live for Sonnet 4.6 and Opus 4.6, which some
// documentation omits). A model that unexpectedly rejects the field is
// retried without it by GenerateSegments.
var noStructuredOutputModelPrefixes = []string{
	"claude-3",
	"claude-sonnet-4-0", "claude-sonnet-4-2", // Sonnet 4 (e.g. claude-sonnet-4-20250514)
	"claude-opus-4-0", "claude-opus-4-2", // Opus 4
}

func anthropicSupportsStructuredOutput(model string) bool {
	m := strings.ToLower(model)
	for _, p := range noStructuredOutputModelPrefixes {
		if strings.HasPrefix(m, p) {
			return false
		}
	}
	return true
}

type anthropicMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
}

type anthropicContentBlock struct {
	Type         string                 `json:"type"`
	Text         string                 `json:"text,omitempty"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

type anthropicCacheControl struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

// clearCacheTTL removes the TTL from every cache breakpoint and reports
// whether any was set, so a model that rejects the field can be retried
// with default-lifetime caching.
func (r *anthropicRequest) clearCacheTTL() bool {
	cleared := false
	for _, m := range r.Messages {
		for i := range m.Content {
			if cc := m.Content[i].CacheControl; cc != nil && cc.TTL != "" {
				cc.TTL = ""
				cleared = true
			}
		}
	}
	return cleared
}

type anthropicResponse struct {
	Content    []anthropicContentBlock `json:"content"`
	StopReason string                  `json:"stop_reason"`
	Usage      anthropicUsage          `json:"usage"`
}

type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}
