// Package llm defines the provider interface and implementations for LLM interaction.
package llm

import (
	"context"
	"strings"
)

// Settings configures the LLM request.
type Settings struct {
	Model       string
	Temperature float64
	MaxTokens   int
	Seed        *int
}

// Provider generates text from a prompt using an LLM.
type Provider interface {
	Generate(ctx context.Context, prompt string, settings Settings) (string, error)
	Name() string
}

// ExtractJSON strips markdown code fences from LLM responses that wrap JSON.
func ExtractJSON(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		// Remove opening fence (```json or ```)
		if idx := strings.Index(s, "\n"); idx != -1 {
			s = s[idx+1:]
		}
		// Remove closing fence
		if idx := strings.LastIndex(s, "```"); idx != -1 {
			s = s[:idx]
		}
		s = strings.TrimSpace(s)
	}
	return s
}
