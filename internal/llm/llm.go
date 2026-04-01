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
// It handles cases where the LLM adds prose before or after a code fence block.
func ExtractJSON(s string) string {
	s = strings.TrimSpace(s)

	// If the response starts with a fence, strip it directly.
	if strings.HasPrefix(s, "```") {
		return stripFence(s)
	}

	// Otherwise look for a code fence block anywhere in the response
	// (e.g., "Here is the JSON:\n```json\n{...}\n```").
	if idx := strings.Index(s, "```"); idx != -1 {
		return stripFence(s[idx:])
	}

	return s
}

func stripFence(s string) string {
	// Remove opening fence line (```json or ```)
	if idx := strings.Index(s, "\n"); idx != -1 {
		s = s[idx+1:]
	}
	// Remove closing fence
	if idx := strings.LastIndex(s, "```"); idx != -1 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}

// SanitizeJSON fixes common LLM JSON issues such as invalid escape sequences
// (e.g., \s, \d, \w from regex patterns) by double-escaping the backslash.
// It correctly preserves already-escaped sequences like \\s.
func SanitizeJSON(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			i++
			continue
		}
		// We have a backslash at position i
		if i+1 >= len(s) {
			b.WriteByte(s[i])
			i++
			continue
		}
		next := s[i+1]
		switch next {
		case '"', '\\', '/', 'b', 'f', 'n', 'r', 't', 'u':
			// Valid JSON escape — pass through as-is
			b.WriteByte(s[i])
			b.WriteByte(next)
			i += 2
		default:
			// Invalid escape like \s, \d, \w — double the backslash
			b.WriteByte('\\')
			b.WriteByte('\\')
			b.WriteByte(next)
			i += 2
		}
	}
	return b.String()
}
