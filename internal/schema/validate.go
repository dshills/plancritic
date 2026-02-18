// Package schema validates review output against the PlanCritic schema.
package schema

import (
	"fmt"

	"github.com/dshills/plancritic/internal/review"
)

// ValidationError describes a single schema violation.
type ValidationError struct {
	Path    string
	Message string
}

func (v ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", v.Path, v.Message)
}

// Validate checks a Review for structural validity.
// planLineCount is the total number of lines in the plan file (0 to skip line range checks).
func Validate(r *review.Review, planLineCount int) []ValidationError {
	var errs []ValidationError

	if r.Tool == "" {
		errs = append(errs, ValidationError{"tool", "required"})
	}
	if r.Version == "" {
		errs = append(errs, ValidationError{"version", "required"})
	}
	if !r.Summary.Verdict.Valid() {
		errs = append(errs, ValidationError{"summary.verdict", fmt.Sprintf("invalid verdict: %q", r.Summary.Verdict)})
	}

	// Verify score consistency
	expected := review.ComputeScore(r.Issues)
	if r.Summary.Score != expected {
		errs = append(errs, ValidationError{"summary.score", fmt.Sprintf("score %d does not match computed %d", r.Summary.Score, expected)})
	}

	// Verify severity counts
	var crit, warn, info int
	for _, iss := range r.Issues {
		switch iss.Severity {
		case review.SeverityCritical:
			crit++
		case review.SeverityWarn:
			warn++
		case review.SeverityInfo:
			info++
		}
	}
	if r.Summary.CriticalCount != crit {
		errs = append(errs, ValidationError{"summary.critical_count", fmt.Sprintf("expected %d, got %d", crit, r.Summary.CriticalCount)})
	}
	if r.Summary.WarnCount != warn {
		errs = append(errs, ValidationError{"summary.warn_count", fmt.Sprintf("expected %d, got %d", warn, r.Summary.WarnCount)})
	}
	if r.Summary.InfoCount != info {
		errs = append(errs, ValidationError{"summary.info_count", fmt.Sprintf("expected %d, got %d", info, r.Summary.InfoCount)})
	}

	// Validate issues
	issueIDs := make(map[string]bool)
	for i, iss := range r.Issues {
		prefix := fmt.Sprintf("issues[%d]", i)
		if iss.ID == "" {
			errs = append(errs, ValidationError{prefix + ".id", "required"})
		} else if issueIDs[iss.ID] {
			errs = append(errs, ValidationError{prefix + ".id", fmt.Sprintf("duplicate ID: %q", iss.ID)})
		} else {
			issueIDs[iss.ID] = true
		}
		if !iss.Severity.Valid() {
			errs = append(errs, ValidationError{prefix + ".severity", fmt.Sprintf("invalid: %q", iss.Severity)})
		}
		if !iss.Category.Valid() {
			errs = append(errs, ValidationError{prefix + ".category", fmt.Sprintf("invalid: %q", iss.Category)})
		}
		if iss.Title == "" {
			errs = append(errs, ValidationError{prefix + ".title", "required"})
		}
		if iss.Description == "" {
			errs = append(errs, ValidationError{prefix + ".description", "required"})
		}
		if len(iss.Evidence) == 0 {
			errs = append(errs, ValidationError{prefix + ".evidence", "at least one evidence entry required"})
		}
		for j, ev := range iss.Evidence {
			errs = append(errs, validateEvidence(fmt.Sprintf("%s.evidence[%d]", prefix, j), ev, planLineCount)...)
		}
	}

	// Validate questions
	questionIDs := make(map[string]bool)
	for i, q := range r.Questions {
		prefix := fmt.Sprintf("questions[%d]", i)
		if q.ID == "" {
			errs = append(errs, ValidationError{prefix + ".id", "required"})
		} else if questionIDs[q.ID] {
			errs = append(errs, ValidationError{prefix + ".id", fmt.Sprintf("duplicate ID: %q", q.ID)})
		} else {
			questionIDs[q.ID] = true
		}
		if !q.Severity.Valid() {
			errs = append(errs, ValidationError{prefix + ".severity", fmt.Sprintf("invalid: %q", q.Severity)})
		}
		if q.Question == "" {
			errs = append(errs, ValidationError{prefix + ".question", "required"})
		}
		if q.WhyNeeded == "" {
			errs = append(errs, ValidationError{prefix + ".why_needed", "required"})
		}
		if len(q.Evidence) == 0 {
			errs = append(errs, ValidationError{prefix + ".evidence", "at least one evidence entry required"})
		}
		for j, ev := range q.Evidence {
			errs = append(errs, validateEvidence(fmt.Sprintf("%s.evidence[%d]", prefix, j), ev, planLineCount)...)
		}
	}

	// Validate patches
	for i, p := range r.Patches {
		prefix := fmt.Sprintf("patches[%d]", i)
		if p.ID == "" {
			errs = append(errs, ValidationError{prefix + ".id", "required"})
		}
		if !p.Type.Valid() {
			errs = append(errs, ValidationError{prefix + ".type", fmt.Sprintf("invalid: %q", p.Type)})
		}
		if p.Title == "" {
			errs = append(errs, ValidationError{prefix + ".title", "required"})
		}
		if p.DiffUnified == "" {
			errs = append(errs, ValidationError{prefix + ".diff_unified", "required"})
		}
	}

	return errs
}

func validateEvidence(prefix string, ev review.Evidence, planLineCount int) []ValidationError {
	var errs []ValidationError
	if ev.Source != "plan" && ev.Source != "context" {
		errs = append(errs, ValidationError{prefix + ".source", fmt.Sprintf("must be 'plan' or 'context', got %q", ev.Source)})
	}
	if ev.Path == "" {
		errs = append(errs, ValidationError{prefix + ".path", "required"})
	}
	if ev.LineStart < 1 {
		errs = append(errs, ValidationError{prefix + ".line_start", "must be >= 1"})
	}
	if ev.LineEnd < ev.LineStart {
		errs = append(errs, ValidationError{prefix + ".line_end", "must be >= line_start"})
	}
	if planLineCount > 0 && ev.Source == "plan" && ev.LineEnd > planLineCount {
		errs = append(errs, ValidationError{prefix + ".line_end", fmt.Sprintf("exceeds plan line count (%d)", planLineCount)})
	}
	if ev.Quote == "" {
		errs = append(errs, ValidationError{prefix + ".quote", "required"})
	}
	return errs
}
