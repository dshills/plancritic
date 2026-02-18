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
	openaiAPIURL       = "https://api.openai.com/v1/chat/completions"
	openaiDefaultModel = "gpt-4o"
)

// OpenAIProvider implements Provider using the OpenAI Chat Completions API.
type OpenAIProvider struct {
	apiKey string
	apiURL string
	client *http.Client
}

// NewOpenAI creates an OpenAI provider using the OPENAI_API_KEY env var.
func NewOpenAI() (*OpenAIProvider, error) {
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY environment variable not set")
	}
	return &OpenAIProvider{apiKey: key, apiURL: openaiAPIURL, client: &http.Client{}}, nil
}

func (o *OpenAIProvider) Name() string { return "openai" }

func (o *OpenAIProvider) Generate(ctx context.Context, prompt string, s Settings) (string, error) {
	model := s.Model
	if model == "" {
		model = openaiDefaultModel
	}

	maxTokens := s.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	reqBody := openaiRequest{
		Model:       model,
		MaxTokens:   maxTokens,
		Temperature: s.Temperature,
		Messages: []openaiMessage{
			{Role: "user", Content: prompt},
		},
		ResponseFormat: &openaiResponseFormat{Type: "json_object"},
	}
	if s.Seed != nil {
		reqBody.Seed = s.Seed
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("openai: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.apiURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("openai: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)

	resp, err := o.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("openai: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("openai: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openai: API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result openaiResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("openai: parse response: %w", err)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("openai: no choices in response")
	}

	return result.Choices[0].Message.Content, nil
}

type openaiRequest struct {
	Model          string               `json:"model"`
	MaxTokens      int                  `json:"max_tokens"`
	Temperature    float64              `json:"temperature"`
	Seed           *int                 `json:"seed,omitempty"`
	Messages       []openaiMessage      `json:"messages"`
	ResponseFormat *openaiResponseFormat `json:"response_format,omitempty"`
}

type openaiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openaiResponseFormat struct {
	Type string `json:"type"`
}

type openaiResponse struct {
	Choices []openaiChoice `json:"choices"`
}

type openaiChoice struct {
	Message openaiMessage `json:"message"`
}
