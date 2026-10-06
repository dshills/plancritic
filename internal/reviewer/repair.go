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

// repairMinMaxTokens is the smallest output cap a repair call is made
// with, regardless of the user's --max-tokens.
const repairMinMaxTokens = 8192

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
// error belongs to an indexed item (an issue, question, patch, or
// coverage entry), only those items are resent (a delta repair) and
// spliced back into rev. Otherwise the whole output is resent. The
// result is validated again; a second failure is a schema error (exit
// code 5). The returned usage covers the repair call even on failure.
func repairReview(
	ctx context.Context,
	provider llm.Provider,
	settings llm.Settings,
	rev review.Review,
	errs []schema.ValidationError,
	bounds repairBounds,
	verbose func(string, ...any),
) (review.Review, llm.Usage, error) {
	byKind, other := schema.OffendingItems(errs)
	delta := len(other) == 0 && len(byKind) > 0

	var promptText string
	if delta {
		items, err := collectRepairItems(rev, byKind)
		if err != nil {
			return review.Review{}, llm.Usage{}, Errorf(5, "prepare repair: %v", err)
		}
		verbose("Delta repair: resending %d item(s) across %d kind(s)", len(items), len(byKind))
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
			return review.Review{}, llm.Usage{}, Errorf(5, "prepare full repair: %v", err)
		}
		promptText = prompt.BuildRepair(string(current), errs, bounds.Shape)
	}

	// A repair must be able to finish. When the original cap was small
	// (or was what truncated the original response), give the repair
	// room; its output is a handful of items at most.
	if settings.MaxTokens > 0 && settings.MaxTokens < repairMinMaxTokens {
		settings.MaxTokens = repairMinMaxTokens
	}
	out, usage, err := provider.Generate(ctx, promptText, settings)
	if err != nil {
		return review.Review{}, usage, Errorf(4, "repair LLM call failed: %v", err)
	}
	if usage.InputTokens > 0 {
		verbose("Repair token usage: input=%d, output=%d", usage.InputTokens, usage.OutputTokens)
	}
	out = llm.ExtractJSON(out)

	var rev2 review.Review
	if err := json.Unmarshal([]byte(out), &rev2); err != nil {
		sanitized := llm.SanitizeJSON(out)
		if err2 := json.Unmarshal([]byte(sanitized), &rev2); err2 != nil {
			return review.Review{}, usage, Errorf(5, "repair response is not valid JSON: %v (pre-sanitize: %v)", err2, err)
		}
	}

	if delta {
		merged, err := mergeRepaired(rev, rev2, byKind)
		if err != nil {
			return review.Review{}, usage, Errorf(5, "repair response unusable: %v", err)
		}
		rev2 = merged
	}

	errs2 := schema.Validate(&rev2, bounds.PlanLines, bounds.ContextLineCounts)
	if bounds.Shape.Coverage && rev2.Coverage == nil {
		errs2 = append(errs2, schema.ValidationError{Path: "coverage", Message: "still missing after repair"})
	}
	if len(errs2) > 0 {
		fmt.Fprintln(os.Stderr, "Schema validation errors after repair:")
		for _, e := range errs2 {
			fmt.Fprintf(os.Stderr, "  %s\n", e)
		}
		return review.Review{}, usage, Errorf(5, "LLM output failed schema validation after repair")
	}
	return rev2, usage, nil
}

// needsSources reports whether any error concerns evidence, in which
// case the model must see the source text to choose a correct citation.
func needsSources(errs []schema.ValidationError) bool {
	for _, e := range errs {
		if strings.Contains(e.Path, "evidence") {
			return true
		}
	}
	return false
}

// itemAt returns the item of the given kind at index i as a value to
// marshal, or false when the index is out of range.
func itemAt(rev review.Review, kind string, i int) (any, bool) {
	switch kind {
	case "issues":
		if i < len(rev.Issues) {
			return rev.Issues[i], true
		}
	case "questions":
		if i < len(rev.Questions) {
			return rev.Questions[i], true
		}
	case "patches":
		if i < len(rev.Patches) {
			return rev.Patches[i], true
		}
	case "coverage.requirements":
		if rev.Coverage != nil && i < len(rev.Coverage.Requirements) {
			return rev.Coverage.Requirements[i], true
		}
	case "coverage.out_of_scope":
		if rev.Coverage != nil && i < len(rev.Coverage.OutOfScope) {
			return rev.Coverage.OutOfScope[i], true
		}
	}
	return nil, false
}

func collectRepairItems(rev review.Review, byKind map[string][]int) ([]prompt.RepairItem, error) {
	var items []prompt.RepairItem
	for _, kind := range schema.RepairKinds {
		for _, i := range byKind[kind] {
			v, ok := itemAt(rev, kind, i)
			if !ok {
				return nil, fmt.Errorf("%s[%d] out of range", kind, i)
			}
			data, err := json.Marshal(v)
			if err != nil {
				return nil, err
			}
			items = append(items, prompt.RepairItem{Kind: kind, Index: i, JSON: string(data)})
		}
	}
	return items, nil
}

// mergeRepaired splices the corrected items from fix into a copy of orig
// at the offending indices, kind by kind and in order. fix must contain
// at least as many items of each kind as were resent; extras are ignored.
func mergeRepaired(orig, fix review.Review, byKind map[string][]int) (review.Review, error) {
	merged := orig
	merged.Issues = append([]review.Issue(nil), orig.Issues...)
	merged.Questions = append([]review.Question(nil), orig.Questions...)
	merged.Patches = append([]review.Patch(nil), orig.Patches...)
	if orig.Coverage != nil {
		cov := *orig.Coverage
		cov.Requirements = append([]review.Requirement(nil), orig.Coverage.Requirements...)
		cov.OutOfScope = append([]review.ScopeItem(nil), orig.Coverage.OutOfScope...)
		merged.Coverage = &cov
	}

	short := func(kind string, got, want int) error {
		return fmt.Errorf("repair returned %d %s item(s), expected %d", got, kind, want)
	}
	for _, kind := range schema.RepairKinds {
		idx := byKind[kind]
		if len(idx) == 0 {
			continue
		}
		switch kind {
		case "issues":
			if len(fix.Issues) < len(idx) {
				return review.Review{}, short(kind, len(fix.Issues), len(idx))
			}
			for n, i := range idx {
				merged.Issues[i] = fix.Issues[n]
			}
		case "questions":
			if len(fix.Questions) < len(idx) {
				return review.Review{}, short(kind, len(fix.Questions), len(idx))
			}
			for n, i := range idx {
				merged.Questions[i] = fix.Questions[n]
			}
		case "patches":
			if len(fix.Patches) < len(idx) {
				return review.Review{}, short(kind, len(fix.Patches), len(idx))
			}
			for n, i := range idx {
				merged.Patches[i] = fix.Patches[n]
			}
		case "coverage.requirements":
			if fix.Coverage == nil || len(fix.Coverage.Requirements) < len(idx) {
				got := 0
				if fix.Coverage != nil {
					got = len(fix.Coverage.Requirements)
				}
				return review.Review{}, short(kind, got, len(idx))
			}
			for n, i := range idx {
				merged.Coverage.Requirements[i] = fix.Coverage.Requirements[n]
			}
		case "coverage.out_of_scope":
			if fix.Coverage == nil || len(fix.Coverage.OutOfScope) < len(idx) {
				got := 0
				if fix.Coverage != nil {
					got = len(fix.Coverage.OutOfScope)
				}
				return review.Review{}, short(kind, got, len(idx))
			}
			for n, i := range idx {
				merged.Coverage.OutOfScope[i] = fix.Coverage.OutOfScope[n]
			}
		}
	}
	return merged, nil
}
