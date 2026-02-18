package review

import "testing"

func TestCheckGrounding(t *testing.T) {
	r := &Review{
		Issues: []Issue{
			{ID: "I-1", Description: "The codebase uses Redis for caching."},
			{ID: "I-2", Description: "The plan does not specify a rollback strategy."},
			{ID: "I-3", Impact: "Looking at the code, this will break.", Recommendation: "Fix the existing implementation."},
		},
	}

	violations := CheckGrounding(r)
	if len(violations) == 0 {
		t.Fatal("expected grounding violations")
	}

	ids := make(map[string]bool)
	for _, v := range violations {
		ids[v.IssueID] = true
	}
	if !ids["I-1"] {
		t.Error("expected violation for I-1")
	}
	if ids["I-2"] {
		t.Error("I-2 should not have a violation")
	}
	if !ids["I-3"] {
		t.Error("expected violation for I-3")
	}
}

func TestApplyGroundingDowngrades(t *testing.T) {
	r := &Review{
		Issues: []Issue{
			{ID: "I-1", Severity: SeverityCritical, Description: "The codebase uses X."},
			{ID: "I-2", Severity: SeverityWarn, Description: "The existing implementation breaks."},
		},
	}

	violations := CheckGrounding(r)
	ApplyGroundingDowngrades(r, violations)

	// I-1 should be downgraded from CRITICAL to WARN
	if r.Issues[0].Severity != SeverityWarn {
		t.Errorf("I-1 severity should be WARN, got %s", r.Issues[0].Severity)
	}
	if !hasTag(r.Issues[0].Tags, "UNVERIFIED") {
		t.Error("I-1 should have UNVERIFIED tag")
	}

	// I-2 should stay WARN but get UNVERIFIED tag
	if r.Issues[1].Severity != SeverityWarn {
		t.Errorf("I-2 severity should stay WARN, got %s", r.Issues[1].Severity)
	}
	if !hasTag(r.Issues[1].Tags, "UNVERIFIED") {
		t.Error("I-2 should have UNVERIFIED tag")
	}
}

func hasTag(tags []string, target string) bool {
	for _, t := range tags {
		if t == target {
			return true
		}
	}
	return false
}
