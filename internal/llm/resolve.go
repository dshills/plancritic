package llm

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// ResolveProvider selects an LLM provider based on the provider flag, model flag,
// and available API keys (in that priority order).
func ResolveProvider(providerFlag, modelFlag string) (Provider, error) {
	// Explicit --provider flag takes highest priority
	if providerFlag != "" {
		model := stripProviderPrefix(modelFlag)
		switch strings.ToLower(providerFlag) {
		case "anthropic":
			p, err := NewAnthropic()
			if err != nil {
				return nil, err
			}
			if model != "" {
				return &modelOverride{Provider: p, model: model}, nil
			}
			return p, nil
		case "openai":
			p, err := NewOpenAI()
			if err != nil {
				return nil, err
			}
			if model != "" {
				return &modelOverride{Provider: p, model: model}, nil
			}
			return p, nil
		case "gemini", "google":
			p, err := NewGemini()
			if err != nil {
				return nil, err
			}
			if model != "" {
				return &modelOverride{Provider: p, model: model}, nil
			}
			return p, nil
		default:
			return nil, fmt.Errorf("unknown provider: %q (valid: anthropic, openai, gemini)", providerFlag)
		}
	}

	// Infer provider from model flag prefix
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

		case strings.HasPrefix(lower, "gemini:"):
			p, err := NewGemini()
			if err != nil {
				return nil, err
			}
			return &modelOverride{Provider: p, model: strings.TrimPrefix(modelFlag, "gemini:")}, nil

		case strings.HasPrefix(lower, "gemini"):
			p, err := NewGemini()
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
	if os.Getenv("GEMINI_API_KEY") != "" {
		return NewGemini()
	}

	return nil, fmt.Errorf("no LLM provider configured: set ANTHROPIC_API_KEY, OPENAI_API_KEY, or GEMINI_API_KEY, or use --provider")
}

// Unwrap returns the underlying provider if p is a wrapper (e.g. from
// a --provider/--model override), otherwise p itself. Callers use this
// when they need to type-assert for provider-specific capabilities such
// as CachingProvider.
func Unwrap(p Provider) Provider {
	if m, ok := p.(*modelOverride); ok {
		return m.Provider
	}
	return p
}

// OverrideModel returns the model set on p if p is a wrapper, or the
// empty string otherwise. Use after Unwrap when the caller needs to
// know the effective model for cache keying.
func OverrideModel(p Provider) string {
	if m, ok := p.(*modelOverride); ok {
		return m.model
	}
	return ""
}

// modelOverride wraps a provider to override the model in settings.
type modelOverride struct {
	Provider
	model string
}

func (m *modelOverride) Generate(ctx context.Context, prompt string, s Settings) (string, Usage, error) {
	s.Model = m.model
	return m.Provider.Generate(ctx, prompt, s)
}

// GenerateSegments forwards to the wrapped provider when it supports
// segmented prompts. Otherwise it concatenates segments into a single
// prompt string and calls Generate.
func (m *modelOverride) GenerateSegments(ctx context.Context, segments []Segment, s Settings) (string, Usage, error) {
	s.Model = m.model
	if sp, ok := m.Provider.(SegmentedProvider); ok {
		return sp.GenerateSegments(ctx, segments, s)
	}
	return m.Provider.Generate(ctx, ConcatSegments(segments), s)
}

// stripProviderPrefix removes a leading "provider:" prefix from a model name.
func stripProviderPrefix(model string) string {
	for _, prefix := range []string{"anthropic:", "openai:", "gemini:"} {
		if strings.HasPrefix(strings.ToLower(model), prefix) {
			return model[len(prefix):]
		}
	}
	return model
}
