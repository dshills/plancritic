package reviewer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/dshills/plancritic/internal/llm"
	"github.com/dshills/plancritic/internal/prompt"
	"github.com/dshills/plancritic/internal/review"
	"github.com/dshills/plancritic/internal/schema"
)

// repairBounds carries the citation limits a repair must respect.
type repairBounds struct {
	PlanName          string
	PlanLines         int
	ContextLineCounts map[string]int
	// Sources is the line-numbered plan and context text, attached to a
	// delta repair only when an error concerns evidence.
	Sources string
	// Shape of the original request; repairs must ask for the same keys.
	Shape schema.OutputShape
}

// repairReview asks the model to fix validation errors in rev. When every
// error belongs to an indexed issue, question, or patch, only those items
// are resent (a delta repair) and spliced back into rev. Otherwise the
// whole output is resent. The result is validated again; a second
// failure is a schema error (exit code 5).
func repairReview(
	ctx context.Context,
	provider llm.Provider,
	settings llm.Settings,
	rev review.Review,
	errs []schema.ValidationError,
	bounds repairBounds,
	verbose func(string, ...any),
) (review.Review, error) {
	issueIdx, qIdx, pIdx, other := schema.OffendingItems(errs)
	delta := len(other) == 0 && len(issueIdx)+len(qIdx)+len(pIdx) > 0

	var promptText string
	if delta {
		items, err := collectRepairItems(rev, issueIdx, qIdx, pIdx)
		if err != nil {
			return review.Review{}, Errorf(5, "prepare repair: %v", err)
		}
		verbose("Delta repair: resending %d issue(s), %d question(s), %d patch(es)", len(issueIdx), len(qIdx), len(pIdx))
		opts := prompt.DeltaRepairOpts{
			Items:             items,
			Errors:            errs,
			PlanName:          bounds.PlanName,
			PlanLines:         bounds.PlanLines,
			ContextLineCounts: bounds.ContextLineCounts,
			Shape:             bounds.Shape,
		}
		if needsSources(errs) {
			opts.Sources = bounds.Sources
		}
		promptText = prompt.BuildDeltaRepair(opts)
	} else {
		verbose("Full repair: %d error(s) outside indexed items", len(other))
		// Send the review as it stands now (after AutoFix), so the output
		// the model sees matches the errors it is asked to fix.
		current, err := json.Marshal(rev)
		if err != nil {
			return review.Review{}, Errorf(5, "prepare full repair: %v", err)
		}
		promptText = prompt.BuildRepair(string(current), errs, bounds.Shape)
	}

	out, usage, err := provider.Generate(ctx, promptText, settings)
	if err != nil {
		return review.Review{}, Errorf(4, "repair LLM call failed: %v", err)
	}
	if usage.InputTokens > 0 {
		verbose("Repair token usage: input=%d, output=%d", usage.InputTokens, usage.OutputTokens)
	}
	out = llm.ExtractJSON(out)

	var rev2 review.Review
	if err := json.Unmarshal([]byte(out), &rev2); err != nil {
		sanitized := llm.SanitizeJSON(out)
		if err2 := json.Unmarshal([]byte(sanitized), &rev2); err2 != nil {
			return review.Review{}, Errorf(5, "repair response is not valid JSON: %v (pre-sanitize: %v)", err2, err)
		}
	}

	if delta {
		merged, err := mergeRepaired(rev, rev2, issueIdx, qIdx, pIdx)
		if err != nil {
			return review.Review{}, Errorf(5, "repair response unusable: %v", err)
		}
		rev2 = merged
	}

	errs2 := schema.Validate(&rev2, bounds.PlanLines, bounds.ContextLineCounts)
	if len(errs2) > 0 {
		fmt.Fprintln(os.Stderr, "Schema validation errors after repair:")
		for _, e := range errs2 {
			fmt.Fprintf(os.Stderr, "  %s\n", e)
		}
		return review.Review{}, Errorf(5, "LLM output failed schema validation after repair")
	}
	return rev2, nil
}

// needsSources reports whether any error concerns evidence, in which
// case the model must see the source text to choose a correct citation.
func needsSources(errs []schema.ValidationError) bool {
	for _, e := range errs {
		if strings.Contains(e.Path, ".evidence") {
			return true
		}
	}
	return false
}

func collectRepairItems(rev review.Review, issueIdx, qIdx, pIdx []int) ([]prompt.RepairItem, error) {
	var items []prompt.RepairItem
	add := func(kind string, idx int, v any) error {
		data, err := json.Marshal(v)
		if err != nil {
			return err
		}
		items = append(items, prompt.RepairItem{Kind: kind, Index: idx, JSON: string(data)})
		return nil
	}
	for _, i := range issueIdx {
		if i >= len(rev.Issues) {
			return nil, fmt.Errorf("issues[%d] out of range", i)
		}
		if err := add("issues", i, rev.Issues[i]); err != nil {
			return nil, err
		}
	}
	for _, i := range qIdx {
		if i >= len(rev.Questions) {
			return nil, fmt.Errorf("questions[%d] out of range", i)
		}
		if err := add("questions", i, rev.Questions[i]); err != nil {
			return nil, err
		}
	}
	for _, i := range pIdx {
		if i >= len(rev.Patches) {
			return nil, fmt.Errorf("patches[%d] out of range", i)
		}
		if err := add("patches", i, rev.Patches[i]); err != nil {
			return nil, err
		}
	}
	return items, nil
}

// mergeRepaired splices the corrected items from fix into a copy of orig
// at the offending indices, in order. fix must contain at least as many
// items of each kind as were resent; extras are ignored.
func mergeRepaired(orig, fix review.Review, issueIdx, qIdx, pIdx []int) (review.Review, error) {
	if len(fix.Issues) < len(issueIdx) {
		return review.Review{}, fmt.Errorf("repair returned %d issue(s), expected %d", len(fix.Issues), len(issueIdx))
	}
	if len(fix.Questions) < len(qIdx) {
		return review.Review{}, fmt.Errorf("repair returned %d question(s), expected %d", len(fix.Questions), len(qIdx))
	}
	if len(fix.Patches) < len(pIdx) {
		return review.Review{}, fmt.Errorf("repair returned %d patch(es), expected %d", len(fix.Patches), len(pIdx))
	}

	merged := orig
	merged.Issues = append([]review.Issue(nil), orig.Issues...)
	merged.Questions = append([]review.Question(nil), orig.Questions...)
	merged.Patches = append([]review.Patch(nil), orig.Patches...)
	for n, i := range issueIdx {
		merged.Issues[i] = fix.Issues[n]
	}
	for n, i := range qIdx {
		merged.Questions[i] = fix.Questions[n]
	}
	for n, i := range pIdx {
		merged.Patches[i] = fix.Patches[n]
	}
	return merged, nil
}
