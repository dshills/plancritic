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

var itemPathPattern = regexp.MustCompile(`^(issues|questions|patches)\[(\d+)\]`)

// OffendingItems groups validation errors by the top-level item they
// belong to. The returned index slices are sorted and de-duplicated.
// Errors that do not belong to an indexed item (e.g. summary.verdict)
// are returned in other; callers that cannot repair those in isolation
// should fall back to a whole-output repair.
func OffendingItems(errs []ValidationError) (issues, questions, patches []int, other []ValidationError) {
	seen := map[string]map[int]bool{"issues": {}, "questions": {}, "patches": {}}
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
		seen[m[1]][idx] = true
	}
	collect := func(kind string) []int {
		out := make([]int, 0, len(seen[kind]))
		for i := range seen[kind] {
			out = append(out, i)
		}
		sort.Ints(out)
		return out
	}
	return collect("issues"), collect("questions"), collect("patches"), other
}
