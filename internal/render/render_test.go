package render

import (
	"strings"
	"testing"

	"github.com/dshills/plancritic/internal/review"
)

func sampleReview() *review.Review {
	return &review.Review{
		Tool:    "plancritic",
		Version: "1.0",
		Summary: review.Summary{
			Verdict:       review.VerdictWithClarifications,
			Score:         73,
			CriticalCount: 1,
			WarnCount:     1,
			InfoCount:     1,
		},
		Issues: []review.Issue{
			{
				ID: "ISSUE-0001", Severity: review.SeverityCritical,
				Category: review.CategoryContradiction,
				Title: "Dependency contradiction", Description: "Plan contradicts itself.",
				Evidence: []review.Evidence{
					{Source: "plan", Path: "plan.md", LineStart: 5, LineEnd: 7, Quote: "no deps"},
				},
				Impact: "Build will fail", Recommendation: "Remove contradiction",
			},
			{
				ID: "ISSUE-0002", Severity: review.SeverityWarn,
				Category: review.CategoryAmbiguity,
				Title: "Vague performance", Description: "No latency target.",
				Evidence: []review.Evidence{
					{Source: "plan", Path: "plan.md", LineStart: 20, LineEnd: 22, Quote: "make it fast"},
				},
				Impact: "Cannot verify", Recommendation: "Add SLO",
			},
			{
				ID: "ISSUE-0003", Severity: review.SeverityInfo,
				Category: review.CategoryTestGap,
				Title: "Missing edge case", Description: "No empty input test.",
				Evidence: []review.Evidence{
					{Source: "plan", Path: "plan.md", LineStart: 30, LineEnd: 30, Quote: "test it"},
				},
				Impact: "Minor", Recommendation: "Add test",
			},
		},
		Questions: []review.Question{
			{
				ID: "Q-0001", Severity: review.SeverityWarn,
				Question: "What is the target DB?", WhyNeeded: "Migration depends on it.",
				Evidence: []review.Evidence{
					{Source: "plan", Path: "plan.md", LineStart: 10, LineEnd: 12, Quote: "use a database"},
				},
				SuggestedAnswers: []string{"PostgreSQL", "MySQL"},
			},
		},
		Patches: []review.Patch{
			{ID: "PATCH-0001", Type: review.PatchTypePlanTextEdit, Title: "Add SLO", DiffUnified: "--- plan.md\n+++ plan.md\n@@ -20 +20 @@\n-make it fast\n+target p95 < 200ms"},
		},
		Input: review.Input{
			ContextFiles: []review.ContextFile{
				{Path: "constraints.md", Hash: "sha256:abc"},
			},
		},
	}
}

func TestMarkdown(t *testing.T) {
	md := Markdown(sampleReview())

	checks := []string{
		"# PlanCritic Review",
		"**Verdict:** EXECUTABLE_WITH_CLARIFICATIONS",
		"**Score:** 73",
		"## Critical Issues",
		"Dependency contradiction",
		"## Warnings",
		"Vague performance",
		"## Info",
		"Missing edge case",
		"## Questions",
		"What is the target DB?",
		"## Suggested Patches",
		"```diff",
		"## Context Used",
		"constraints.md",
	}
	for _, want := range checks {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q", want)
		}
	}
}

func TestMarkdownEmpty(t *testing.T) {
	r := &review.Review{
		Summary: review.Summary{Verdict: review.VerdictExecutable, Score: 100},
	}
	md := Markdown(r)
	if !strings.Contains(md, "No issues found") {
		t.Error("expected 'No issues found' for empty review")
	}
}
