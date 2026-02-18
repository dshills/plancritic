package internal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	pctx "github.com/dshills/plancritic/internal/context"
	"github.com/dshills/plancritic/internal/plan"
	"github.com/dshills/plancritic/internal/review"
	"github.com/dshills/plancritic/internal/schema"
)

func projectRoot() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filename))
}

func TestGoldenSimpleReview(t *testing.T) {
	root := projectRoot()

	// Load the golden JSON (simulated LLM response)
	goldenPath := filepath.Join(root, "testdata", "golden", "simple-review.json")
	goldenData, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("failed to read golden file: %v", err)
	}

	// Parse as review
	var rev review.Review
	if err := json.Unmarshal(goldenData, &rev); err != nil {
		t.Fatalf("failed to parse golden JSON: %v", err)
	}

	// Load plan to get line count
	planPath := filepath.Join(root, "testdata", "plans", "simple.md")
	p, err := plan.Load(planPath)
	if err != nil {
		t.Fatalf("failed to load plan: %v", err)
	}

	// Verify context file loads correctly
	ctxPath := filepath.Join(root, "testdata", "contexts", "constraints.md")
	ctx, err := pctx.Load(ctxPath)
	if err != nil {
		t.Fatalf("failed to load context: %v", err)
	}
	if len(ctx.Lines) == 0 {
		t.Error("context file has no lines")
	}

	// Validate schema
	validationErrs := schema.Validate(&rev, len(p.Lines))
	for _, e := range validationErrs {
		t.Errorf("validation error: %s", e)
	}

	// Verify deterministic scoring
	expectedScore := review.ComputeScore(rev.Issues)
	if rev.Summary.Score != expectedScore {
		t.Errorf("score mismatch: got %d, want %d", rev.Summary.Score, expectedScore)
	}

	// Verify sorting: CRITICAL issues come first
	if len(rev.Issues) > 1 {
		for i := 1; i < len(rev.Issues); i++ {
			prevOrder := severityOrder(rev.Issues[i-1].Severity)
			currOrder := severityOrder(rev.Issues[i].Severity)
			if prevOrder > currOrder {
				t.Errorf("issues not sorted by severity: %s at [%d] before %s at [%d]",
					rev.Issues[i-1].Severity, i-1, rev.Issues[i].Severity, i)
			}
		}
	}

	// Verify expected issue IDs exist
	expectedIDs := []string{"ISSUE-0001", "ISSUE-0002", "ISSUE-0003"}
	issueIDs := make(map[string]bool)
	for _, iss := range rev.Issues {
		issueIDs[iss.ID] = true
	}
	for _, id := range expectedIDs {
		if !issueIDs[id] {
			t.Errorf("missing expected issue ID: %s", id)
		}
	}

	// Verify verdict
	if rev.Summary.Verdict != review.VerdictNotExecutable {
		t.Errorf("expected NOT_EXECUTABLE verdict, got %s", rev.Summary.Verdict)
	}

	// Verify post-processing pipeline
	review.SortIssues(rev.Issues)
	review.SortQuestions(rev.Questions)
	review.Truncate(&rev, review.DefaultMaxIssues, review.DefaultMaxQuestions)
	summary := review.ComputeSummary(rev.Issues)
	if summary.Score != rev.Summary.Score {
		t.Errorf("recomputed score differs: %d vs %d", summary.Score, rev.Summary.Score)
	}

	// Verify JSON round-trip stability
	data1, err := json.MarshalIndent(rev, "", "  ")
	if err != nil {
		t.Fatalf("first marshal failed: %v", err)
	}
	var rev2 review.Review
	if err := json.Unmarshal(data1, &rev2); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	data2, err := json.MarshalIndent(rev2, "", "  ")
	if err != nil {
		t.Fatalf("second marshal failed: %v", err)
	}
	if string(data1) != string(data2) {
		t.Error("JSON round-trip produced different output")
	}
}

func severityOrder(s review.Severity) int {
	switch s {
	case review.SeverityCritical:
		return 0
	case review.SeverityWarn:
		return 1
	default:
		return 2
	}
}
