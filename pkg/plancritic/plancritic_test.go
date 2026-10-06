package plancritic

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func basenames(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, filepath.Base(p))
	}
	return out
}

func TestMaterializeInputsUsesDeterministicNames(t *testing.T) {
	opts := CheckOptions{
		PlanName: "my plan.md",
		PlanText: "# Plan\n",
		ContextDocuments: []ContextDocument{
			{Name: "docs/SPEC.md", Text: "spec one"},
			{Name: "SPEC.md", Text: "spec two"},
			{Name: "", Text: "unnamed"},
			{Name: "notes", Text: "no extension"},
			{Name: "spec.md", Text: "case-insensitive collision"},
		},
	}

	run := func() (string, []string, string) {
		t.Helper()
		planPath, ctxPaths, cleanup, err := materializeInputs(opts)
		if err != nil {
			t.Fatal(err)
		}
		defer cleanup()
		for i, p := range ctxPaths {
			data, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != opts.ContextDocuments[i].Text {
				t.Errorf("context %d content mismatch: %q", i, data)
			}
		}
		return filepath.Base(planPath), basenames(ctxPaths), filepath.Dir(planPath)
	}

	plan1, ctx1, dir1 := run()
	plan2, ctx2, dir2 := run()

	if plan1 != "my plan.md" {
		t.Errorf("plan basename = %q, want the caller's name", plan1)
	}
	wantCtx := []string{"SPEC.md", "SPEC-2.md", "context-3.md", "notes.md", "spec-3.md"}
	if !reflect.DeepEqual(ctx1, wantCtx) {
		t.Errorf("context basenames = %v, want %v", ctx1, wantCtx)
	}
	if plan1 != plan2 || !reflect.DeepEqual(ctx1, ctx2) {
		t.Errorf("basenames must be identical across calls: %v/%v vs %v/%v", plan1, ctx1, plan2, ctx2)
	}
	if dir1 == dir2 {
		t.Error("each call should get its own private temp directory")
	}
	for _, d := range []string{dir1, dir2} {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Errorf("cleanup should remove %s, stat err=%v", d, err)
		}
	}
}

func TestMaterializeInputsPlanAndContextNameCollision(t *testing.T) {
	opts := CheckOptions{
		PlanText:         "# Plan\n",
		ContextDocuments: []ContextDocument{{Name: "PLAN.md", Text: "ctx"}},
	}
	planPath, ctxPaths, cleanup, err := materializeInputs(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if filepath.Base(planPath) != "PLAN.md" {
		t.Errorf("plan should keep the default name, got %q", filepath.Base(planPath))
	}
	if got := basenames(ctxPaths); !reflect.DeepEqual(got, []string{"PLAN-2.md"}) {
		t.Errorf("context should be de-duplicated against the plan name, got %v", got)
	}
}

func TestMaterializeInputsPathOnlyNeedsNoTempDir(t *testing.T) {
	opts := CheckOptions{PlanPath: "/some/plan.md", ContextPaths: []string{"/some/spec.md"}}
	planPath, ctxPaths, cleanup, err := materializeInputs(opts)
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	if planPath != opts.PlanPath || !reflect.DeepEqual(ctxPaths, opts.ContextPaths) {
		t.Errorf("paths should pass through untouched: %q %v", planPath, ctxPaths)
	}
}

func TestMaterializeInputsValidation(t *testing.T) {
	if _, _, _, err := materializeInputs(CheckOptions{}); err == nil {
		t.Error("expected error when neither plan_path nor plan_text is set")
	}
	if _, _, _, err := materializeInputs(CheckOptions{PlanPath: "a.md", PlanText: "x"}); err == nil {
		t.Error("expected error when both plan_path and plan_text are set")
	}
}

func TestSafeBasename(t *testing.T) {
	tests := []struct{ in, fallback, want string }{
		{"PLAN.md", "x.md", "PLAN.md"},
		{"docs/sub/SPEC.md", "x.md", "SPEC.md"},
		{`C:\\work\\SPEC.md`, "x.md", "SPEC.md"},
		{"../../etc/passwd", "x.md", "passwd.md"},
		{"..", "fallback.md", "fallback.md"},
		{"", "fallback.md", "fallback.md"},
		{"  ", "fallback.md", "fallback.md"},
		{"README", "x.md", "README.md"},
	}
	for _, tt := range tests {
		if got := safeBasename(tt.in, tt.fallback); got != tt.want {
			t.Errorf("safeBasename(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
