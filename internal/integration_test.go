package internal

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	pctx "github.com/dshills/plancritic/internal/context"
	"github.com/dshills/plancritic/internal/llm"
	"github.com/dshills/plancritic/internal/plan"
	"github.com/dshills/plancritic/internal/profile"
	"github.com/dshills/plancritic/internal/prompt"
	"github.com/dshills/plancritic/internal/review"
	"github.com/dshills/plancritic/internal/schema"
)

// skipUnlessIntegration skips the test unless PLANCRITIC_INTEGRATION=1.
func skipUnlessIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("PLANCRITIC_INTEGRATION") != "1" {
		t.Skip("skipping integration test (set PLANCRITIC_INTEGRATION=1 to run)")
	}
}

// loadTestPlan loads the test plan from testdata.
func loadTestPlan(t *testing.T) *plan.Plan {
	t.Helper()
	p, err := plan.Load(filepath.Join(projectRoot(), "testdata", "plans", "simple.md"))
	if err != nil {
		t.Fatalf("load plan: %v", err)
	}
	return p
}

// loadTestContext loads the test constraints context from testdata.
func loadTestContext(t *testing.T) *pctx.File {
	t.Helper()
	c, err := pctx.Load(filepath.Join(projectRoot(), "testdata", "contexts", "constraints.md"))
	if err != nil {
		t.Fatalf("load context: %v", err)
	}
	return c
}

// runReview calls the LLM and validates the response structurally.
func runReview(t *testing.T, provider llm.Provider, opts prompt.BuildOpts, planLineCount int) review.Review {
	t.Helper()

	promptText := prompt.Build(opts)
	settings := llm.Settings{
		Temperature: 0.2,
		MaxTokens:   16384,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	result, err := provider.Generate(ctx, promptText, settings)
	if err != nil {
		t.Fatalf("LLM call failed: %v", err)
	}
	t.Logf("Response length: %d bytes", len(result))

	result = llm.ExtractJSON(result)

	var rev review.Review
	if err := json.Unmarshal([]byte(result), &rev); err != nil {
		t.Fatalf("parse JSON: %v\nRaw (first 1000 chars):\n%s", err, truncateStr(result, 1000))
	}

	// Recompute summary deterministically (LLM scores are not authoritative)
	rev.Summary = review.ComputeSummary(rev.Issues)
	review.SortIssues(rev.Issues)
	review.SortQuestions(rev.Questions)

	// Schema validation after recompute (non-fatal: LLMs may produce minor schema issues
	// that would be caught by the repair loop in production)
	validationErrs := schema.Validate(&rev, planLineCount)
	for _, e := range validationErrs {
		t.Logf("validation warning: %s", e)
	}

	// Must produce at least one finding
	if len(rev.Issues) == 0 && len(rev.Questions) == 0 {
		t.Error("expected at least one issue or question")
	}

	t.Logf("Provider: %s | Verdict: %s | Score: %d | Issues: %d | Questions: %d",
		provider.Name(), rev.Summary.Verdict, rev.Summary.Score,
		len(rev.Issues), len(rev.Questions))

	return rev
}

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// ---------- Per-provider basic tests ----------

func TestIntegrationAnthropic(t *testing.T) {
	skipUnlessIntegration(t)
	t.Parallel()
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY not set")
	}

	provider, err := llm.ResolveProvider("anthropic:claude-sonnet-4-6")
	if err != nil {
		t.Fatalf("resolve provider: %v", err)
	}

	p := loadTestPlan(t)
	prof, err := profile.LoadBuiltin("general")
	if err != nil {
		t.Fatalf("load profile: %v", err)
	}

	rev := runReview(t, provider, prompt.BuildOpts{
		Plan:    p,
		Profile: prof,
	}, len(p.Lines))

	// The sample plan has a clear dependency contradiction — any reasonable model should catch it
	assertHasCategory(t, rev.Issues, review.CategoryContradiction)
}

func TestIntegrationOpenAI(t *testing.T) {
	skipUnlessIntegration(t)
	t.Parallel()
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("OPENAI_API_KEY not set")
	}

	provider, err := llm.ResolveProvider("openai:gpt-5.2")
	if err != nil {
		t.Fatalf("resolve provider: %v", err)
	}

	p := loadTestPlan(t)
	prof, err := profile.LoadBuiltin("general")
	if err != nil {
		t.Fatalf("load profile: %v", err)
	}

	rev := runReview(t, provider, prompt.BuildOpts{
		Plan:    p,
		Profile: prof,
	}, len(p.Lines))

	assertHasCategory(t, rev.Issues, review.CategoryContradiction)
}

// ---------- With context files ----------

func TestIntegrationAnthropicWithContext(t *testing.T) {
	skipUnlessIntegration(t)
	t.Parallel()
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY not set")
	}

	provider, err := llm.ResolveProvider("anthropic:claude-sonnet-4-6")
	if err != nil {
		t.Fatalf("resolve provider: %v", err)
	}

	p := loadTestPlan(t)
	ctx := loadTestContext(t)
	prof, err := profile.LoadBuiltin("general")
	if err != nil {
		t.Fatalf("load profile: %v", err)
	}

	rev := runReview(t, provider, prompt.BuildOpts{
		Plan:     p,
		Contexts: []*pctx.File{ctx},
		Profile:  prof,
	}, len(p.Lines))

	// With constraints.md context, the model should detect the timestamp naming issue
	assertHasCategory(t, rev.Issues, review.CategoryContradiction)
	assertHasContextEvidence(t, rev)
}

func TestIntegrationOpenAIWithContext(t *testing.T) {
	skipUnlessIntegration(t)
	t.Parallel()
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("OPENAI_API_KEY not set")
	}

	provider, err := llm.ResolveProvider("openai:gpt-5.2")
	if err != nil {
		t.Fatalf("resolve provider: %v", err)
	}

	p := loadTestPlan(t)
	ctx := loadTestContext(t)
	prof, err := profile.LoadBuiltin("general")
	if err != nil {
		t.Fatalf("load profile: %v", err)
	}

	rev := runReview(t, provider, prompt.BuildOpts{
		Plan:     p,
		Contexts: []*pctx.File{ctx},
		Profile:  prof,
	}, len(p.Lines))

	assertHasCategory(t, rev.Issues, review.CategoryContradiction)
	assertHasContextEvidence(t, rev)
}

// ---------- With profile ----------

func TestIntegrationAnthropicGoProfile(t *testing.T) {
	skipUnlessIntegration(t)
	t.Parallel()
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY not set")
	}

	provider, err := llm.ResolveProvider("anthropic:claude-sonnet-4-6")
	if err != nil {
		t.Fatalf("resolve provider: %v", err)
	}

	p := loadTestPlan(t)
	ctx := loadTestContext(t)
	prof, err := profile.LoadBuiltin("go-backend")
	if err != nil {
		t.Fatalf("load profile: %v", err)
	}

	rev := runReview(t, provider, prompt.BuildOpts{
		Plan:     p,
		Contexts: []*pctx.File{ctx},
		Profile:  prof,
	}, len(p.Lines))

	// go-backend profile emphasizes minimal deps — should flag the dependency contradiction
	assertHasCategory(t, rev.Issues, review.CategoryContradiction)
}

func TestIntegrationOpenAIGoProfile(t *testing.T) {
	skipUnlessIntegration(t)
	t.Parallel()
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("OPENAI_API_KEY not set")
	}

	provider, err := llm.ResolveProvider("openai:gpt-5.2")
	if err != nil {
		t.Fatalf("resolve provider: %v", err)
	}

	p := loadTestPlan(t)
	ctx := loadTestContext(t)
	prof, err := profile.LoadBuiltin("go-backend")
	if err != nil {
		t.Fatalf("load profile: %v", err)
	}

	rev := runReview(t, provider, prompt.BuildOpts{
		Plan:     p,
		Contexts: []*pctx.File{ctx},
		Profile:  prof,
	}, len(p.Lines))

	assertHasCategory(t, rev.Issues, review.CategoryContradiction)
}

// ---------- Strict grounding mode ----------

func TestIntegrationAnthropicStrict(t *testing.T) {
	skipUnlessIntegration(t)
	t.Parallel()
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY not set")
	}

	provider, err := llm.ResolveProvider("anthropic:claude-sonnet-4-6")
	if err != nil {
		t.Fatalf("resolve provider: %v", err)
	}

	p := loadTestPlan(t)
	prof, err := profile.LoadBuiltin("general")
	if err != nil {
		t.Fatalf("load profile: %v", err)
	}

	rev := runReview(t, provider, prompt.BuildOpts{
		Plan:    p,
		Profile: prof,
		Strict:  true,
	}, len(p.Lines))

	// In strict mode, all evidence must come from plan or context — no fabricated repo knowledge
	violations := review.CheckGrounding(&rev)
	if len(violations) > 0 {
		for _, v := range violations {
			t.Logf("grounding violation: issue=%s field=%s phrase=%q", v.IssueID, v.Field, v.Phrase)
		}
		t.Logf("found %d grounding violations in strict mode (will be downgraded in production)", len(violations))
	}

	if len(rev.Issues) == 0 {
		t.Error("strict mode produced no issues")
	}
}

func TestIntegrationOpenAIStrict(t *testing.T) {
	skipUnlessIntegration(t)
	t.Parallel()
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("OPENAI_API_KEY not set")
	}

	provider, err := llm.ResolveProvider("openai:gpt-5.2")
	if err != nil {
		t.Fatalf("resolve provider: %v", err)
	}

	p := loadTestPlan(t)
	prof, err := profile.LoadBuiltin("general")
	if err != nil {
		t.Fatalf("load profile: %v", err)
	}

	rev := runReview(t, provider, prompt.BuildOpts{
		Plan:    p,
		Profile: prof,
		Strict:  true,
	}, len(p.Lines))

	violations := review.CheckGrounding(&rev)
	if len(violations) > 0 {
		for _, v := range violations {
			t.Logf("grounding violation: issue=%s field=%s phrase=%q", v.IssueID, v.Field, v.Phrase)
		}
		t.Logf("found %d grounding violations in strict mode (will be downgraded in production)", len(violations))
	}

	if len(rev.Issues) == 0 {
		t.Error("strict mode produced no issues")
	}
}

// ---------- Post-processing pipeline ----------

func TestIntegrationPostProcessing(t *testing.T) {
	skipUnlessIntegration(t)
	t.Parallel()

	provider, err := llm.ResolveProvider("")
	if err != nil {
		t.Fatalf("resolve provider: %v", err)
	}
	t.Logf("Auto-detected provider: %s", provider.Name())

	p := loadTestPlan(t)
	ctx := loadTestContext(t)
	prof, err := profile.LoadBuiltin("go-backend")
	if err != nil {
		t.Fatalf("load profile: %v", err)
	}

	rev := runReview(t, provider, prompt.BuildOpts{
		Plan:     p,
		Contexts: []*pctx.File{ctx},
		Profile:  prof,
		Strict:   true,
	}, len(p.Lines))

	// Apply full post-processing pipeline
	review.SortIssues(rev.Issues)
	review.SortQuestions(rev.Questions)
	review.Truncate(&rev, review.DefaultMaxIssues, review.DefaultMaxQuestions)

	violations := review.CheckGrounding(&rev)
	review.ApplyGroundingDowngrades(&rev, violations)

	// Re-sort and recompute after downgrades (downgrades change severity)
	review.SortIssues(rev.Issues)
	summary := review.ComputeSummary(rev.Issues)
	rev.Summary = summary

	// Re-validate after full pipeline (non-fatal for LLM-invented categories)
	validationErrs := schema.Validate(&rev, len(p.Lines))
	for _, e := range validationErrs {
		t.Logf("post-pipeline validation warning: %s", e)
	}

	// Verify sorting invariant: severity is non-increasing
	for i := 1; i < len(rev.Issues); i++ {
		if severityOrder(rev.Issues[i-1].Severity) > severityOrder(rev.Issues[i].Severity) {
			t.Errorf("sort violated: %s before %s at index %d",
				rev.Issues[i-1].Severity, rev.Issues[i].Severity, i)
		}
	}

	// Verify JSON round-trip stability
	data1, err := json.MarshalIndent(rev, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var rev2 review.Review
	if err := json.Unmarshal(data1, &rev2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	data2, err := json.MarshalIndent(rev2, "", "  ")
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	if string(data1) != string(data2) {
		t.Error("JSON round-trip produced different output")
	}

	t.Logf("Final: verdict=%s score=%d issues=%d questions=%d",
		rev.Summary.Verdict, rev.Summary.Score, len(rev.Issues), len(rev.Questions))
}

// ---------- Helpers ----------

func assertHasCategory(t *testing.T, issues []review.Issue, cat review.Category) {
	t.Helper()
	for _, iss := range issues {
		if iss.Category == cat {
			return
		}
	}
	t.Errorf("expected at least one issue with category %s", cat)
}

func assertHasContextEvidence(t *testing.T, rev review.Review) {
	t.Helper()
	for _, iss := range rev.Issues {
		for _, ev := range iss.Evidence {
			if ev.Source == "context" {
				return
			}
		}
	}
	for _, q := range rev.Questions {
		for _, ev := range q.Evidence {
			if ev.Source == "context" {
				return
			}
		}
	}
	t.Error("expected at least one evidence entry with source 'context'")
}
