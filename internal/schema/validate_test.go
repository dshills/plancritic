package schema

import (
	"strings"
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

// --- Missing top-level fields ---

func TestValidateMissingVersion(t *testing.T) {
	r := validReview()
	r.Version = ""
	errs := Validate(r, 0)
	assertHasError(t, errs, "version", "")
}

// --- Severity count mismatches ---

func TestValidateSeverityCountMismatches(t *testing.T) {
	r := validReview()
	r.Summary.CriticalCount = 99
	r.Summary.WarnCount = 99
	r.Summary.InfoCount = 99
	errs := Validate(r, 0)
	assertHasError(t, errs, "summary.critical_count", "")
	assertHasError(t, errs, "summary.warn_count", "")
	assertHasError(t, errs, "summary.info_count", "")
}

// --- Issue field validation ---

func TestValidateIssueEmptyID(t *testing.T) {
	r := validReview()
	r.Issues[0].ID = ""
	errs := Validate(r, 0)
	assertHasError(t, errs, "issues[0].id", "required")
}

func TestValidateIssueInvalidSeverity(t *testing.T) {
	r := validReview()
	r.Issues[0].Severity = "BOGUS"
	errs := Validate(r, 0)
	assertHasError(t, errs, "issues[0].severity", "invalid")
}

func TestValidateIssueInvalidCategory(t *testing.T) {
	r := validReview()
	r.Issues[0].Category = "NOT_A_CATEGORY"
	errs := Validate(r, 0)
	assertHasError(t, errs, "issues[0].category", "invalid")
}

func TestValidateIssueEmptyTitle(t *testing.T) {
	r := validReview()
	r.Issues[0].Title = ""
	errs := Validate(r, 0)
	assertHasError(t, errs, "issues[0].title", "required")
}

func TestValidateIssueEmptyDescription(t *testing.T) {
	r := validReview()
	r.Issues[0].Description = ""
	errs := Validate(r, 0)
	assertHasError(t, errs, "issues[0].description", "required")
}

func TestValidateIssueEmptyEvidence(t *testing.T) {
	r := validReview()
	r.Issues[0].Evidence = []review.Evidence{}
	errs := Validate(r, 0)
	assertHasError(t, errs, "issues[0].evidence", "at least one")
}

// --- Question validation ---

func TestValidateQuestionEmptyID(t *testing.T) {
	r := validReview()
	r.Questions[0].ID = ""
	errs := Validate(r, 0)
	assertHasError(t, errs, "questions[0].id", "required")
}

func TestValidateDuplicateQuestionIDs(t *testing.T) {
	r := validReview()
	r.Questions = append(r.Questions, r.Questions[0])
	errs := Validate(r, 0)
	assertHasError(t, errs, "questions[1].id", "duplicate")
}

func TestValidateQuestionInvalidSeverity(t *testing.T) {
	r := validReview()
	r.Questions[0].Severity = "NOPE"
	errs := Validate(r, 0)
	assertHasError(t, errs, "questions[0].severity", "invalid")
}

func TestValidateQuestionEmptyQuestion(t *testing.T) {
	r := validReview()
	r.Questions[0].Question = ""
	errs := Validate(r, 0)
	assertHasError(t, errs, "questions[0].question", "required")
}

func TestValidateQuestionEmptyWhyNeeded(t *testing.T) {
	r := validReview()
	r.Questions[0].WhyNeeded = ""
	errs := Validate(r, 0)
	assertHasError(t, errs, "questions[0].why_needed", "required")
}

func TestValidateQuestionEmptyEvidence(t *testing.T) {
	r := validReview()
	r.Questions[0].Evidence = []review.Evidence{}
	errs := Validate(r, 0)
	assertHasError(t, errs, "questions[0].evidence", "at least one")
}

// --- Evidence edge cases ---

func TestValidateEvidenceEmptyPath(t *testing.T) {
	r := validReview()
	r.Issues[0].Evidence[0].Path = ""
	errs := Validate(r, 0)
	assertHasError(t, errs, "issues[0].evidence[0].path", "required")
}

func TestValidateEvidenceLineEndLessThanLineStart(t *testing.T) {
	r := validReview()
	r.Issues[0].Evidence[0].LineStart = 10
	r.Issues[0].Evidence[0].LineEnd = 5
	errs := Validate(r, 0)
	assertHasError(t, errs, "issues[0].evidence[0].line_end", "line_start")
}

func TestValidateEvidenceEmptyQuote(t *testing.T) {
	r := validReview()
	r.Issues[0].Evidence[0].Quote = ""
	errs := Validate(r, 0)
	assertHasError(t, errs, "issues[0].evidence[0].quote", "required")
}

func TestValidateEvidenceInvalidSource(t *testing.T) {
	r := validReview()
	r.Issues[0].Evidence[0].Source = "filesystem"
	errs := Validate(r, 0)
	assertHasError(t, errs, "issues[0].evidence[0].source", "plan")
}

func TestValidateEvidenceLineStartZero(t *testing.T) {
	r := validReview()
	r.Issues[0].Evidence[0].LineStart = 0
	errs := Validate(r, 0)
	assertHasError(t, errs, "issues[0].evidence[0].line_start", ">= 1")
}

// --- helper ---

func assertHasError(t *testing.T, errs []ValidationError, path, msgSubstring string) {
	t.Helper()
	for _, e := range errs {
		if e.Path == path {
			if msgSubstring == "" || strings.Contains(e.Message, msgSubstring) {
				return
			}
		}
	}
	t.Errorf("expected validation error at path %q containing %q, got errors: %v", path, msgSubstring, errs)
}
