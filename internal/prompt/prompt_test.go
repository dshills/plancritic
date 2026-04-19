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
		`<plan path="plan.md">`,
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
	if !strings.Contains(text, `<context path="constraints.md">`) {
		t.Error("context block missing from prompt")
	}
}

func TestBuildWithStepIDs(t *testing.T) {
	p := &plan.Plan{FilePath: "plan.md", Lines: []string{"step"}}
	steps := []plan.StepID{{ID: "P-001", LineStart: 1, Text: "First step"}}
	text := Build(BuildOpts{Plan: p, StepIDs: steps})
	if !strings.Contains(text, "P-001") {
		t.Error("step IDs missing from prompt")
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
	if !strings.Contains(segs[1].Text, `<context path="constraints.md">`) {
		t.Error("contexts segment missing context block")
	}
	if !strings.Contains(segs[2].Text, `<plan path="plan.md">`) {
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
