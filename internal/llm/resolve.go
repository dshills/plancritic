package llm

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// ResolveProvider selects an LLM provider based on the model flag and available API keys.
func ResolveProvider(modelFlag string) (Provider, error) {
	// Explicit provider from model flag
	if modelFlag != "" {
		lower := strings.ToLower(modelFlag)
		switch {
		case strings.HasPrefix(lower, "anthropic:"):
			p, err := NewAnthropic()
			if err != nil {
				return nil, err
			}
			return &modelOverride{Provider: p, model: strings.TrimPrefix(modelFlag, "anthropic:")}, nil

		case strings.HasPrefix(lower, "claude"):
			p, err := NewAnthropic()
			if err != nil {
				return nil, err
			}
			return &modelOverride{Provider: p, model: modelFlag}, nil

		case strings.HasPrefix(lower, "openai:"):
			p, err := NewOpenAI()
			if err != nil {
				return nil, err
			}
			return &modelOverride{Provider: p, model: strings.TrimPrefix(modelFlag, "openai:")}, nil

		case strings.HasPrefix(lower, "gpt"):
			p, err := NewOpenAI()
			if err != nil {
				return nil, err
			}
			return &modelOverride{Provider: p, model: modelFlag}, nil
		}
	}

	// Auto-detect from environment
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		return NewAnthropic()
	}
	if os.Getenv("OPENAI_API_KEY") != "" {
		return NewOpenAI()
	}

	return nil, fmt.Errorf("no LLM provider configured: set ANTHROPIC_API_KEY or OPENAI_API_KEY")
}

// modelOverride wraps a provider to override the model in settings.
type modelOverride struct {
	Provider
	model string
}

func (m *modelOverride) Generate(ctx context.Context, prompt string, s Settings) (string, error) {
	s.Model = m.model
	return m.Provider.Generate(ctx, prompt, s)
}
