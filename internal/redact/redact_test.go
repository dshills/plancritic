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
