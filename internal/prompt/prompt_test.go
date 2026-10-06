package prompt

import (
	"strings"
	"testing"

	pctx "github.com/dshills/plancritic/internal/context"
	"github.com/dshills/plancritic/internal/plan"
	"github.com/dshills/plancritic/internal/profile"
	"github.com/dshills/plancritic/internal/schema"
)

func TestBuild(t *testing.T) {
	p := &plan.Plan{
		FilePath: "plan.md",
		Lines:    []string{"# Step 1", "Do something"},
	}
	prof, err := profile.LoadBuiltin("general")
	if err != nil {
		t.Fatal(err)
	}

	text := Build(BuildOpts{
		Plan:    p,
		Profile: prof,
	})

	checks := []string{
		"plan critic",
		"ONLY valid JSON",
		`##PLANCRITIC_PLAN_BEGIN path="plan.md"##`,
		"L001:",
		"## Profile: general",
		"Return at most 50 issues",
	}
	for _, want := range checks {
		if !strings.Contains(text, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestBuildStrict(t *testing.T) {
	p := &plan.Plan{FilePath: "plan.md", Lines: []string{"step"}}
	text := Build(BuildOpts{Plan: p, Strict: true})
	if !strings.Contains(text, "Strict Grounding Mode") {
		t.Error("strict mode section missing from prompt")
	}
}

func TestBuildWithContext(t *testing.T) {
	p := &plan.Plan{FilePath: "plan.md", Lines: []string{"step"}}
	ctx := &pctx.File{FilePath: "constraints.md", Lines: []string{"rule one"}}
	text := Build(BuildOpts{Plan: p, Contexts: []*pctx.File{ctx}})
	if !strings.Contains(text, `##PLANCRITIC_CONTEXT_BEGIN path="constraints.md"##`) {
		t.Error("context block missing from prompt")
	}
}

func TestBuildWithStepIDs(t *testing.T) {
	p := &plan.Plan{FilePath: "plan.md", Lines: []string{"step"}}
	steps := []plan.StepID{{ID: "P-001", LineStart: 1, Text: "First step"}}
	text := Build(BuildOpts{Plan: p, StepIDs: steps})
	if !strings.Contains(text, "P-001 L1 First step") {
		t.Errorf("compact step index entry missing from prompt:\n%s", text)
	}
}

func TestBuildStepIndexTruncatesLongTitles(t *testing.T) {
	long := strings.Repeat("word ", 30) // 150 chars
	p := &plan.Plan{FilePath: "plan.md", Lines: []string{long}}
	steps := []plan.StepID{{ID: "P-001", LineStart: 1, Text: long}}
	text := Build(BuildOpts{Plan: p, StepIDs: steps})

	idx := strings.Index(text, "## Plan Step Index")
	if idx == -1 {
		t.Fatal("step index section missing")
	}
	section := text[idx:]
	if strings.Contains(section, long) {
		t.Error("step index repeated the full step text; expected truncation")
	}
	if !strings.Contains(section, "…") {
		t.Error("truncated title should end with an ellipsis")
	}
}

func TestTruncateTitle(t *testing.T) {
	tests := []struct {
		in    string
		limit int
		want  string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"this is longer than ten", 10, "this is lo…"},
		{"trailing space ", 9, "trailing…"},
		{"héllo wörld ünïcode", 5, "héllo…"},
		{"anything", 0, "anything"},
	}
	for _, tt := range tests {
		if got := truncateTitle(tt.in, tt.limit); got != tt.want {
			t.Errorf("truncateTitle(%q, %d) = %q, want %q", tt.in, tt.limit, got, tt.want)
		}
	}
}

func TestBuildSegmentsCacheMarks(t *testing.T) {
	p := &plan.Plan{FilePath: "plan.md", Lines: []string{"step"}}
	ctx := &pctx.File{FilePath: "constraints.md", Lines: []string{"rule"}}
	prof, err := profile.LoadBuiltin("general")
	if err != nil {
		t.Fatal(err)
	}

	segs := BuildSegments(BuildOpts{Plan: p, Contexts: []*pctx.File{ctx}, Profile: prof})
	if len(segs) != 3 {
		t.Fatalf("expected 3 segments (prefix, contexts, tail), got %d", len(segs))
	}
	if !segs[0].CacheMark {
		t.Error("prefix segment should have CacheMark=true")
	}
	if !segs[1].CacheMark {
		t.Error("contexts segment should have CacheMark=true")
	}
	if segs[2].CacheMark {
		t.Error("tail segment (plan) must not be cached — it changes across re-runs")
	}
	if !strings.Contains(segs[0].Text, "## Profile: general") {
		t.Error("prefix segment missing profile content")
	}
	if !strings.Contains(segs[1].Text, `##PLANCRITIC_CONTEXT_BEGIN path="constraints.md"##`) {
		t.Error("contexts segment missing context block")
	}
	if !strings.Contains(segs[2].Text, `##PLANCRITIC_PLAN_BEGIN path="plan.md"##`) {
		t.Error("tail segment missing plan block")
	}
}

func TestBuildSegmentsNoContexts(t *testing.T) {
	p := &plan.Plan{FilePath: "plan.md", Lines: []string{"step"}}
	segs := BuildSegments(BuildOpts{Plan: p})
	if len(segs) != 2 {
		t.Fatalf("expected 2 segments when no contexts provided, got %d", len(segs))
	}
	if !segs[0].CacheMark {
		t.Error("prefix should still be cached when contexts are absent")
	}
	if segs[1].CacheMark {
		t.Error("plan tail must not be cached")
	}
}

func TestBuildMatchesConcatenatedSegments(t *testing.T) {
	p := &plan.Plan{FilePath: "plan.md", Lines: []string{"# step"}}
	ctx := &pctx.File{FilePath: "notes.md", Lines: []string{"note"}}
	opts := BuildOpts{Plan: p, Contexts: []*pctx.File{ctx}, Strict: true}

	s := Build(opts)
	var concat strings.Builder
	for _, seg := range BuildSegments(opts) {
		concat.WriteString(seg.Text)
	}
	if s != concat.String() {
		t.Error("Build() output must equal concatenation of BuildSegments()")
	}
}

func TestBuildOmitsQuoteFromSchemaAndTellsModelNotToEmitIt(t *testing.T) {
	p := &plan.Plan{FilePath: "plan.md", Lines: []string{"step"}}
	text := Build(BuildOpts{Plan: p})

	// Evidence schema line for questions must no longer list "quote": string.
	// Matching the questions evidence line is a proxy for all evidence shapes
	// since they share the same structural description.
	if strings.Contains(text, `"quote": string`) {
		t.Error("prompt schema should no longer request a quote field from the LLM")
	}
	if !strings.Contains(text, "Do NOT emit a \"quote\" field") {
		t.Error("prompt should explicitly tell the model not to emit quote")
	}
}

func TestBuildRepair(t *testing.T) {
	errs := []schema.ValidationError{
		{Path: "issues[0].severity", Message: "invalid: \"HIGH\""},
	}
	text := BuildRepair(`{"broken": true}`, errs)
	if !strings.Contains(text, "issues[0].severity") {
		t.Error("repair prompt missing error path")
	}
	if !strings.Contains(text, `{"broken": true}`) {
		t.Error("repair prompt missing original output")
	}
}

func TestSchemaDefinitionOmitsServerFilledFields(t *testing.T) {
	for _, absent := range []string{"plan_hash", `"score"`, "critical_count", `"tool"`, `"meta"`, `"quote"`} {
		if strings.Contains(schemaDefinition, absent) {
			t.Errorf("prompt schema should not ask the model for %s", absent)
		}
	}
	for _, present := range []string{`"verdict"`, `"issues"`, `"questions"`, `"patches"`, `"checklists"`, `"line_start"`} {
		if !strings.Contains(schemaDefinition, present) {
			t.Errorf("prompt schema should mention %s", present)
		}
	}
}

func TestBuildDeltaRepair(t *testing.T) {
	text := BuildDeltaRepair(DeltaRepairOpts{
		Items: []RepairItem{
			{Kind: "issues", Index: 3, JSON: `{"id":"ISSUE-0004"}`},
			{Kind: "questions", Index: 0, JSON: `{"id":"Q-0001"}`},
		},
		Errors: []schema.ValidationError{
			{Path: "issues[3].evidence[0].line_end", Message: "exceeds plan line count (120)"},
			{Path: "questions[0].evidence", Message: "at least one evidence entry required"},
		},
		PlanName:          "PLAN.md",
		PlanLines:         120,
		ContextLineCounts: map[string]int{"SPEC.md": 466, "ARCH.md": 40},
	})

	for _, want := range []string{
		`"issues" must contain exactly 1 corrected item(s), "questions" exactly 1, "patches" exactly 0`,
		`plan "PLAN.md" lines 1-120; context "ARCH.md" lines 1-40; context "SPEC.md" lines 1-466`,
		"- issues[3].evidence[0].line_end: exceeds plan line count (120)",
		"### issues[3]",
		`{"id":"ISSUE-0004"}`,
		"### questions[0]",
		"## Output JSON Schema",
		"Return ONLY the JSON object",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("delta repair prompt missing %q:\n%s", want, text)
		}
	}
}

func TestBuildDeltaRepairIncludesSourcesOnlyWhenGiven(t *testing.T) {
	base := DeltaRepairOpts{
		Items:     []RepairItem{{Kind: "issues", Index: 0, JSON: `{}`}},
		Errors:    []schema.ValidationError{{Path: "issues[0].evidence", Message: "required"}},
		PlanName:  "PLAN.md",
		PlanLines: 2,
	}
	if strings.Contains(BuildDeltaRepair(base), "## Sources") {
		t.Error("no Sources section expected when none supplied")
	}
	p := &plan.Plan{FilePath: "PLAN.md", Lines: []string{"# Plan", "1. Step"}}
	c := &pctx.File{FilePath: "docs/SPEC.md", Lines: []string{"spec line"}}
	base.Sources = RenderSources(p, []*pctx.File{c})
	text := BuildDeltaRepair(base)
	for _, want := range []string{"## Sources", `##PLANCRITIC_CONTEXT_BEGIN path="SPEC.md"##`, "L001: spec line", `##PLANCRITIC_PLAN_BEGIN path="PLAN.md"##`, "L002: 1. Step"} {
		if !strings.Contains(text, want) {
			t.Errorf("delta repair with sources missing %q", want)
		}
	}
}

func TestRenderSourcesMatchesMainPrompt(t *testing.T) {
	p := &plan.Plan{FilePath: "PLAN.md", Lines: []string{"# Plan"}}
	c := &pctx.File{FilePath: "SPEC.md", Lines: []string{"spec"}}
	main := Build(BuildOpts{Plan: p, Contexts: []*pctx.File{c}})
	for _, block := range []string{RenderContextBlock(c), RenderPlanBlock(p)} {
		if !strings.Contains(main, block) {
			t.Errorf("main prompt should embed the same rendered block:\n%s", block)
		}
	}
}
