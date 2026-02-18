// Package redact replaces secrets in text with [REDACTED] before sending to LLM.
package redact

import "regexp"

var patterns []*regexp.Regexp

func init() {
	raw := []string{
		// AWS access key IDs
		`AKIA[0-9A-Z]{16}`,
		// AWS secret access keys (40 char base64 after common prefixes)
		`(?i)(aws_secret_access_key|aws_secret)\s*[:=]\s*[A-Za-z0-9/+=]{40}`,
		// Private key blocks
		`-----BEGIN [A-Z ]+PRIVATE KEY-----[\s\S]*?-----END [A-Z ]+PRIVATE KEY-----`,
		// Bearer tokens
		`Bearer\s+[A-Za-z0-9\-._~+/]+=*`,
		// Generic key/secret/token/password assignments
		`(?i)(api[_-]?key|api[_-]?secret|secret[_-]?key|token|password|passwd|credentials)\s*[:=]\s*\S+`,
	}
	for _, r := range raw {
		patterns = append(patterns, regexp.MustCompile(r))
	}
}

// Redact replaces secret patterns in text with [REDACTED].
func Redact(text string) string {
	for _, p := range patterns {
		text = p.ReplaceAllString(text, "[REDACTED]")
	}
	return text
}
