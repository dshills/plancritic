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
	openaiAPIURL       = "https://api.openai.com/v1/chat/completions"
	openaiDefaultModel = "gpt-5.2"
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
	return &OpenAIProvider{apiKey: key, apiURL: openaiAPIURL, client: &http.Client{Timeout: 5 * time.Minute}}, nil
}

func (o *OpenAIProvider) Name() string { return "openai" }

// DefaultModel implements ModelDefaulter.
func (o *OpenAIProvider) DefaultModel() string { return openaiDefaultModel }

func (o *OpenAIProvider) Generate(ctx context.Context, prompt string, s Settings) (string, Usage, error) {
	model := s.Model
	if model == "" {
		model = openaiDefaultModel
	}

	maxTokens := s.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 16384
	}

	reqBody := openaiRequest{
		Model:               model,
		MaxCompletionTokens: maxTokens,
		Temperature:         s.Temperature,
		Messages: []openaiMessage{
			{Role: "user", Content: prompt},
		},
		ResponseFormat: &openaiResponseFormat{Type: "json_object"},
	}
	if len(s.OutputSchema) > 0 && openaiSupportsStructuredOutput(model) {
		reqBody.ResponseFormat = &openaiResponseFormat{
			Type:       "json_schema",
			JSONSchema: &openaiJSONSchema{Name: "plancritic_review", Strict: true, Schema: s.OutputSchema},
		}
	}
	if s.Seed != nil {
		reqBody.Seed = s.Seed
	}

	var respBody []byte
	for attempt := 0; ; attempt++ {
		status, data, err := o.post(ctx, reqBody)
		if err != nil {
			return "", Usage{}, err
		}
		if status == http.StatusOK {
			respBody = data
			break
		}
		// A model that rejects json_schema is retried once in plain JSON
		// mode so a stale capability list cannot fail the run.
		if status == http.StatusBadRequest && attempt == 0 &&
			reqBody.ResponseFormat != nil && reqBody.ResponseFormat.Type == "json_schema" {
			msg := strings.ToLower(string(data))
			if strings.Contains(msg, "response_format") || strings.Contains(msg, "json_schema") {
				reqBody.ResponseFormat = &openaiResponseFormat{Type: "json_object"}
				continue
			}
		}
		return "", Usage{}, fmt.Errorf("openai: API returned %d: %s", status, string(data))
	}

	var result openaiResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", Usage{}, fmt.Errorf("openai: parse response: %w", err)
	}

	usage := Usage{
		InputTokens:  result.Usage.PromptTokens,
		OutputTokens: result.Usage.CompletionTokens,
	}

	if len(result.Choices) == 0 {
		return "", usage, fmt.Errorf("openai: no choices in response")
	}

	choice := result.Choices[0]
	if choice.FinishReason == "length" {
		return choice.Message.Content, usage, &TruncatedError{Provider: "openai", MaxTokens: maxTokens, Partial: choice.Message.Content}
	}

	return choice.Message.Content, usage, nil
}

// post sends one Chat Completions request and returns the HTTP status
// and raw body; a non-200 status is not an error so callers can inspect it.
func (o *OpenAIProvider) post(ctx context.Context, reqBody openaiRequest) (int, []byte, error) {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return 0, nil, fmt.Errorf("openai: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.apiURL, bytes.NewReader(body))
	if err != nil {
		return 0, nil, fmt.Errorf("openai: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)

	resp, err := o.client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("openai: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("openai: read response: %w", err)
	}
	return resp.StatusCode, data, nil
}

// openaiSupportsStructuredOutput reports whether model accepts
// response_format json_schema. The JSON-mode-only generation (gpt-3.5,
// gpt-4, gpt-4-turbo) returns 400 for it and keeps json_object instead;
// gpt-4o, gpt-4.1, gpt-4.5, the gpt-5 family, and the o-series support it.
func openaiSupportsStructuredOutput(model string) bool {
	m := strings.ToLower(model)
	switch {
	case strings.HasPrefix(m, "gpt-3"):
		return false
	case strings.HasPrefix(m, "gpt-4") && !strings.HasPrefix(m, "gpt-4o") && !strings.HasPrefix(m, "gpt-4."):
		return false
	case m == "gpt-4o-2024-05-13", m == "chatgpt-4o-latest",
		strings.HasPrefix(m, "o1-mini"), strings.HasPrefix(m, "o1-preview"):
		// Snapshots that predate structured outputs, and the o1 previews.
		return false
	}
	return true
}

type openaiRequest struct {
	Model               string                `json:"model"`
	MaxCompletionTokens int                   `json:"max_completion_tokens"`
	Temperature         float64               `json:"temperature"`
	Seed                *int                  `json:"seed,omitempty"`
	Messages            []openaiMessage       `json:"messages"`
	ResponseFormat      *openaiResponseFormat `json:"response_format,omitempty"`
}

type openaiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openaiResponseFormat struct {
	Type       string            `json:"type"`
	JSONSchema *openaiJSONSchema `json:"json_schema,omitempty"`
}

type openaiJSONSchema struct {
	Name   string          `json:"name"`
	Strict bool            `json:"strict"`
	Schema json.RawMessage `json:"schema"`
}

type openaiResponse struct {
	Choices []openaiChoice `json:"choices"`
	Usage   openaiUsage    `json:"usage"`
}

type openaiChoice struct {
	Message      openaiMessage `json:"message"`
	FinishReason string        `json:"finish_reason"`
}

type openaiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}
