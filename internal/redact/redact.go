// Package redact replaces secrets in text with [REDACTED] before sending to LLM.
package redact

import (
	"regexp"
	"strings"
)

type pattern struct {
	re        *regexp.Regexp
	markers   []string // substrings that must appear for the regex to match
	lowerCase bool     // if true, match markers against lower-cased input
}

var patterns []pattern

func init() {
	patterns = []pattern{
		// AWS access key IDs
		{
			re:      regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
			markers: []string{"AKIA"},
		},
		// AWS secret access keys (40 char base64 after common prefixes)
		{
			re:        regexp.MustCompile(`(?i)(aws_secret_access_key|aws_secret)\s*[:=]\s*[A-Za-z0-9/+=]{40}`),
			markers:   []string{"aws_secret"},
			lowerCase: true,
		},
		// Private key blocks
		{
			re:      regexp.MustCompile(`-----BEGIN [A-Z ]+PRIVATE KEY-----[\s\S]*?-----END [A-Z ]+PRIVATE KEY-----`),
			markers: []string{"-----BEGIN"},
		},
		// Bearer tokens
		{
			re:      regexp.MustCompile(`Bearer\s+[A-Za-z0-9\-._~+/]+=*`),
			markers: []string{"Bearer"},
		},
		// Generic key/secret/token/password assignments
		{
			re:        regexp.MustCompile(`(?i)(api[_-]?key|api[_-]?secret|secret[_-]?key|token|password|passwd|credentials)\s*[:=]\s*\S+`),
			markers:   []string{"api", "secret", "token", "password", "passwd", "credentials"},
			lowerCase: true,
		},
	}

	// Markers for case-insensitive patterns are compared against a lower-cased
	// copy of the input, so normalize them here rather than relying on every
	// caller to hand-lowercase their entries.
	for i := range patterns {
		if !patterns[i].lowerCase {
			continue
		}
		for j, m := range patterns[i].markers {
			patterns[i].markers[j] = strings.ToLower(m)
		}
	}
}

// Redact replaces secret patterns in text with [REDACTED].
//
// For each pattern, a cheap literal marker check runs first; the regex engine
// is only invoked when a marker is present. The case-insensitive lowered copy
// is computed lazily and reused across patterns that need it.
func Redact(text string) string {
	var lower string
	var lowered bool
	for _, p := range patterns {
		haystack := text
		if p.lowerCase {
			if !lowered {
				lower = strings.ToLower(text)
				lowered = true
			}
			haystack = lower
		}
		// An empty markers slice means "no prefilter" — always run the regex.
		if len(p.markers) > 0 && !containsAny(haystack, p.markers) {
			continue
		}
		next := p.re.ReplaceAllString(text, "[REDACTED]")
		if next != text {
			text = next
			// Redaction happened; invalidate the cached lower copy so
			// subsequent case-insensitive patterns see post-redaction text.
			lowered = false
		}
	}
	return text
}

func containsAny(s string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}
