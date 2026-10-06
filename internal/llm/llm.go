// Package llm defines the provider interface and implementations for LLM interaction.
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Settings configures the LLM request.
type Settings struct {
	Model       string
	Temperature float64
	MaxTokens   int
	Seed        *int
	// CachedContentName, when set, asks the provider to reference an
	// existing provider-side context cache by resource name (e.g.
	// "cachedContents/abc123" for Gemini). Only honored by providers
	// that implement CachingProvider.
	CachedContentName string
	// OutputSchema, when non-empty, is a JSON Schema the response must
	// satisfy. Providers that support native structured output pass it
	// through (Anthropic output_config, OpenAI json_schema, Gemini
	// responseJsonSchema); others ignore it and rely on the prompt.
	OutputSchema json.RawMessage
	// OnRetry, when set, is called before each retry of a transient
	// provider failure so callers can log it.
	OnRetry RetryNotify
}

// Usage reports token counts for a single request. Cache-related fields
// will be zero for providers that do not support prompt caching.
type Usage struct {
	InputTokens              int
	OutputTokens             int
	CacheCreationInputTokens int
	CacheReadInputTokens     int
}

// Provider generates text from a prompt using an LLM. Usage reports
// token counts for the returned response and is tied to that specific
// call (no shared state on the provider).
type Provider interface {
	Generate(ctx context.Context, prompt string, settings Settings) (string, Usage, error)
	Name() string
}

// ModelDefaulter is an optional interface for providers that pick a
// default model when Settings.Model is empty. Callers that need a stable
// identity for the effective model (e.g. result-cache keys) use it so
// two providers of the same name but different defaults never collide.
type ModelDefaulter interface {
	DefaultModel() string
}

// EffectiveModel returns the model a request through p will actually
// use: an explicit override wrapper, else requested, else the provider's
// own default when it exposes one, else "".
func EffectiveModel(p Provider, requested string) string {
	if o := OverrideModel(p); o != "" {
		return o
	}
	if requested != "" {
		return requested
	}
	if d, ok := Unwrap(p).(ModelDefaulter); ok {
		return d.DefaultModel()
	}
	return ""
}

// Segment is a piece of prompt text that may optionally mark a cache
// breakpoint for providers that support prompt caching (e.g. Anthropic).
// Providers that don't support caching concatenate all segments into a
// single prompt string.
type Segment struct {
	Text string
	// CacheMark, when true, requests that the provider place a cache
	// checkpoint at the end of this segment. The provider is free to
	// ignore the mark if the cumulative prefix is too small to cache.
	CacheMark bool
}

// SegmentedProvider is an optional extension interface implemented by
// providers that can take advantage of segmented prompts for caching.
// Callers should type-assert and fall back to Generate when a provider
// does not implement this interface.
type SegmentedProvider interface {
	Provider
	GenerateSegments(ctx context.Context, segments []Segment, settings Settings) (string, Usage, error)
}

// CacheHandle identifies a provider-side context cache.
type CacheHandle struct {
	Name      string
	ExpiresAt time.Time
}

// CachingProvider is an optional interface implemented by providers
// that expose a persistent server-side context cache (e.g. Gemini
// Context Caching). Callers type-assert and fall back to uncached
// generation when unsupported.
type CachingProvider interface {
	SegmentedProvider
	// CreateCache uploads the given segments as a cache resource,
	// returning an opaque handle that can be passed back via
	// Settings.CachedContentName on subsequent Generate calls. Providers
	// may reject inputs below a minimum token size.
	CreateCache(ctx context.Context, segments []Segment, model string, ttl time.Duration) (CacheHandle, error)
}

// ConcatSegments joins segments into a single prompt string.
func ConcatSegments(segs []Segment) string {
	total := 0
	for _, s := range segs {
		total += len(s.Text)
	}
	var b strings.Builder
	b.Grow(total)
	for _, s := range segs {
		b.WriteString(s.Text)
	}
	return b.String()
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

// TruncatedError reports that the model stopped because it hit the
// output token cap. Partial holds whatever text was produced so the
// caller can try to salvage it (see SalvageJSON) instead of discarding
// the whole, already-billed response.
type TruncatedError struct {
	Provider  string
	MaxTokens int
	Partial   string
}

func (e *TruncatedError) Error() string {
	return fmt.Sprintf("%s: response truncated (hit max_tokens=%d)", e.Provider, e.MaxTokens)
}

// SalvageJSON recovers a parseable document from JSON that was cut off
// mid-stream. It keeps the longest prefix that ends where an object or
// array closes as an element of a top-level array (a whole issue or
// question) or as a top-level member, then closes every container
// still open. Boundaries after scalar values are deliberately not
// tracked: in plancritic's output shape every top-level member and
// every element of a top-level array is an object or an array, so a
// scalar boundary could never be the best cut. Already-valid input is
// returned unchanged. The second result is false when no usable prefix
// exists (for example, the cut fell inside the first element).
func SalvageJSON(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if json.Valid([]byte(s)) {
		return s, true
	}

	type cut struct {
		pos   int
		stack string
	}
	var cuts []cut
	var stack []byte
	inStr, esc := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{', '[':
			stack = append(stack, c)
		case '}', ']':
			if len(stack) == 0 {
				return "", false
			}
			stack = stack[:len(stack)-1]
			// A boundary worth cutting at: an element of a top-level
			// array just closed (stack is "{[" for an array member of a
			// root object, "[" for a root array) or a top-level member
			// value just closed (stack is "{").
			if st := string(stack); st == "{[" || st == "{" || st == "[" {
				cuts = append(cuts, cut{pos: i + 1, stack: st})
			}
		}
	}

	for n := len(cuts) - 1; n >= 0; n-- {
		var b strings.Builder
		b.WriteString(s[:cuts[n].pos])
		for j := len(cuts[n].stack) - 1; j >= 0; j-- {
			if cuts[n].stack[j] == '{' {
				b.WriteByte('}')
			} else {
				b.WriteByte(']')
			}
		}
		if candidate := b.String(); json.Valid([]byte(candidate)) {
			return candidate, true
		}
	}
	return "", false
}
