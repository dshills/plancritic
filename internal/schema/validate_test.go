package schema

import (
	"testing"

	"github.com/dshills/plancritic/internal/review"
)

func validReview() *review.Review {
	issues := []review.Issue{
		{
			ID: "ISSUE-0001", Severity: review.SeverityCritical,
			Category: review.CategoryContradiction,
			Title: "Test issue", Description: "A test issue",
			Evidence: []review.Evidence{
				{Source: "plan", Path: "plan.md", LineStart: 1, LineEnd: 2, Quote: "some text"},
			},
			Impact: "big", Recommendation: "fix it",
		},
	}
	return &review.Review{
		Tool:    "plancritic",
		Version: "1.0",
		Summary: review.ComputeSummary(issues),
		Issues:  issues,
		Questions: []review.Question{
			{
				ID: "Q-0001", Severity: review.SeverityWarn,
				Question: "What?", WhyNeeded: "Because",
				Evidence: []review.Evidence{
					{Source: "plan", Path: "plan.md", LineStart: 3, LineEnd: 4, Quote: "other text"},
				},
			},
		},
	}
}

func TestValidateValid(t *testing.T) {
	errs := Validate(validReview(), 100)
	if len(errs) > 0 {
		for _, e := range errs {
			t.Errorf("unexpected error: %s", e)
		}
	}
}

func TestValidateMissingTool(t *testing.T) {
	r := validReview()
	r.Tool = ""
	errs := Validate(r, 0)
	found := false
	for _, e := range errs {
		if e.Path == "tool" {
			found = true
		}
	}
	if !found {
		t.Error("expected error for missing tool")
	}
}

func TestValidateInvalidVerdict(t *testing.T) {
	r := validReview()
	r.Summary.Verdict = "INVALID"
	errs := Validate(r, 0)
	found := false
	for _, e := range errs {
		if e.Path == "summary.verdict" {
			found = true
		}
	}
	if !found {
		t.Error("expected error for invalid verdict")
	}
}

func TestValidateDuplicateIssueIDs(t *testing.T) {
	r := validReview()
	r.Issues = append(r.Issues, r.Issues[0])
	// Recompute score to match
	r.Summary.Score = review.ComputeScore(r.Issues)
	errs := Validate(r, 0)
	found := false
	for _, e := range errs {
		if e.Message == `duplicate ID: "ISSUE-0001"` {
			found = true
		}
	}
	if !found {
		t.Error("expected duplicate ID error")
	}
}

func TestValidateInvalidEvidence(t *testing.T) {
	r := validReview()
	r.Issues[0].Evidence[0].Source = "unknown"
	r.Issues[0].Evidence[0].LineStart = 0
	errs := Validate(r, 0)
	if len(errs) < 2 {
		t.Errorf("expected at least 2 evidence errors, got %d", len(errs))
	}
}

func TestValidateLineExceedsPlan(t *testing.T) {
	r := validReview()
	r.Issues[0].Evidence[0].LineEnd = 200
	errs := Validate(r, 50)
	found := false
	for _, e := range errs {
		if e.Path == "issues[0].evidence[0].line_end" {
			found = true
		}
	}
	if !found {
		t.Error("expected line_end exceeds plan error")
	}
}

func TestValidateScoreMismatch(t *testing.T) {
	r := validReview()
	r.Summary.Score = 99 // Wrong
	errs := Validate(r, 0)
	found := false
	for _, e := range errs {
		if e.Path == "summary.score" {
			found = true
		}
	}
	if !found {
		t.Error("expected score mismatch error")
	}
}

func TestValidatePatches(t *testing.T) {
	r := validReview()
	r.Patches = []review.Patch{
		{ID: "", Type: "INVALID", Title: "", DiffUnified: ""},
	}
	errs := Validate(r, 0)
	if len(errs) < 3 {
		t.Errorf("expected at least 3 patch errors, got %d", len(errs))
	}
}
