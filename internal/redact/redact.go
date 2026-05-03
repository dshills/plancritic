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
	repl      string   // replacement string; "" defaults to "[REDACTED]"
}

var patterns []pattern

func init() {
	patterns = []pattern{
		// AWS access key IDs
		{
			re:      regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
			markers: []string{"AKIA"},
		},
		// AWS secret access keys (40 char base64 after common prefixes).
		// Capture the separator so the original format is preserved.
		{
			re:        regexp.MustCompile(`(?i)(aws_secret_access_key|aws_secret)(\s*[:=]\s*)[A-Za-z0-9/+=]{40}`),
			markers:   []string{"aws_secret"},
			lowerCase: true,
			repl:      "${1}${2}[REDACTED]",
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
		// Generic key/secret/token/password assignments (YAML / .env / plain text).
		// Includes aws_secret* so coverage is consistent with the JSON pattern below.
		// Word boundaries on the key prevent matching compound names like "mypassword".
		// Captures the separator so the original format is preserved in output.
		// The value arm handles double-quoted, single-quoted, and bare values so
		// multi-word quoted secrets (e.g. password: "my secret") are fully redacted.
		{
			re:        regexp.MustCompile(`(?i)\b(api[_-]?key|api[_-]?secret|secret[_-]?key|token|password|passwd|credentials|aws_secret_access_key|aws_secret)\b(\s*[:=]\s*)(?:"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|\S+)`),
			markers:   []string{"api", "secret", "token", "password", "passwd", "credentials"},
			lowerCase: true,
			repl:      "${1}${2}[REDACTED]",
		},
		// JSON-format secrets: "key": "value" — the [:=] pattern above misses
		// quoted JSON keys/values because of surrounding double-quote chars.
		// Word boundaries on the key prevent false positives on compound names.
		// Captures the separator whitespace to preserve original formatting.
		// The value pattern handles quoted strings with escape sequences and
		// scalar non-string types (number, boolean, null) without consuming
		// JSON structural delimiters like commas or closing braces.
		{
			re:        regexp.MustCompile(`(?i)"\b(api[_-]?key|api[_-]?secret|secret[_-]?key|token|password|passwd|credentials|aws_secret_access_key|aws_secret)\b"(\s*:\s*)(?:"(?:[^"\\]|\\.)*"|true|false|null|[0-9.eE+\-]+)`),
			markers:   []string{"api", "secret", "token", "password", "passwd", "credentials"},
			lowerCase: true,
			repl:      `"${1}"${2}"[REDACTED]"`,
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
		repl := "[REDACTED]"
		if p.repl != "" {
			repl = p.repl
		}
		next := p.re.ReplaceAllString(text, repl)
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
