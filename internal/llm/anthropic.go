package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
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
	return &AnthropicProvider{apiKey: key, apiURL: anthropicAPIURL, client: &http.Client{}}, nil
}

func (a *AnthropicProvider) Name() string { return "anthropic" }

func (a *AnthropicProvider) Generate(ctx context.Context, prompt string, s Settings) (string, error) {
	model := s.Model
	if model == "" {
		model = anthropicDefaultModel
	}

	maxTokens := s.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 16384
	}

	reqBody := anthropicRequest{
		Model:       model,
		MaxTokens:   maxTokens,
		Temperature: &s.Temperature,
		Messages: []anthropicMessage{
			{Role: "user", Content: prompt},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("anthropic: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.apiURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("anthropic: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", a.apiKey)
	req.Header.Set("Anthropic-Version", anthropicAPIVersion)

	resp, err := a.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("anthropic: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("anthropic: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("anthropic: API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result anthropicResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("anthropic: parse response: %w", err)
	}

	for _, block := range result.Content {
		if block.Type == "text" {
			return block.Text, nil
		}
	}

	return "", fmt.Errorf("anthropic: no text content in response")
}

type anthropicRequest struct {
	Model       string              `json:"model"`
	MaxTokens   int                 `json:"max_tokens"`
	Temperature *float64            `json:"temperature,omitempty"`
	Messages    []anthropicMessage  `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	Content []anthropicContentBlock `json:"content"`
}

type anthropicContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
