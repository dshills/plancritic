package reviewer

import (
	"path/filepath"
	"strings"

	"github.com/dshills/plancritic/internal/lint"
	"github.com/dshills/plancritic/internal/plan"
	"github.com/dshills/plancritic/internal/profile"
	"github.com/dshills/plancritic/internal/redact"
	"github.com/dshills/plancritic/internal/review"
)

// LintOptions configures Lint.
type LintOptions struct {
	ProfileName       string
	SeverityThreshold string
	MaxIssues         int
	RedactEnabled     bool
}

// Lint runs only the local, zero-token checks over the plan and returns
// a review in the same shape as Run produces, with meta.model set to
// "local/lint". No provider is contacted and nothing is cached.
func Lint(planPath string, o LintOptions, version string) (review.Review, error) {
	p, err := plan.Load(planPath)
	if err != nil {
		return review.Review{}, Errorf(3, "failed to load plan: %v", err)
	}
	if o.RedactEnabled {
		p.Raw = redact.Redact(p.Raw)
		p.Lines = strings.Split(p.Raw, "\n")
	}
	profName := o.ProfileName
	if profName == "" {
		profName = "general"
	}
	prof, err := profile.LoadBuiltin(profName)
	if err != nil {
		return review.Review{}, Errorf(3, "failed to load profile: %v", err)
	}

	rev := review.Review{
		Tool:      "plancritic",
		Version:   version,
		Input:     review.Input{PlanFile: filepath.Base(planPath), PlanHash: p.Hash, Profile: profName},
		Issues:    lint.Run(p, prof),
		Questions: []review.Question{},
		Meta:      review.Meta{Model: "local/lint"},
	}
	review.SortIssues(rev.Issues)
	rev.Issues = review.FilterBySeverity(rev.Issues, o.SeverityThreshold)
	maxIssues := o.MaxIssues
	if maxIssues <= 0 {
		maxIssues = review.DefaultMaxIssues
	}
	review.Truncate(&rev, maxIssues, review.DefaultMaxQuestions)
	rev.Summary = review.ComputeSummary(rev.Issues)
	review.AssignFingerprints(&rev)
	return rev, nil
}
