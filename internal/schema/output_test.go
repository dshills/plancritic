package schema

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/dshills/plancritic/internal/review"
)

// walkStrict asserts every object node is strict-mode compatible: it
// declares additionalProperties:false and lists every property as
// required. Provider strict modes reject schemas that do not.
func walkStrict(t *testing.T, path string, node any) {
	t.Helper()
	m, ok := node.(map[string]any)
	if !ok {
		return
	}
	if m["type"] == "object" {
		if ap, ok := m["additionalProperties"].(bool); !ok || ap {
			t.Errorf("%s: object must set additionalProperties:false", path)
		}
		props, _ := m["properties"].(map[string]any)
		req, _ := m["required"].([]string)
		if len(req) != len(props) {
			t.Errorf("%s: required has %d entries, properties has %d", path, len(req), len(props))
		}
		for _, r := range req {
			if _, ok := props[r]; !ok {
				t.Errorf("%s: required %q is not a property", path, r)
			}
		}
		for name, child := range props {
			walkStrict(t, path+"."+name, child)
		}
	}
	if items, ok := m["items"]; ok {
		walkStrict(t, path+"[]", items)
	}
	for _, banned := range []string{"minItems", "maxItems", "minimum", "maximum", "minLength", "maxLength", "$ref", "format"} {
		if _, ok := m[banned]; ok {
			t.Errorf("%s: %q is not portable across provider strict modes", path, banned)
		}
	}
}

func TestModelOutputSchemaIsStrictCompatible(t *testing.T) {
	walkStrict(t, "root", ModelOutputSchema())
}

func TestModelOutputSchemaOmitsServerFilledFields(t *testing.T) {
	root := ModelOutputSchema()
	props := root["properties"].(map[string]any)
	for _, absent := range []string{"tool", "version", "input", "meta"} {
		if _, ok := props[absent]; ok {
			t.Errorf("root must not ask the model for %q", absent)
		}
	}
	summary := props["summary"].(map[string]any)["properties"].(map[string]any)
	for _, absent := range []string{"score", "critical_count", "warn_count", "info_count"} {
		if _, ok := summary[absent]; ok {
			t.Errorf("summary must not ask the model for %q", absent)
		}
	}
	issueEv := props["issues"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)["evidence"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	if _, ok := issueEv["quote"]; ok {
		t.Error("evidence must not ask the model for quote; it is reconstructed locally")
	}
}

func TestModelOutputSchemaEnumsMatchReviewTypes(t *testing.T) {
	props := ModelOutputSchema()["properties"].(map[string]any)
	issueProps := props["issues"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)

	cats := issueProps["category"].(map[string]any)["enum"].([]string)
	if len(cats) != 13 {
		t.Errorf("expected 13 categories, got %d", len(cats))
	}
	for _, c := range cats {
		if !review.Category(c).Valid() {
			t.Errorf("category %q in schema is not valid in review", c)
		}
	}
	for _, s := range issueProps["severity"].(map[string]any)["enum"].([]string) {
		if !review.Severity(s).Valid() {
			t.Errorf("severity %q in schema is not valid in review", s)
		}
	}
	verdicts := props["summary"].(map[string]any)["properties"].(map[string]any)["verdict"].(map[string]any)["enum"].([]string)
	for _, v := range verdicts {
		if !review.Verdict(v).Valid() {
			t.Errorf("verdict %q in schema is not valid in review", v)
		}
	}
}

func TestModelOutputSchemaJSONRoundTrips(t *testing.T) {
	raw := ModelOutputSchemaJSON()
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("schema JSON does not parse: %v", err)
	}
	if !strings.Contains(string(raw), `"additionalProperties":false`) {
		t.Error("serialized schema should carry additionalProperties:false")
	}
	if &raw[0] != &ModelOutputSchemaJSON()[0] {
		t.Error("ModelOutputSchemaJSON should serialize once and reuse the result")
	}
}

func ev(src, path string, start, end int) review.Evidence {
	return review.Evidence{Source: src, Path: path, LineStart: start, LineEnd: end}
}

func TestAutoFixRenumbersEmptyAndDuplicateIDs(t *testing.T) {
	r := &review.Review{
		Issues: []review.Issue{
			{ID: "ISSUE-0001", Evidence: []review.Evidence{ev("plan", "p", 1, 1)}},
			{ID: "ISSUE-0001", Evidence: []review.Evidence{ev("plan", "p", 1, 1)}},
			{ID: "", Evidence: []review.Evidence{ev("plan", "p", 1, 1)}},
		},
		Questions: []review.Question{{ID: "Q-0002"}, {ID: "Q-0002"}},
		Patches:   []review.Patch{{ID: ""}},
	}
	fixes := AutoFix(r, 10, nil)
	got := []string{r.Issues[0].ID, r.Issues[1].ID, r.Issues[2].ID, r.Questions[0].ID, r.Questions[1].ID, r.Patches[0].ID}
	want := []string{"ISSUE-0001", "ISSUE-0002", "ISSUE-0003", "Q-0002", "Q-0001", "PATCH-0001"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ids = %v, want %v", got, want)
	}
	if len(fixes) != 4 {
		t.Errorf("expected 4 fix lines, got %d: %v", len(fixes), fixes)
	}
}

func TestAutoFixEvidenceRanges(t *testing.T) {
	ctxCounts := map[string]int{"SPEC.md": 50}
	r := &review.Review{
		Summary: review.Summary{Verdict: review.VerdictExecutable},
		Issues: []review.Issue{{ID: "ISSUE-0001", Severity: review.SeverityWarn, Category: review.CategoryAmbiguity, Title: "t", Description: "d", Evidence: []review.Evidence{
			ev("plan", "plan.md", 0, 3),           // start below 1
			ev("plan", "plan.md", 8, 5),           // inverted
			ev("plan", "plan.md", 9, 99),          // past end, start inside
			ev("plan", "plan.md", 40, 99),         // start past end: leave alone
			ev("context", "docs/SPEC.md", 48, 60), // context clamp via basename
			ev("context", "missing.md", 1, 999),   // unknown context: leave alone
		}}},
	}
	fixes := AutoFix(r, 20, ctxCounts)
	e := r.Issues[0].Evidence
	checks := []struct {
		i          int
		start, end int
	}{
		{0, 1, 3}, {1, 5, 8}, {2, 9, 20}, {3, 40, 99}, {4, 48, 50}, {5, 1, 999},
	}
	for _, c := range checks {
		if e[c.i].LineStart != c.start || e[c.i].LineEnd != c.end {
			t.Errorf("evidence[%d] = %d-%d, want %d-%d", c.i, e[c.i].LineStart, e[c.i].LineEnd, c.start, c.end)
		}
	}
	if len(fixes) != 4 {
		t.Errorf("expected 4 fixes, got %d: %v", len(fixes), fixes)
	}
	// After AutoFix only the genuinely wrong citations should remain.
	remaining := Validate(r, 20, ctxCounts)
	if len(remaining) != 2 {
		t.Errorf("expected 2 remaining validation errors, got %d: %v", len(remaining), remaining)
	}
}

func TestAutoFixNoopOnValidReview(t *testing.T) {
	r := &review.Review{Issues: []review.Issue{{ID: "ISSUE-0001", Evidence: []review.Evidence{ev("plan", "p", 2, 4)}}}}
	if fixes := AutoFix(r, 10, nil); len(fixes) != 0 {
		t.Errorf("expected no fixes, got %v", fixes)
	}
}

func TestOffendingItems(t *testing.T) {
	errs := []ValidationError{
		{Path: "issues[3].evidence[0].line_end", Message: "x"},
		{Path: "issues[1].severity", Message: "x"},
		{Path: "issues[3].id", Message: "x"},
		{Path: "questions[0].evidence", Message: "x"},
		{Path: "patches[2].diff_unified", Message: "x"},
		{Path: "summary.verdict", Message: "x"},
	}
	issues, questions, patches, other := OffendingItems(errs)
	if !reflect.DeepEqual(issues, []int{1, 3}) {
		t.Errorf("issues = %v", issues)
	}
	if !reflect.DeepEqual(questions, []int{0}) {
		t.Errorf("questions = %v", questions)
	}
	if !reflect.DeepEqual(patches, []int{2}) {
		t.Errorf("patches = %v", patches)
	}
	if len(other) != 1 || other[0].Path != "summary.verdict" {
		t.Errorf("other = %v", other)
	}
}

func TestAutoFixDoesNotStealLaterValidIDs(t *testing.T) {
	r := &review.Review{
		Issues: []review.Issue{
			{ID: "", Evidence: []review.Evidence{ev("plan", "p", 1, 1)}},
			{ID: "ISSUE-0001", Evidence: []review.Evidence{ev("plan", "p", 1, 1)}},
			{ID: "ISSUE-0001", Evidence: []review.Evidence{ev("plan", "p", 1, 1)}},
		},
	}
	fixes := AutoFix(r, 10, nil)
	got := []string{r.Issues[0].ID, r.Issues[1].ID, r.Issues[2].ID}
	want := []string{"ISSUE-0002", "ISSUE-0001", "ISSUE-0003"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ids = %v, want %v (the valid ISSUE-0001 must keep its ID)", got, want)
	}
	if len(fixes) != 2 {
		t.Errorf("expected exactly 2 fixes, got %v", fixes)
	}
}
