package render

import (
	"strings"
	"testing"

	"github.com/dshills/plancritic/internal/review"
)

func compactFixture() *review.Review {
	return &review.Review{
		Input:   review.Input{PlanFile: "PLAN.md"},
		Summary: review.Summary{Verdict: review.VerdictNotExecutable, Score: 61, CriticalCount: 1, WarnCount: 1, InfoCount: 0},
		Issues: []review.Issue{
			{
				ID: "ISSUE-0001", Severity: review.SeverityCritical, Category: review.CategoryContradiction, Blocking: true,
				Title:          "Dependency-free claim\ncontradicts   library use",
				Description:    "long description that should not appear",
				Impact:         "impact that should not appear",
				Recommendation: "Pick one: drop the libraries or\nremove the claim.",
				Evidence: []review.Evidence{
					{Source: "plan", Path: "whatever.md", LineStart: 4, LineEnd: 6, Quote: "quoted text that should not appear"},
					{Source: "context", Path: "SPEC.md", LineStart: 12, LineEnd: 12, Quote: "spec"},
				},
			},
			{
				ID: "ISSUE-0002", Severity: review.SeverityWarn, Category: review.CategoryTestGap,
				Title: "No tests named", Recommendation: "List the tests.",
				Evidence: []review.Evidence{{Source: "plan", Path: "PLAN.md", LineStart: 20, LineEnd: 20}},
				Tags:     []string{"UNVERIFIED", "assumption"},
			},
		},
		Questions: []review.Question{{
			ID: "Q-0001", Severity: review.SeverityWarn, Question: "Which auth provider?", WhyNeeded: "Blocks step 3.",
			Evidence: []review.Evidence{{Source: "plan", Path: "PLAN.md", LineStart: 30, LineEnd: 31}},
		}},
		Patches: []review.Patch{{ID: "PATCH-0001", Title: "Remove the claim", DiffUnified: "--- a\n+++ b\n"}},
		Checklists: []review.Checklist{{
			ID: "TESTING", Title: "Test coverage",
			Checks: []review.CheckItem{
				{Check: "Tests named?", Status: "PASS"},
				{Check: "Tests mapped to criteria?", Status: "FAIL"},
				{Check: "Perf tests?", Status: "N/A"},
			},
		}},
		Meta: review.Meta{Model: "anthropic/claude-sonnet-4-6"},
	}
}

func TestCompactLines(t *testing.T) {
	out := Compact(compactFixture())
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	want := []string{
		`VERDICT NOT_EXECUTABLE score=61 critical=1 warn=1 info=0`,
		`ISSUE-0001 CRITICAL(blocking) CONTRADICTION PLAN.md:L4-6,SPEC.md:L12 "Dependency-free claim contradicts library use" -> Pick one: drop the libraries or remove the claim.`,
		`ISSUE-0002 WARN TEST_GAP PLAN.md:L20 "No tests named" -> List the tests. [UNVERIFIED,assumption]`,
		`Q-0001 WARN PLAN.md:L30-31 "Which auth provider?" -> Blocks step 3.`,
		`PATCH-0001 "Remove the claim" (diff available via --patch-out)`,
		`CHECK TESTING pass=1 fail=1 na=1`,
		`CHECK TESTING FAIL "Tests mapped to criteria?"`,
	}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines, want %d:\n%s", len(lines), len(want), out)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d:\n got %q\nwant %q", i+1, lines[i], want[i])
		}
	}
	for _, absent := range []string{"long description", "impact that", "quoted text", "--- a"} {
		if strings.Contains(out, absent) {
			t.Errorf("compact output should not contain %q", absent)
		}
	}
}

func TestCompactHeaderFlags(t *testing.T) {
	r := &review.Review{Summary: review.Summary{Verdict: review.VerdictExecutable, Score: 100}}
	if got := CompactHeader(r); got != "VERDICT EXECUTABLE_AS_IS score=100 critical=0 warn=0 info=0" {
		t.Errorf("header = %q", got)
	}
	r.Meta.Cached, r.Meta.Truncated = true, true
	if got := CompactHeader(r); !strings.HasSuffix(got, " cached truncated") {
		t.Errorf("header should carry cached and truncated markers, got %q", got)
	}
	if out := Compact(r); strings.Count(out, "\n") != 1 {
		t.Errorf("an empty review should render the header line only, got %q", out)
	}
}

func TestCompactMissingEvidence(t *testing.T) {
	r := &review.Review{Issues: []review.Issue{{ID: "ISSUE-0001", Severity: review.SeverityInfo, Category: review.CategoryAmbiguity, Title: "t"}}}
	if out := Compact(r); !strings.Contains(out, `ISSUE-0001 INFO AMBIGUITY - "t"`) {
		t.Errorf("missing evidence should render as '-', got %q", out)
	}
}

func TestCompactDeltaMarkers(t *testing.T) {
	r := compactFixture()
	review.AssignFingerprints(r)
	r.Delta = &review.Delta{
		BaselineFile: "prev.json", ScoreChange: 9,
		New:        []review.DeltaEntry{{Kind: "issue", ID: "ISSUE-0002"}},
		Persisting: []review.DeltaEntry{{Kind: "issue", ID: "ISSUE-0001"}, {Kind: "question", ID: "Q-0001"}},
		Resolved:   []review.DeltaEntry{{Kind: "issue", ID: "ISSUE-0009", Title: "Gone now"}},
	}
	out := Compact(r)
	for _, want := range []string{
		" new=1 persisting=2 resolved=1 score_change=+9\n",
		`"Dependency-free claim contradicts library use" -> Pick one: drop the libraries or remove the claim. [persisting]`,
		`-> List the tests. [new,UNVERIFIED,assumption]`,
		`"Which auth provider?" -> Blocks step 3. [persisting]`,
		`RESOLVED issue ISSUE-0009 "Gone now"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("compact delta output missing %q:\n%s", want, out)
		}
	}
}
