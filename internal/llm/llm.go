// Package llm defines the provider interface and implementations for LLM interaction.
package llm

import "context"

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
