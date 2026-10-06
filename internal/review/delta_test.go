package review

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func issueAt(id string, cat Category, line int, quote string) Issue {
	return Issue{ID: id, Severity: SeverityWarn, Category: cat, Title: "t " + id,
		Evidence: []Evidence{{Source: "plan", Path: "plan.md", LineStart: line, LineEnd: line, Quote: quote}}}
}

func TestIssueFingerprintStability(t *testing.T) {
	a := issueAt("ISSUE-0001", CategoryAmbiguity, 10, "Handle edge cases")
	b := issueAt("ISSUE-0007", CategoryAmbiguity, 42, "  handle   EDGE cases\n")
	if IssueFingerprint(a) != IssueFingerprint(b) {
		t.Error("same category and cited text must match regardless of ID, line number, case, or whitespace")
	}
	c := issueAt("ISSUE-0001", CategoryTestGap, 10, "Handle edge cases")
	if IssueFingerprint(a) == IssueFingerprint(c) {
		t.Error("different category must differ")
	}
	d := issueAt("ISSUE-0001", CategoryAmbiguity, 10, "Handle all edge cases")
	if IssueFingerprint(a) == IssueFingerprint(d) {
		t.Error("different cited text must differ")
	}
	e := a
	e.Evidence = []Evidence{{Source: "context", Path: "docs/SPEC.md", LineStart: 10, LineEnd: 10, Quote: "Handle edge cases"}}
	if IssueFingerprint(a) == IssueFingerprint(e) {
		t.Error("same text in a different source file must differ")
	}
	f := e
	f.Evidence = []Evidence{{Source: "context", Path: "SPEC.md", LineStart: 10, LineEnd: 10, Quote: "Handle edge cases"}}
	if IssueFingerprint(e) != IssueFingerprint(f) {
		t.Error("context paths compare by basename")
	}

	// Evidence order must not matter, and hashing must not reorder the
	// caller's slice.
	two := a
	two.Evidence = []Evidence{
		{Source: "plan", Path: "plan.md", LineStart: 5, LineEnd: 6, Quote: "first cite"},
		{Source: "context", Path: "SPEC.md", LineStart: 9, LineEnd: 9, Quote: "second cite"},
	}
	reversed := a
	reversed.Evidence = []Evidence{two.Evidence[1], two.Evidence[0]}
	if IssueFingerprint(two) != IssueFingerprint(reversed) {
		t.Error("the same evidence in a different order must produce the same fingerprint")
	}
	if two.Evidence[0].Quote != "first cite" || reversed.Evidence[0].Quote != "second cite" {
		t.Error("fingerprinting must not reorder the evidence slice")
	}
	if len(IssueFingerprint(a)) != fingerprintLen {
		t.Errorf("fingerprint length = %d", len(IssueFingerprint(a)))
	}
	q := Question{Evidence: a.Evidence}
	if QuestionFingerprint(q) == IssueFingerprint(a) {
		t.Error("a question and an issue on the same text must not collide")
	}
}

func TestComputeDelta(t *testing.T) {
	baseline := &Review{
		Tool:    "plancritic",
		Summary: Summary{Score: 60},
		Issues: []Issue{
			issueAt("ISSUE-0001", CategoryContradiction, 5, "dependency-free"),
			issueAt("ISSUE-0002", CategoryTestGap, 20, "write tests"),
		},
		Questions: []Question{{ID: "Q-0001", Severity: SeverityWarn, Question: "which auth?",
			Evidence: []Evidence{{Source: "plan", Path: "plan.md", LineStart: 30, LineEnd: 30, Quote: "auth tbd"}}}},
	}
	AssignFingerprints(baseline)
	current := &Review{
		Tool:    "plancritic",
		Summary: Summary{Score: 81},
		Issues: []Issue{
			issueAt("ISSUE-0001", CategoryTestGap, 25, "write tests"), // persisting, moved and renumbered
			issueAt("ISSUE-0002", CategoryAmbiguity, 40, "robust"),    // new
		},
	}
	AssignFingerprints(current)

	d := ComputeDelta(baseline, current, "/tmp/prev.json")
	if d.BaselineFile != "prev.json" || d.ScoreChange != 21 {
		t.Errorf("header fields wrong: %+v", d)
	}
	ids := func(es []DeltaEntry) []string {
		out := []string{}
		for _, e := range es {
			out = append(out, e.Kind+":"+e.ID)
		}
		return out
	}
	if got := ids(d.Persisting); !reflect.DeepEqual(got, []string{"issue:ISSUE-0001"}) {
		t.Errorf("persisting = %v", got)
	}
	if got := ids(d.New); !reflect.DeepEqual(got, []string{"issue:ISSUE-0002"}) {
		t.Errorf("new = %v", got)
	}
	if got := ids(d.Resolved); !reflect.DeepEqual(got, []string{"issue:ISSUE-0001", "question:Q-0001"}) {
		t.Errorf("resolved = %v (baseline IDs expected)", got)
	}
	if d.Status("issue", "ISSUE-0001") != "persisting" || d.Status("issue", "ISSUE-0002") != "new" {
		t.Error("Status lookup wrong")
	}
	if (*Delta)(nil).Status("issue", "x") != "" {
		t.Error("nil delta should report no status")
	}
	data, _ := json.Marshal(ComputeDelta(current, current, "same.json"))
	if string(data) == "" || !jsonHasArrays(data) {
		t.Errorf("delta arrays must serialize as [] not null: %s", data)
	}
}

func jsonHasArrays(data []byte) bool {
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	for _, k := range []string{"resolved", "new", "persisting"} {
		if _, ok := m[k].([]any); !ok {
			return false
		}
	}
	return true
}

func TestLoadBaselineComputesMissingFingerprints(t *testing.T) {
	r := &Review{Tool: "plancritic", Issues: []Issue{issueAt("ISSUE-0001", CategoryAmbiguity, 1, "x")}}
	data, _ := json.Marshal(r) // no fingerprints in the file
	path := filepath.Join(t.TempDir(), "prev.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadBaseline(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Issues[0].Fingerprint != IssueFingerprint(r.Issues[0]) {
		t.Error("baseline without fingerprints should get them from its quotes")
	}

	bad := filepath.Join(t.TempDir(), "bad.json")
	_ = os.WriteFile(bad, []byte(`{"tool":"other"}`), 0o600)
	if _, err := LoadBaseline(bad); err == nil {
		t.Error("a non-plancritic file should be rejected")
	}
	if _, err := LoadBaseline(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Error("a missing file should be an error")
	}
}

func TestComputeDeltaMatchesCollidingFingerprintsByCount(t *testing.T) {
	passage := []Evidence{{Source: "plan", Path: "plan.md", LineStart: 30, LineEnd: 30, Quote: "auth tbd"}}
	baseline := &Review{Questions: []Question{
		{ID: "Q-0001", Severity: SeverityWarn, Question: "Which provider?", Evidence: passage},
		{ID: "Q-0002", Severity: SeverityWarn, Question: "Which token lifetime?", Evidence: passage},
	}}
	current := &Review{Questions: []Question{
		{ID: "Q-0001", Severity: SeverityWarn, Question: "Which provider?", Evidence: passage},
	}}
	AssignFingerprints(baseline)
	AssignFingerprints(current)
	if baseline.Questions[0].Fingerprint != baseline.Questions[1].Fingerprint {
		t.Fatal("precondition: the two questions should share a fingerprint")
	}
	d := ComputeDelta(baseline, current, "b.json")
	if len(d.Persisting) != 1 || len(d.New) != 0 || len(d.Resolved) != 1 {
		t.Errorf("two baseline questions on one passage, one remaining: want 1 persisting + 1 resolved, got persisting=%d new=%d resolved=%d", len(d.Persisting), len(d.New), len(d.Resolved))
	}
	// And the other direction: one became two.
	d = ComputeDelta(current, baseline, "b.json")
	if len(d.Persisting) != 1 || len(d.New) != 1 || len(d.Resolved) != 0 {
		t.Errorf("one baseline question, two current: want 1 persisting + 1 new, got persisting=%d new=%d resolved=%d", len(d.Persisting), len(d.New), len(d.Resolved))
	}
	if d.Status("question", "Q-0002") != "new" || d.Status("question", "Q-0001") != "persisting" {
		t.Error("Status must distinguish colliding findings by ID")
	}
}

func TestLoadBaselineRejectsUnmatchableFindings(t *testing.T) {
	write := func(r *Review) string {
		data, _ := json.Marshal(r)
		path := filepath.Join(t.TempDir(), "prev.json")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	noQuotes := &Review{Tool: "plancritic", Issues: []Issue{{ID: "ISSUE-0001", Category: CategoryAmbiguity,
		Evidence: []Evidence{{Source: "plan", Path: "plan.md", LineStart: 1, LineEnd: 1}}}}}
	if _, err := LoadBaseline(write(noQuotes)); err == nil || !strings.Contains(err.Error(), "no-quotes") {
		t.Errorf("a baseline with neither fingerprints nor quotes must be rejected with guidance, got %v", err)
	}
	noEvidence := &Review{Tool: "plancritic", Questions: []Question{{ID: "Q-0001"}}}
	if _, err := LoadBaseline(write(noEvidence)); err == nil {
		t.Error("a baseline finding without evidence must be rejected")
	}
	// With fingerprints present, quotes are not needed.
	withFP := &Review{Tool: "plancritic", Issues: []Issue{{ID: "ISSUE-0001", Fingerprint: "abc", Category: CategoryAmbiguity}}}
	if _, err := LoadBaseline(write(withFP)); err != nil {
		t.Errorf("fingerprinted baseline without quotes should load: %v", err)
	}
}
