package redact

import (
	"strings"
	"testing"
)

func TestRedactAWSKey(t *testing.T) {
	input := "key is AKIAIOSFODNN7EXAMPLE and more text"
	got := Redact(input)
	if strings.Contains(got, "AKIAIOSFODNN7EXAMPLE") {
		t.Error("AWS key should be redacted")
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Error("expected [REDACTED] replacement")
	}
}

func TestRedactBearerToken(t *testing.T) {
	input := "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abc123"
	got := Redact(input)
	if strings.Contains(got, "eyJhbGciOiJIUzI1NiJ9") {
		t.Error("Bearer token should be redacted")
	}
}

func TestRedactPrivateKey(t *testing.T) {
	input := "-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEA0Z3VS5JJcds3xfn/ygWyF8PbnGH\n-----END RSA PRIVATE KEY-----"
	got := Redact(input)
	if strings.Contains(got, "MIIEpAIBAAKCAQEA0Z3VS5JJcds3xfn") {
		t.Error("private key should be redacted")
	}
}

func TestRedactGenericSecrets(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"api_key", "api_key=sk-1234567890abcdef"},
		{"token", "token: ghp_abcdef1234567890"},
		{"password", "password=hunter2"},
		{"api-secret", "api-secret: mysecretvalue"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Redact(tt.input)
			if !strings.Contains(got, "[REDACTED]") {
				t.Errorf("expected redaction for %q, got: %s", tt.name, got)
			}
		})
	}
}

func TestRedactPreservesNonSecrets(t *testing.T) {
	input := "This is a normal plan step with no secrets."
	got := Redact(input)
	if got != input {
		t.Errorf("non-secret text was modified: %s", got)
	}
}

func TestRedactJSONContextSecrets(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"json_api_key", `{"api_key": "sk-1234567890abcdef"}`},
		{"json_api_secret", `{"api_secret": "supersecretvalue"}`},
		{"json_token", `{"token": "ghp_abcdef1234567890"}`},
		{"json_password", `{"password": "hunter2"}`},
		{"json_aws_secret", `{"aws_secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}`},
		{"json_credentials", `{"credentials": "my-secret-creds"}`},
		{"json_secret_key", `{"secret_key": "topsecret"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Redact(tt.input)
			if !strings.Contains(got, "[REDACTED]") {
				t.Errorf("expected redaction for JSON %q, got: %s", tt.name, got)
			}
		})
	}
}

func TestRedactJSONEscapedQuote(t *testing.T) {
	input := `{"password": "p@ss\"word"}`
	got := Redact(input)
	if strings.Contains(got, "p@ss") {
		t.Error("escaped-quote JSON secret should be fully redacted")
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Error("expected [REDACTED] in output")
	}
}

func TestRedactJSONCompactDoesNotOverredact(t *testing.T) {
	// Compact JSON without spaces: structural delimiters must not be consumed.
	input := `{"api_key":"secret123","other":"safe"}`
	got := Redact(input)
	if strings.Contains(got, "secret123") {
		t.Error("compact JSON secret value should be redacted")
	}
	if !strings.Contains(got, `"other":"safe"`) {
		t.Errorf("unrelated compact JSON field should be preserved, got: %s", got)
	}
}

func TestRedactJSONPreservesKey(t *testing.T) {
	input := `{"api_key": "sk-supersecret123", "other": "safe"}`
	got := Redact(input)
	if strings.Contains(got, "sk-supersecret123") {
		t.Error("JSON secret value should be redacted")
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Error("expected [REDACTED] in output")
	}
	if !strings.Contains(got, `"other": "safe"`) {
		t.Error("unrelated JSON field should be preserved")
	}
	// Key name must be preserved so redacted output remains diagnostic.
	if !strings.Contains(got, "api_key") {
		t.Error("key name should be preserved in redacted output")
	}
}
