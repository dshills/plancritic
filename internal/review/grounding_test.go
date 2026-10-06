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

func TestCheckGroundingQuestions(t *testing.T) {
	r := &Review{
		Questions: []Question{
			{ID: "Q-1", Question: "Does the codebase uses Redis?", WhyNeeded: "Need to know."},
			{ID: "Q-2", Question: "What format?", WhyNeeded: "The existing implementation depends on it."},
			{ID: "Q-3", Question: "What version?", WhyNeeded: "For compatibility."},
		},
	}

	violations := CheckGrounding(r)
	ids := make(map[string]bool)
	for _, v := range violations {
		ids[v.IssueID] = true
	}
	if !ids["Q-1"] {
		t.Error("expected violation for Q-1 (question text)")
	}
	if !ids["Q-2"] {
		t.Error("expected violation for Q-2 (why_needed text)")
	}
	if ids["Q-3"] {
		t.Error("Q-3 should not have a violation")
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

func TestCheckGroundingPhraseInCitedEvidenceIsNotAViolation(t *testing.T) {
	r := &Review{Issues: []Issue{
		{ID: "I-1", Severity: SeverityCritical, Description: "The plan says the existing code uses Cobra but then replaces it.",
			Evidence: []Evidence{{Source: "plan", LineStart: 3, LineEnd: 3, Quote: "Note: The existing\n   code uses Cobra for the CLI."}}},
		{ID: "I-2", Severity: SeverityCritical, Description: "The existing code uses Cobra.",
			Evidence: []Evidence{{Source: "plan", LineStart: 9, LineEnd: 9, Quote: "Add a CLI."}}},
		{ID: "I-3", Description: "The project’s layout is unclear.",
			Evidence: []Evidence{{Source: "context", Path: "SPEC.md", LineStart: 1, LineEnd: 1, Quote: "Describe the project's layout."}}},
	}}
	v := CheckGrounding(r)
	ids := map[string]bool{}
	for _, x := range v {
		ids[x.IssueID] = true
	}
	if ids["I-1"] {
		t.Error("a phrase the finding quotes from its own evidence (across a line break) is grounded")
	}
	if !ids["I-2"] {
		t.Error("the same phrase without supporting evidence is still a violation")
	}
	if ids["I-3"] {
		t.Error("curly and straight apostrophes should match")
	}

	ApplyGroundingDowngrades(r, v)
	if r.Issues[0].Severity != SeverityCritical || len(r.Issues[0].Tags) != 0 {
		t.Errorf("a grounded CRITICAL must not be downgraded or tagged: %+v", r.Issues[0])
	}
	want := []string{"UNVERIFIED", "UNVERIFIED:the existing code"}
	if r.Issues[1].Severity != SeverityWarn || len(r.Issues[1].Tags) != 2 || r.Issues[1].Tags[0] != want[0] || r.Issues[1].Tags[1] != want[1] {
		t.Errorf("violation should downgrade and name the phrase: %+v", r.Issues[1])
	}
}

func TestCheckGroundingWordBoundaries(t *testing.T) {
	r := &Review{Issues: []Issue{
		{ID: "I-1", Description: "Switch the existing codec to Opus."},
		{ID: "I-2", Description: "List the projects affected."},
	}}
	if v := CheckGrounding(r); len(v) != 0 {
		t.Errorf("'existing codec' and 'the projects' are not fabrication phrases: %+v", v)
	}
}

func TestApplyGroundingDowngradesTagsEachPhraseOnce(t *testing.T) {
	r := &Review{Issues: []Issue{{ID: "I-1", Severity: SeverityCritical,
		Description: "The codebase uses X.", Impact: "The codebase uses X widely.", Recommendation: "Looking at the code, change it."}}}
	ApplyGroundingDowngrades(r, CheckGrounding(r))
	got := r.Issues[0].Tags
	want := []string{"UNVERIFIED", "UNVERIFIED:the codebase uses", "UNVERIFIED:looking at the code"}
	if len(got) != len(want) {
		t.Fatalf("tags = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("tags = %v, want %v", got, want)
		}
	}
}

func TestCheckGroundingDoesNotJoinCitations(t *testing.T) {
	r := &Review{Issues: []Issue{{ID: "I-1", Description: "The existing code is fragile.",
		Evidence: []Evidence{{Source: "plan", Quote: "Refactor the existing"}, {Source: "plan", Quote: "code paths later."}}}}}
	if v := CheckGrounding(r); len(v) != 1 {
		t.Errorf("a phrase split across two citations is not grounded: %+v", v)
	}
}
