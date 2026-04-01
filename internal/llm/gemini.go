package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const (
	geminiAPIURL       = "https://generativelanguage.googleapis.com/v1beta/models"
	geminiDefaultModel = "gemini-2.5-flash"
)

// GeminiProvider implements Provider using the Google Gemini API.
type GeminiProvider struct {
	apiKey string
	apiURL string
	client *http.Client
}

// NewGemini creates a Gemini provider using the GEMINI_API_KEY env var.
func NewGemini() (*GeminiProvider, error) {
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY environment variable not set")
	}
	return &GeminiProvider{apiKey: key, apiURL: geminiAPIURL, client: &http.Client{Timeout: 5 * time.Minute}}, nil
}

func (g *GeminiProvider) Name() string { return "gemini" }

func (g *GeminiProvider) Generate(ctx context.Context, prompt string, s Settings) (string, error) {
	model := s.Model
	if model == "" {
		model = geminiDefaultModel
	}

	maxTokens := s.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 16384
	}

	reqBody := geminiRequest{
		Contents: []geminiContent{
			{
				Parts: []geminiPart{
					{Text: prompt},
				},
			},
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

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("gemini: marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/%s:generateContent?key=%s", g.apiURL, model, g.apiKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("gemini: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("gemini: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("gemini: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gemini: API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result geminiResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("gemini: parse response: %w", err)
	}

	if len(result.Candidates) == 0 {
		return "", fmt.Errorf("gemini: no candidates in response")
	}

	candidate := result.Candidates[0]
	if candidate.FinishReason == "MAX_TOKENS" {
		return "", fmt.Errorf("gemini: response truncated (hit maxOutputTokens=%d)", maxTokens)
	}

	for _, part := range candidate.Content.Parts {
		if part.Text != "" {
			return part.Text, nil
		}
	}

	return "", fmt.Errorf("gemini: no text content in response")
}

type geminiRequest struct {
	Contents         []geminiContent        `json:"contents"`
	GenerationConfig geminiGenerationConfig `json:"generationConfig"`
}

type geminiContent struct {
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
	Candidates []geminiCandidate `json:"candidates"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
}
