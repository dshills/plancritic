package review

import (
	"testing"
)

// --- Enum validation tests ---

func TestVerdictValid(t *testing.T) {
	valid := []Verdict{VerdictExecutable, VerdictWithClarifications, VerdictNotExecutable}
	for _, v := range valid {
		if !v.Valid() {
			t.Errorf("expected %q to be valid", v)
		}
	}
	if Verdict("INVALID").Valid() {
		t.Error("expected INVALID verdict to be invalid")
	}
}

func TestSeverityValid(t *testing.T) {
	valid := []Severity{SeverityInfo, SeverityWarn, SeverityCritical}
	for _, s := range valid {
		if !s.Valid() {
			t.Errorf("expected %q to be valid", s)
		}
	}
	if Severity("HIGH").Valid() {
		t.Error("expected HIGH severity to be invalid")
	}
}

func TestCategoryValid(t *testing.T) {
	valid := []Category{
		CategoryContradiction, CategoryAmbiguity, CategoryMissingPrerequisite,
		CategoryMissingAcceptanceCriteria, CategoryRiskSecurity, CategoryRiskData,
		CategoryRiskOperations, CategoryTestGap, CategoryScopeCreepRisk,
		CategoryUnrealisticStep, CategoryOrderingDependency,
		CategoryUnspecifiedInterface, CategoryNonDeterminism,
	}
	for _, c := range valid {
		if !c.Valid() {
			t.Errorf("expected %q to be valid", c)
		}
	}
	if Category("UNKNOWN").Valid() {
		t.Error("expected UNKNOWN category to be invalid")
	}
}

func TestPatchTypeValid(t *testing.T) {
	if !PatchTypePlanTextEdit.Valid() {
		t.Error("expected PLAN_TEXT_EDIT to be valid")
	}
	if PatchType("OTHER").Valid() {
		t.Error("expected OTHER patch type to be invalid")
	}
}

func TestCheckStatusValid(t *testing.T) {
	for _, cs := range []CheckStatus{CheckStatusPass, CheckStatusFail, CheckStatusNA} {
		if !cs.Valid() {
			t.Errorf("expected %q to be valid", cs)
		}
	}
	if CheckStatus("MAYBE").Valid() {
		t.Error("expected MAYBE to be invalid")
	}
}

// --- Score tests ---

func TestComputeScore(t *testing.T) {
	tests := []struct {
		name   string
		issues []Issue
		want   int
	}{
		{"empty", nil, 100},
		{"one critical", []Issue{{Severity: SeverityCritical}}, 80},
		{"one warn", []Issue{{Severity: SeverityWarn}}, 93},
		{"one info", []Issue{{Severity: SeverityInfo}}, 98},
		{"mixed", []Issue{
			{Severity: SeverityCritical},
			{Severity: SeverityCritical},
			{Severity: SeverityWarn},
			{Severity: SeverityInfo},
		}, 51},
		{"clamp at zero", []Issue{
			{Severity: SeverityCritical}, {Severity: SeverityCritical},
			{Severity: SeverityCritical}, {Severity: SeverityCritical},
			{Severity: SeverityCritical}, {Severity: SeverityCritical},
		}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeScore(tt.issues)
			if got != tt.want {
				t.Errorf("ComputeScore() = %d, want %d", got, tt.want)
			}
		})
	}
}

// --- Sort tests ---

func TestSortIssues(t *testing.T) {
	issues := []Issue{
		{ID: "1", Severity: SeverityInfo, Evidence: []Evidence{{LineStart: 10}}},
		{ID: "2", Severity: SeverityCritical, Evidence: []Evidence{{LineStart: 50}}},
		{ID: "3", Severity: SeverityWarn, Evidence: []Evidence{{LineStart: 5}}},
		{ID: "4", Severity: SeverityCritical, Evidence: []Evidence{{LineStart: 20}}},
		{ID: "5", Severity: SeverityWarn, Evidence: []Evidence{}},
	}

	SortIssues(issues)

	expected := []string{"4", "2", "5", "3", "1"}
	for i, id := range expected {
		if issues[i].ID != id {
			t.Errorf("position %d: got ID %s, want %s", i, issues[i].ID, id)
		}
	}
}

func TestSortQuestions(t *testing.T) {
	questions := []Question{
		{ID: "Q1", Severity: SeverityInfo, Evidence: []Evidence{{LineStart: 10}}},
		{ID: "Q2", Severity: SeverityCritical, Evidence: []Evidence{{LineStart: 5}}},
		{ID: "Q3", Severity: SeverityWarn, Evidence: []Evidence{{LineStart: 1}}},
	}

	SortQuestions(questions)

	expected := []string{"Q2", "Q3", "Q1"}
	for i, id := range expected {
		if questions[i].ID != id {
			t.Errorf("position %d: got ID %s, want %s", i, questions[i].ID, id)
		}
	}
}

// --- Summary tests ---

func TestComputeSummary(t *testing.T) {
	tests := []struct {
		name    string
		issues  []Issue
		verdict Verdict
	}{
		{"no issues", nil, VerdictExecutable},
		{"warn only", []Issue{{Severity: SeverityWarn}}, VerdictWithClarifications},
		{"critical non-blocking", []Issue{{Severity: SeverityCritical, Blocking: false}}, VerdictWithClarifications},
		{"critical blocking", []Issue{{Severity: SeverityCritical, Blocking: true}}, VerdictNotExecutable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := ComputeSummary(tt.issues)
			if s.Verdict != tt.verdict {
				t.Errorf("verdict = %s, want %s", s.Verdict, tt.verdict)
			}
		})
	}
}

// --- Truncate tests ---

func TestTruncate(t *testing.T) {
	// Under limits — no truncation
	r := &Review{
		Issues:    make([]Issue, 5),
		Questions: make([]Question, 3),
	}
	Truncate(r, 50, 20)
	if len(r.Issues) != 5 {
		t.Errorf("expected 5 issues, got %d", len(r.Issues))
	}

	// Over limits
	r2 := &Review{
		Issues:    make([]Issue, 55),
		Questions: make([]Question, 25),
	}
	Truncate(r2, 50, 20)
	// 50 original + 1 truncation warning
	if len(r2.Issues) != 51 {
		t.Errorf("expected 51 issues after truncation, got %d", len(r2.Issues))
	}
	if len(r2.Questions) != 20 {
		t.Errorf("expected 20 questions after truncation, got %d", len(r2.Questions))
	}
	last := r2.Issues[len(r2.Issues)-1]
	if last.ID != "ISSUE-TRUNC" {
		t.Errorf("expected truncation issue, got ID %s", last.ID)
	}
}
