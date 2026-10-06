package lint

import (
	"strings"
	"testing"

	"github.com/dshills/plancritic/internal/plan"
	"github.com/dshills/plancritic/internal/profile"
	"github.com/dshills/plancritic/internal/review"
)

func mkPlan(text string) *plan.Plan {
	return &plan.Plan{FilePath: "dir/PLAN.md", Lines: strings.Split(text, "\n")}
}

func byCheck(issues []review.Issue, check string) []review.Issue {
	var out []review.Issue
	for _, iss := range issues {
		if len(iss.Tags) == 2 && iss.Tags[1] == check {
			out = append(out, iss)
		}
	}
	return out
}

func lines(iss review.Issue) []int {
	out := []int{}
	for _, ev := range iss.Evidence {
		out = append(out, ev.LineStart)
	}
	return out
}

func TestTriggerPhrasesWholeWordCaseInsensitive(t *testing.T) {
	prof := &profile.Profile{Heuristics: profile.Heuristics{AmbiguityTriggers: []string{"fast", "etc.", "fast"}}}
	p := mkPlan("# Plan\nMake it FAST.\nWe eat breakfast fast\nlists, etc.\nnothing here")
	issues := byCheck(Run(p, prof), "trigger-phrase")
	if len(issues) != 2 {
		t.Fatalf("expected one finding per distinct phrase, got %d: %+v", len(issues), issues)
	}
	fast := issues[0]
	if got := lines(fast); len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Errorf(`"fast" should match L2 and L3 as whole words only (not "breakfast"), got %v`, got)
	}
	if fast.Severity != review.SeverityInfo || fast.Blocking || fast.Tags[0] != "local" || !strings.HasPrefix(fast.ID, "ISSUE-LINT-") {
		t.Errorf("local findings must be INFO, non-blocking, tagged local, with LINT ids: %+v", fast)
	}
	if fast.Evidence[0].Path != "PLAN.md" || fast.Evidence[0].Quote != "Make it FAST." {
		t.Errorf("evidence should cite the plan basename with the line quoted: %+v", fast.Evidence[0])
	}
	if got := lines(issues[1]); len(got) != 1 || got[0] != 4 {
		t.Errorf(`"etc." should match at a line end, got %v`, got)
	}
}

func TestContradictionPairsNeedBothSides(t *testing.T) {
	prof := &profile.Profile{Heuristics: profile.Heuristics{Contradictions: []profile.Contradiction{
		{TriggerA: "dependency-free", TriggerB: "add dependency", Severity: "CRITICAL", Note: "claims vs adds."},
		{TriggerA: "no stored procedures", TriggerB: "stored procedure", Severity: "CRITICAL"},
	}}}
	p := mkPlan("Stay dependency-free.\nWe will add dependency X.\nNo stored procedures.")
	issues := byCheck(Run(p, prof), "contradiction-pair")
	if len(issues) != 1 {
		t.Fatalf("only the pair with both sides present should fire, got %d", len(issues))
	}
	iss := issues[0]
	if iss.Severity != review.SeverityInfo || iss.Category != review.CategoryContradiction {
		t.Errorf("local contradictions are INFO candidates: %+v", iss)
	}
	if !strings.Contains(iss.Description, "CRITICAL") || !strings.Contains(iss.Description, "claims vs adds.") {
		t.Errorf("description should carry the profile's severity and note: %q", iss.Description)
	}
	if got := lines(iss); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("evidence should cite both sides, got %v", got)
	}
}

func TestPlaceholdersAndEvidenceCap(t *testing.T) {
	var b strings.Builder
	b.WriteString("# Plan\n")
	for i := 0; i < 12; i++ {
		b.WriteString("step TBD\n")
	}
	b.WriteString("FIXME later\ntodo: nothing\nXXX\n???\nokay")
	issues := byCheck(Run(mkPlan(b.String()), nil), "placeholder")
	if len(issues) != 1 {
		t.Fatalf("expected one placeholder finding, got %d", len(issues))
	}
	iss := issues[0]
	if len(iss.Evidence) != maxEvidence || !strings.Contains(iss.Description, "16 occurrences") {
		t.Errorf("evidence should be capped with the total in the description: %d cited, %q", len(iss.Evidence), iss.Description)
	}
}

func TestStructureChecks(t *testing.T) {
	text := strings.Join([]string{
		"# Plan",
		"## Phase 1: Setup",
		"Do things. Acceptance criteria: it builds.",
		"## Phase 2: Build",
		"Depends on Phase 7 finishing.",
		"### Details",
		"",
		"## Phase 3: Ship",
		"Done when deployed.",
		"## Overview",
		"",
		"## overview",
		"text",
		"## Notes",
		"### Sub",
		"sub text",
	}, "\n")
	issues := Run(mkPlan(text), nil)

	empty := byCheck(issues, "empty-section")
	if len(empty) != 1 || len(empty[0].Evidence) != 2 || empty[0].Evidence[0].LineStart != 6 || empty[0].Evidence[1].LineStart != 10 {
		t.Errorf("empty sections should be 'Details' (L6) and the first 'Overview' (L10); 'Notes' has a subsection: %+v", empty)
	}
	dup := byCheck(issues, "duplicate-heading")
	if len(dup) != 1 || !strings.Contains(dup[0].Title, `"overview"`) || len(dup[0].Evidence) != 2 {
		t.Errorf("duplicate heading should be reported once with both lines: %+v", dup)
	}
	undef := byCheck(issues, "undefined-phase")
	if len(undef) != 1 || !strings.Contains(undef[0].Title, "Phase 7") || undef[0].Evidence[0].LineStart != 5 {
		t.Errorf("Phase 7 is referenced but never defined: %+v", undef)
	}
	crit := byCheck(issues, "phase-without-criteria")
	if len(crit) != 1 || len(crit[0].Evidence) != 1 || crit[0].Evidence[0].LineStart != 4 {
		t.Errorf("only Phase 2 lacks acceptance criteria: %+v", crit)
	}
}

func TestNoPhasesNoPhaseChecks(t *testing.T) {
	issues := Run(mkPlan("# Plan\nSee Phase 3 of the other doc.\n## Work\nstuff"), nil)
	if len(byCheck(issues, "undefined-phase")) != 0 || len(byCheck(issues, "phase-without-criteria")) != 0 {
		t.Error("phase checks must not run on plans without phase headings")
	}
}

func TestSinglePhaseStillNeedsCriteria(t *testing.T) {
	issues := Run(mkPlan("# Plan\n## Phase 1\nstuff"), nil)
	if len(byCheck(issues, "phase-without-criteria")) != 1 {
		t.Error("a lone phase without acceptance criteria should still be reported")
	}
	if len(byCheck(Run(mkPlan("# Plan\n## Phase 1\nDone when it ships."), nil), "phase-without-criteria")) != 0 {
		t.Error("a lone phase with criteria is fine")
	}
}

func TestCleanPlanHasNoFindings(t *testing.T) {
	prof, err := profile.LoadBuiltin("general")
	if err != nil {
		t.Fatal(err)
	}
	text := "# Plan\n## Phase 1: Setup\nInitialize the module. Acceptance criteria: go build passes.\n## Phase 2: Core\nImplement types. Done when tests pass.\n"
	if issues := Run(mkPlan(text), prof); len(issues) != 0 {
		t.Errorf("a clean plan should produce no findings, got %+v", issues)
	}
}

func TestSummaries(t *testing.T) {
	prof := &profile.Profile{Heuristics: profile.Heuristics{AmbiguityTriggers: []string{"robust"}}}
	issues := Run(mkPlan("robust code\nmore\nrobust tests"), prof)
	got := Summaries(issues)
	if len(got) != 1 || got[0] != `AMBIGUITY: Vague phrase "robust" (L1, L3)` {
		t.Errorf("summary line wrong: %v", got)
	}
}

func TestFencedCodeIsNotStructure(t *testing.T) {
	text := strings.Join([]string{
		"# Plan",
		"## Phase 1: Setup",
		"Acceptance criteria: builds.",
		"```bash",
		"# Phase 9 is not a heading",
		"## Overview",
		"```",
		"## Phase 2: Build",
		"Done when tests pass.",
		"~~~",
		"## Overview",
		"~~~",
		"See Phase 2.",
	}, "\n")
	issues := Run(mkPlan(text), nil)
	if n := len(byCheck(issues, "undefined-phase")); n != 0 {
		t.Errorf("a phase mentioned only inside a code fence must not count as a reference, got %d", n)
	}
	if n := len(byCheck(issues, "duplicate-heading")); n != 0 {
		t.Errorf("headings inside code fences must not count, got %d", n)
	}
	if n := len(byCheck(issues, "empty-section")); n != 0 {
		t.Errorf("fenced pseudo-headings must not create empty sections, got %d", n)
	}
}

func TestDuplicateHeadingsOnlyAmongSiblings(t *testing.T) {
	text := strings.Join([]string{
		"# Plan",
		"## Phase 1: Setup",
		"Acceptance criteria: ok.",
		"### Tests",
		"unit",
		"## Phase 2: Build",
		"Acceptance criteria: ok.",
		"### Tests",
		"integration",
		"### Tests",
		"again",
	}, "\n")
	dup := byCheck(Run(mkPlan(text), nil), "duplicate-heading")
	if len(dup) != 1 || len(dup[0].Evidence) != 2 || dup[0].Evidence[0].LineStart != 8 || dup[0].Evidence[1].LineStart != 10 {
		t.Errorf("only the two 'Tests' under Phase 2 are duplicates: %+v", dup)
	}
}

func TestUnsuperseded(t *testing.T) {
	ev := func(n int) []review.Evidence {
		return []review.Evidence{{Source: "plan", Path: "PLAN.md", LineStart: n, LineEnd: n}}
	}
	local := []review.Issue{
		{ID: "ISSUE-LINT-0001", Category: review.CategoryAmbiguity, Evidence: ev(5)},
		{ID: "ISSUE-LINT-0002", Category: review.CategoryContradiction, Evidence: ev(9)},
		{ID: "ISSUE-LINT-0003", Category: review.CategoryAmbiguity, Evidence: ev(20)},
	}
	model := []review.Issue{
		{ID: "ISSUE-0001", Category: review.CategoryAmbiguity, Evidence: ev(5)},  // confirms LINT-0001
		{ID: "ISSUE-0002", Category: review.CategoryTestGap, Evidence: ev(9)},    // different category: no
		{ID: "ISSUE-0003", Category: review.CategoryAmbiguity, Evidence: ev(21)}, // adjacent, no overlap
	}
	got := Unsuperseded(local, model, nil)
	if len(got) != 2 || got[0].ID != "ISSUE-LINT-0002" || got[1].ID != "ISSUE-LINT-0003" {
		t.Errorf("only the confirmed candidate should drop out, got %+v", got)
	}
}

func TestUnsupersededPerOccurrence(t *testing.T) {
	var b strings.Builder
	b.WriteString("# Plan\n")
	for i := 0; i < 12; i++ { // L2-L13
		b.WriteString("step TBD\n")
	}
	p := mkPlan(strings.TrimSuffix(b.String(), "\n"))
	issues := byCheck(Run(p, nil), "placeholder")
	if len(issues) != 1 || len(issues[0].Occurrences) != 12 || len(issues[0].Evidence) != maxEvidence {
		t.Fatalf("setup: expected 12 occurrences, 10 cited: %+v", issues)
	}
	confirm := func(lines ...int) []review.Issue {
		var out []review.Issue
		for _, n := range lines {
			out = append(out, review.Issue{Category: review.CategoryAmbiguity, Evidence: []review.Evidence{{Source: "plan", LineStart: n, LineEnd: n}}})
		}
		return out
	}

	// Confirming the ten cited lines leaves the two uncited ones, now cited.
	got := Unsuperseded(issues, confirm(2, 3, 4, 5, 6, 7, 8, 9, 10, 11), p.Lines)
	if len(got) != 1 || len(got[0].Evidence) != 2 || got[0].Evidence[0].LineStart != 12 || got[0].Evidence[1].LineStart != 13 {
		t.Errorf("uncited occurrences beyond the cap must survive and become the evidence: %+v", got)
	}
	if strings.Contains(got[0].Description, "occurrences") {
		t.Errorf("with two left there is no cap note: %q", got[0].Description)
	}

	// Confirming every occurrence removes the finding, cap or not.
	all := confirm(2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13)
	if got := Unsuperseded(issues, all, p.Lines); len(got) != 0 {
		t.Errorf("a fully confirmed finding must be removed: %+v", got)
	}

	// Exactly ten occurrences, all confirmed: removed.
	ten := byCheck(Run(mkPlan("# Plan\n"+strings.Repeat("x TBD\n", 9)+"x TBD"), nil), "placeholder")
	if got := Unsuperseded(ten, confirm(2, 3, 4, 5, 6, 7, 8, 9, 10, 11), nil); len(got) != 0 {
		t.Errorf("ten fully confirmed occurrences must be removed: %+v", got)
	}
}

func TestFencedCriteriaDoNotCount(t *testing.T) {
	text := "# Plan\n## Phase 1\nWork.\n```go\nacceptance := false\n```"
	if len(byCheck(Run(mkPlan(text), nil), "phase-without-criteria")) != 1 {
		t.Error("a code example mentioning 'acceptance' is not an acceptance criterion")
	}
}

func TestFenceLengthsAndTrailingText(t *testing.T) {
	text := strings.Join([]string{
		"# Plan",
		"## Phase 1: A",
		"Acceptance criteria: ok.",
		"````markdown",
		"```bash",
		"## Overview",
		"```",
		"## Overview",
		"````",
		"## Phase 2: B",
		"Done when shipped.",
		"``` not a closer",
		"## Overview",
		"```",
	}, "\n")
	issues := Run(mkPlan(text), nil)
	if n := len(byCheck(issues, "duplicate-heading")); n != 0 {
		t.Errorf("headings inside a four-backtick fence (even after an inner three-backtick fence) and after a fence with trailing text must not count, got %d", n)
	}
}

func TestContradictionEvidenceKeepsBothSides(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 12; i++ {
		b.WriteString("stay dependency-free\n")
	}
	b.WriteString("we will add dependency X")
	prof := &profile.Profile{Heuristics: profile.Heuristics{Contradictions: []profile.Contradiction{{TriggerA: "dependency-free", TriggerB: "add dependency", Severity: "CRITICAL"}}}}
	issues := byCheck(Run(mkPlan(b.String()), prof), "contradiction-pair")
	if len(issues) != 1 {
		t.Fatalf("expected one finding, got %d", len(issues))
	}
	got := lines(issues[0])
	if len(got) != maxEvidence || got[len(got)-1] != 13 {
		t.Errorf("the single trigger-B line (L13) must survive the evidence cap, got %v", got)
	}
	if !strings.Contains(issues[0].Description, "13 occurrences") {
		t.Errorf("total should count the union: %q", issues[0].Description)
	}
}

func TestCriteriaMarkersAreWholeWords(t *testing.T) {
	text := strings.Join([]string{
		"# Plan",
		"## Phase 1: A",
		"Acceptance criteria: ok.",
		"## Phase 2: B",
		"Implement the unacceptable-input handler using a multicriteria ranking.",
	}, "\n")
	crit := byCheck(Run(mkPlan(text), nil), "phase-without-criteria")
	if len(crit) != 1 || crit[0].Evidence[0].LineStart != 4 {
		t.Errorf("'unacceptable' and 'multicriteria' must not count as acceptance criteria: %+v", crit)
	}
}

func TestIndentedHeadingsAreHeadings(t *testing.T) {
	text := "# Plan\n   ## Phase 1: A\nAcceptance criteria: ok.\n  ## Phase 2: B\nwork\n    ## not a heading (code indent)"
	crit := byCheck(Run(mkPlan(text), nil), "phase-without-criteria")
	if len(crit) != 1 || crit[0].Evidence[0].LineStart != 4 {
		t.Errorf("headings indented up to three spaces count; Phase 2 lacks criteria: %+v", crit)
	}
}

func TestContradictionConfirmedOnEitherSideIsRemoved(t *testing.T) {
	prof := &profile.Profile{Heuristics: profile.Heuristics{Contradictions: []profile.Contradiction{{TriggerA: "dependency-free", TriggerB: "add dependency", Severity: "CRITICAL"}}}}
	p := mkPlan("Stay dependency-free.\nWe will add dependency X.\nAlso dependency-free here.")
	issues := byCheck(Run(p, prof), "contradiction-pair")
	model := []review.Issue{{Category: review.CategoryContradiction, Evidence: []review.Evidence{{Source: "plan", LineStart: 2, LineEnd: 2}}}}
	if got := Unsuperseded(issues, model, p.Lines); len(got) != 0 {
		t.Errorf("a model contradiction on one side confirms the pair: %+v", got)
	}
}

func TestClosingHashesIgnoredInHeadings(t *testing.T) {
	dup := byCheck(Run(mkPlan("# Plan\n## Tests\na\n## Tests ##\nb"), nil), "duplicate-heading")
	if len(dup) != 1 {
		t.Errorf("'## Tests' and '## Tests ##' are the same heading: %+v", dup)
	}
}
