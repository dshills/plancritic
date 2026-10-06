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
// planLineCount is the total number of lines in the plan file (0 to
// skip plan line-range checks). contextLineCounts maps a context
// file's basename (the identifier used in the prompt, matching
// Evidence.Path) to its total line count; pass nil to skip context
// line-range checks. Range checks are only enforced when a positive
// count is supplied for the cited source.
func Validate(r *review.Review, planLineCount int, contextLineCounts map[string]int) []ValidationError {
	var errs []ValidationError

	// Note: tool, version, score, and severity counts are NOT validated here
	// because the CLI tool overwrites them deterministically after validation.
	// Validating LLM-produced values for these fields would cause unnecessary
	// repair round-trips.

	if !r.Summary.Verdict.Valid() {
		errs = append(errs, ValidationError{"summary.verdict", fmt.Sprintf("invalid verdict: %q", r.Summary.Verdict)})
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
			errs = append(errs, validateEvidence(fmt.Sprintf("%s.evidence[%d]", prefix, j), ev, planLineCount, contextLineCounts)...)
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
			errs = append(errs, validateEvidence(fmt.Sprintf("%s.evidence[%d]", prefix, j), ev, planLineCount, contextLineCounts)...)
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

	// Validate coverage (present only when a spec was supplied)
	if c := r.Coverage; c != nil {
		reqIDs := make(map[string]bool)
		for i, req := range c.Requirements {
			prefix := fmt.Sprintf("coverage.requirements[%d]", i)
			if req.ID == "" {
				errs = append(errs, ValidationError{prefix + ".id", "required"})
			} else if reqIDs[req.ID] {
				errs = append(errs, ValidationError{prefix + ".id", fmt.Sprintf("duplicate ID: %q", req.ID)})
			} else {
				reqIDs[req.ID] = true
			}
			if req.Requirement == "" {
				errs = append(errs, ValidationError{prefix + ".requirement", "required"})
			}
			if !req.Status.Valid() {
				errs = append(errs, ValidationError{prefix + ".status", fmt.Sprintf("invalid: %q", req.Status)})
			}
			if len(req.SpecEvidence) == 0 {
				errs = append(errs, ValidationError{prefix + ".spec_evidence", "at least one spec citation required"})
			}
			for j, ev := range req.SpecEvidence {
				where := fmt.Sprintf("%s.spec_evidence[%d]", prefix, j)
				if ev.Source != "context" {
					errs = append(errs, ValidationError{where + ".source", "spec citations must have source \"context\""})
				}
				errs = append(errs, validateEvidence(where, ev, planLineCount, contextLineCounts)...)
			}
			if req.Status != review.CoverageUncovered && len(req.PlanEvidence) == 0 {
				errs = append(errs, ValidationError{prefix + ".plan_evidence", "a COVERED or PARTIAL requirement must cite the plan"})
			}
			for j, ev := range req.PlanEvidence {
				where := fmt.Sprintf("%s.plan_evidence[%d]", prefix, j)
				if ev.Source != "plan" {
					errs = append(errs, ValidationError{where + ".source", "plan citations must have source \"plan\""})
				}
				errs = append(errs, validateEvidence(where, ev, planLineCount, contextLineCounts)...)
			}
		}
		scopeIDs := make(map[string]bool)
		for i, item := range c.OutOfScope {
			prefix := fmt.Sprintf("coverage.out_of_scope[%d]", i)
			if item.ID == "" {
				errs = append(errs, ValidationError{prefix + ".id", "required"})
			} else if scopeIDs[item.ID] {
				errs = append(errs, ValidationError{prefix + ".id", fmt.Sprintf("duplicate ID: %q", item.ID)})
			} else {
				scopeIDs[item.ID] = true
			}
			if item.PlanStep == "" {
				errs = append(errs, ValidationError{prefix + ".plan_step", "required"})
			}
			if len(item.PlanEvidence) == 0 {
				errs = append(errs, ValidationError{prefix + ".plan_evidence", "at least one plan citation required"})
			}
			for j, ev := range item.PlanEvidence {
				where := fmt.Sprintf("%s.plan_evidence[%d]", prefix, j)
				if ev.Source != "plan" {
					errs = append(errs, ValidationError{where + ".source", "plan citations must have source \"plan\""})
				}
				errs = append(errs, validateEvidence(where, ev, planLineCount, contextLineCounts)...)
			}
		}
	}

	return errs
}

func validateEvidence(prefix string, ev review.Evidence, planLineCount int, contextLineCounts map[string]int) []ValidationError {
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
	// Callers pass nil to skip context-side validation (used by tests
	// that don't care about cross-file consistency). An empty but
	// non-nil map means "no context files were provided" and any
	// "context" citation from the LLM is therefore invalid.
	if ev.Source == "context" && contextLineCounts != nil && ev.Path != "" {
		key := review.NormalizeContextPath(ev.Path)
		count, ok := contextLineCounts[key]
		if !ok {
			errs = append(errs, ValidationError{prefix + ".path", fmt.Sprintf("context %q was not provided", key)})
		} else if ev.LineEnd > count {
			errs = append(errs, ValidationError{prefix + ".line_end", fmt.Sprintf("exceeds context %q line count (%d)", key, count)})
		}
	}
	// Quote is no longer required from the LLM: the runner reconstructs
	// it deterministically from the line range (see review.ReconstructQuotes).
	return errs
}
