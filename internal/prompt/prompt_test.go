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
