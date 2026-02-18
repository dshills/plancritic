package internal

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dshills/plancritic/internal/llm"
	"github.com/dshills/plancritic/internal/plan"
	"github.com/dshills/plancritic/internal/profile"
	"github.com/dshills/plancritic/internal/prompt"
	"github.com/dshills/plancritic/internal/review"
	"github.com/dshills/plancritic/internal/schema"
)

// TestIntegrationLive runs a real LLM call against a small plan.
// Gated by PLANCRITIC_INTEGRATION=1 environment variable.
// Requires ANTHROPIC_API_KEY or OPENAI_API_KEY to be set.
func TestIntegrationLive(t *testing.T) {
	if os.Getenv("PLANCRITIC_INTEGRATION") != "1" {
		t.Skip("skipping integration test (set PLANCRITIC_INTEGRATION=1 to run)")
	}

	root := projectRoot()
	planPath := filepath.Join(root, "testdata", "plans", "simple.md")
	p, err := plan.Load(planPath)
	if err != nil {
		t.Fatalf("load plan: %v", err)
	}

	prof, err := profile.LoadBuiltin("general")
	if err != nil {
		t.Fatalf("load profile: %v", err)
	}

	provider, err := llm.ResolveProvider("")
	if err != nil {
		t.Fatalf("resolve provider: %v", err)
	}
	t.Logf("Using provider: %s", provider.Name())

	promptText := prompt.Build(prompt.BuildOpts{
		Plan:    p,
		Profile: prof,
	})

	settings := llm.Settings{
		Temperature: 0.2,
		MaxTokens:   4096,
	}

	result, err := provider.Generate(context.Background(), promptText, settings)
	if err != nil {
		t.Fatalf("LLM call: %v", err)
	}
	t.Logf("Response length: %d bytes", len(result))

	var rev review.Review
	if err := json.Unmarshal([]byte(result), &rev); err != nil {
		t.Fatalf("parse JSON: %v\nRaw response:\n%s", err, result[:min(len(result), 500)])
	}

	// Only validate schema, not content
	validationErrs := schema.Validate(&rev, len(p.Lines))
	for _, e := range validationErrs {
		t.Errorf("validation error: %s", e)
	}

	// Verify non-empty output
	if len(rev.Issues) == 0 && len(rev.Questions) == 0 {
		t.Error("expected at least one issue or question from LLM")
	}

	t.Logf("Verdict: %s, Score: %d, Issues: %d, Questions: %d",
		rev.Summary.Verdict, rev.Summary.Score, len(rev.Issues), len(rev.Questions))
}
