package schema

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"

	"github.com/dshills/plancritic/internal/review"
)

// AutoFix repairs mechanical defects in r that need no judgment from the
// model and returns a human-readable line per fix applied. It handles
// empty or duplicate IDs (renumbered) and evidence line ranges that are
// inverted, start below 1, or run past the end of the cited file
// (clamped). Anything else, such as an unknown context path or a missing
// evidence list, is left for Validate to report and the model to repair.
func AutoFix(r *review.Review, planLineCount int, contextLineCounts map[string]int) []string {
	var fixes []string

	// The verdict is recomputed from the issues after validation, so the
	// model's value is never used. A missing one (typically because the
	// output was truncated before the summary) must not cost a repair.
	if !r.Summary.Verdict.Valid() {
		fixes = append(fixes, fmt.Sprintf("summary.verdict: %q replaced with a placeholder (the verdict is computed from the issues)", r.Summary.Verdict))
		r.Summary.Verdict = review.VerdictWithClarifications
	}

	issueIDs := make([]*string, len(r.Issues))
	for i := range r.Issues {
		issueIDs[i] = &r.Issues[i].ID
	}
	fixes = append(fixes, fixIDs(issueIDs, "ISSUE", "issues")...)
	for i := range r.Issues {
		for j := range r.Issues[i].Evidence {
			fixes = append(fixes, fixEvidence(&r.Issues[i].Evidence[j], fmt.Sprintf("issues[%d].evidence[%d]", i, j), planLineCount, contextLineCounts)...)
		}
	}

	questionIDs := make([]*string, len(r.Questions))
	for i := range r.Questions {
		questionIDs[i] = &r.Questions[i].ID
	}
	fixes = append(fixes, fixIDs(questionIDs, "Q", "questions")...)
	for i := range r.Questions {
		for j := range r.Questions[i].Evidence {
			fixes = append(fixes, fixEvidence(&r.Questions[i].Evidence[j], fmt.Sprintf("questions[%d].evidence[%d]", i, j), planLineCount, contextLineCounts)...)
		}
	}

	patchIDs := make([]*string, len(r.Patches))
	for i := range r.Patches {
		patchIDs[i] = &r.Patches[i].ID
	}
	fixes = append(fixes, fixIDs(patchIDs, "PATCH", "patches")...)

	if c := r.Coverage; c != nil {
		reqIDs := make([]*string, len(c.Requirements))
		for i := range c.Requirements {
			reqIDs[i] = &c.Requirements[i].ID
		}
		fixes = append(fixes, fixIDs(reqIDs, "REQ", "coverage.requirements")...)
		for i := range c.Requirements {
			for j := range c.Requirements[i].SpecEvidence {
				fixes = append(fixes, fixEvidence(&c.Requirements[i].SpecEvidence[j], fmt.Sprintf("coverage.requirements[%d].spec_evidence[%d]", i, j), planLineCount, contextLineCounts)...)
			}
			for j := range c.Requirements[i].PlanEvidence {
				fixes = append(fixes, fixEvidence(&c.Requirements[i].PlanEvidence[j], fmt.Sprintf("coverage.requirements[%d].plan_evidence[%d]", i, j), planLineCount, contextLineCounts)...)
			}
		}
		scopeIDs := make([]*string, len(c.OutOfScope))
		for i := range c.OutOfScope {
			scopeIDs[i] = &c.OutOfScope[i].ID
		}
		fixes = append(fixes, fixIDs(scopeIDs, "SCOPE", "coverage.out_of_scope")...)
		for i := range c.OutOfScope {
			for j := range c.OutOfScope[i].PlanEvidence {
				fixes = append(fixes, fixEvidence(&c.OutOfScope[i].PlanEvidence[j], fmt.Sprintf("coverage.out_of_scope[%d].plan_evidence[%d]", i, j), planLineCount, contextLineCounts)...)
			}
		}
	}

	return fixes
}

// fixIDs assigns fresh IDs to empty entries and to later duplicates of
// an earlier ID. Every non-empty ID present in the collection is
// reserved first, so a replacement never steals an ID that a later,
// valid item already holds.
func fixIDs(ids []*string, prefix, kind string) []string {
	reserved := make(map[string]bool, len(ids))
	for _, id := range ids {
		if *id != "" {
			reserved[*id] = true
		}
	}

	var fixes []string
	visited := make(map[string]bool, len(ids))
	next := 1
	for i, id := range ids {
		if *id != "" && !visited[*id] {
			visited[*id] = true
			continue
		}
		old := *id
		for ; ; next++ {
			candidate := fmt.Sprintf("%s-%04d", prefix, next)
			if !reserved[candidate] {
				*id = candidate
				reserved[candidate] = true
				visited[candidate] = true
				next++
				break
			}
		}
		if old == "" {
			fixes = append(fixes, fmt.Sprintf("%s[%d].id: assigned %s (was empty)", kind, i, *id))
		} else {
			fixes = append(fixes, fmt.Sprintf("%s[%d].id: renamed duplicate %q to %s", kind, i, old, *id))
		}
	}
	return fixes
}

func fixEvidence(ev *review.Evidence, where string, planLineCount int, contextLineCounts map[string]int) []string {
	var fixes []string

	if ev.LineStart < 1 && ev.LineEnd >= 1 {
		fixes = append(fixes, fmt.Sprintf("%s.line_start: raised %d to 1", where, ev.LineStart))
		ev.LineStart = 1
	}
	if ev.LineStart >= 1 && ev.LineEnd >= 1 && ev.LineEnd < ev.LineStart {
		fixes = append(fixes, fmt.Sprintf("%s: swapped inverted range %d-%d", where, ev.LineStart, ev.LineEnd))
		ev.LineStart, ev.LineEnd = ev.LineEnd, ev.LineStart
	}

	limit := 0
	switch ev.Source {
	case "plan":
		limit = planLineCount
	case "context":
		if contextLineCounts != nil && ev.Path != "" {
			limit = contextLineCounts[review.NormalizeContextPath(ev.Path)]
		}
	}
	// Clamp only when the start is still inside the file; a range that
	// begins past the end is a wrong citation, not an off-by-some.
	if limit > 0 && ev.LineEnd > limit && ev.LineStart <= limit {
		fixes = append(fixes, fmt.Sprintf("%s.line_end: clamped %d to %d", where, ev.LineEnd, limit))
		ev.LineEnd = limit
	}
	return fixes
}

// RepairKinds lists the top-level item collections a delta repair can
// resend, in the order they appear in a repair prompt and response.
var RepairKinds = []string{"issues", "questions", "patches", "coverage.requirements", "coverage.out_of_scope"}

var itemPathPattern = regexp.MustCompile(`^(issues|questions|patches|coverage\.requirements|coverage\.out_of_scope)\[(\d+)\]`)

// OffendingItems groups validation errors by the top-level item they
// belong to, keyed by kind (see RepairKinds). Each index slice is
// sorted and de-duplicated. Errors that do not belong to an indexed
// item are returned in other; callers that cannot repair those in
// isolation should fall back to a whole-output repair.
func OffendingItems(errs []ValidationError) (byKind map[string][]int, other []ValidationError) {
	seen := make(map[string]map[int]bool)
	for _, e := range errs {
		m := itemPathPattern.FindStringSubmatch(e.Path)
		if m == nil {
			other = append(other, e)
			continue
		}
		idx, err := strconv.Atoi(m[2])
		if err != nil {
			other = append(other, e)
			continue
		}
		if seen[m[1]] == nil {
			seen[m[1]] = make(map[int]bool)
		}
		seen[m[1]][idx] = true
	}
	byKind = make(map[string][]int, len(seen))
	for kind, idxs := range seen {
		out := make([]int, 0, len(idxs))
		for i := range idxs {
			out = append(out, i)
		}
		sort.Ints(out)
		byKind[kind] = out
	}
	return byKind, other
}
