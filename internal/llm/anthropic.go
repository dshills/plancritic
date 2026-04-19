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
	anthropicDefaultModel = "claude-sonnet-4-6"
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
	return &AnthropicProvider{apiKey: key, apiURL: anthropicAPIURL, client: &http.Client{Timeout: 5 * time.Minute}}, nil
}

func (a *AnthropicProvider) Name() string { return "anthropic" }

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
		}
		blocks = append(blocks, block)
	}
	if len(blocks) == 0 {
		return "", Usage{}, fmt.Errorf("anthropic: empty prompt")
	}

	reqBody := anthropicRequest{
		Model:       model,
		MaxTokens:   maxTokens,
		Temperature: &s.Temperature,
		Messages: []anthropicMessage{
			{Role: "user", Content: blocks},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", Usage{}, fmt.Errorf("anthropic: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.apiURL, bytes.NewReader(body))
	if err != nil {
		return "", Usage{}, fmt.Errorf("anthropic: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", a.apiKey)
	req.Header.Set("Anthropic-Version", anthropicAPIVersion)
	req.Header.Set("Anthropic-Beta", "prompt-caching-2024-07-31")

	resp, err := a.client.Do(req)
	if err != nil {
		return "", Usage{}, fmt.Errorf("anthropic: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", Usage{}, fmt.Errorf("anthropic: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", Usage{}, fmt.Errorf("anthropic: API returned %d: %s", resp.StatusCode, string(respBody))
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
		return out.String(), usage, fmt.Errorf("anthropic: response truncated (hit max_tokens=%d)", maxTokens)
	}
	if out.Len() == 0 {
		return "", usage, fmt.Errorf("anthropic: no text content in response")
	}
	return out.String(), usage, nil
}

type anthropicRequest struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	Temperature *float64           `json:"temperature,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
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
