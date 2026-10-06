package reviewer

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dshills/plancritic/internal/llm"
	"github.com/dshills/plancritic/internal/review"
	"github.com/dshills/plancritic/internal/schema"
)

type capturingProvider struct {
	response string
	prompts  []string
	settings []llm.Settings
}

func (c *capturingProvider) Name() string { return "mock" }

func (c *capturingProvider) Generate(_ context.Context, prompt string, s llm.Settings) (string, llm.Usage, error) {
	c.prompts = append(c.prompts, prompt)
	c.settings = append(c.settings, s)
	return c.response, llm.Usage{}, nil
}

func validIssue(id, title string) review.Issue {
	return review.Issue{
		ID: id, Severity: review.SeverityWarn, Category: review.CategoryAmbiguity, Title: title, Description: "d",
		Evidence: []review.Evidence{{Source: "plan", Path: "plan.md", LineStart: 1, LineEnd: 1}},
	}
}

// Validate can no longer produce a non-indexed error on its own (an
// invalid verdict is auto-fixed first), so the full-repair fallback is
// exercised directly here to keep its contract covered: it must send
// the review as it currently stands, not a stale raw output.
func TestRepairReviewFullFallbackSendsCurrentReview(t *testing.T) {
	rev := review.Review{
		Summary: review.Summary{Verdict: review.VerdictExecutable},
		Issues:  []review.Issue{validIssue("ISSUE-0002", "already renumbered")},
	}
	fixed, _ := json.Marshal(rev)
	p := &capturingProvider{response: string(fixed)}
	errs := []schema.ValidationError{{Path: "summary.something", Message: "synthetic non-indexed error"}}

	got, err := repairReview(context.Background(), p, llm.Settings{MaxTokens: 100}, rev, errs, repairBounds{PlanName: "plan.md", PlanLines: 3}, func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.prompts) != 1 {
		t.Fatalf("expected one repair call, got %d", len(p.prompts))
	}
	prompt := p.prompts[0]
	if !strings.Contains(prompt, "## Original Output") {
		t.Error("non-indexed errors should use the whole-output repair prompt")
	}
	if !strings.Contains(prompt, `"ISSUE-0002"`) || !strings.Contains(prompt, "synthetic non-indexed error") {
		t.Errorf("full repair should carry the current review and the errors:\n%s", prompt)
	}
	if p.settings[0].MaxTokens < repairMinMaxTokens {
		t.Errorf("repair should raise a small max-tokens cap, got %d", p.settings[0].MaxTokens)
	}
	if len(got.Issues) != 1 || got.Issues[0].Title != "already renumbered" {
		t.Errorf("unexpected repaired review: %+v", got)
	}
}

func TestMergeRepairedRejectsShortResponse(t *testing.T) {
	orig := review.Review{Issues: []review.Issue{validIssue("ISSUE-0001", "a"), validIssue("ISSUE-0002", "b")}}
	if _, err := mergeRepaired(orig, review.Review{}, []int{1}, nil, nil); err == nil {
		t.Error("expected an error when the repair returns fewer items than resent")
	}
	merged, err := mergeRepaired(orig, review.Review{Issues: []review.Issue{validIssue("ISSUE-0002", "fixed")}}, []int{1}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if merged.Issues[0].Title != "a" || merged.Issues[1].Title != "fixed" {
		t.Errorf("merge should replace only index 1: %+v", merged.Issues)
	}
	if orig.Issues[1].Title != "b" {
		t.Error("merge must not mutate the original slice")
	}
}
