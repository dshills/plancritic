package schema

import (
	"encoding/json"
	"fmt"
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
	for _, shape := range []OutputShape{{}, {Patches: true}, {Checklists: true}, {Patches: true, Checklists: true}} {
		walkStrict(t, fmt.Sprintf("root%+v", shape), ModelOutputSchema(shape))
	}
}

func TestModelOutputSchemaOmitsServerFilledFields(t *testing.T) {
	root := ModelOutputSchema(OutputShape{})
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
	props := ModelOutputSchema(OutputShape{})["properties"].(map[string]any)
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
	raw := ModelOutputSchemaJSON(OutputShape{})
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("schema JSON does not parse: %v", err)
	}
	if !strings.Contains(string(raw), `"additionalProperties":false`) {
		t.Error("serialized schema should carry additionalProperties:false")
	}
}

func TestModelOutputSchemaShapeGatesOptionalSections(t *testing.T) {
	keys := func(shape OutputShape) map[string]bool {
		root := ModelOutputSchema(shape)
		out := map[string]bool{}
		for k := range root["properties"].(map[string]any) {
			out[k] = true
		}
		req := root["required"].([]string)
		if len(req) != len(out) {
			t.Errorf("shape %+v: required %v does not match properties %v", shape, req, out)
		}
		return out
	}
	if k := keys(OutputShape{}); k["patches"] || k["checklists"] {
		t.Errorf("default shape must not request patches or checklists: %v", k)
	}
	if k := keys(OutputShape{Patches: true}); !k["patches"] || k["checklists"] {
		t.Errorf("patches-only shape wrong: %v", k)
	}
	if k := keys(OutputShape{Checklists: true}); k["patches"] || !k["checklists"] {
		t.Errorf("checklists-only shape wrong: %v", k)
	}
	if k := keys(OutputShape{Patches: true, Checklists: true}); !k["patches"] || !k["checklists"] {
		t.Errorf("full shape wrong: %v", k)
	}
	if string(ModelOutputSchemaJSON(OutputShape{})) == string(ModelOutputSchemaJSON(OutputShape{Patches: true})) {
		t.Error("serialized schemas must differ by shape")
	}
}

func ev(src, path string, start, end int) review.Evidence {
	return review.Evidence{Source: src, Path: path, LineStart: start, LineEnd: end}
}

func TestAutoFixRenumbersEmptyAndDuplicateIDs(t *testing.T) {
	r := &review.Review{
		Summary: review.Summary{Verdict: review.VerdictExecutable},
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
	r := &review.Review{Summary: review.Summary{Verdict: review.VerdictExecutable}, Issues: []review.Issue{{ID: "ISSUE-0001", Evidence: []review.Evidence{ev("plan", "p", 2, 4)}}}}
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
		{Path: "coverage.requirements[4].spec_evidence[0].line_end", Message: "x"},
		{Path: "coverage.out_of_scope[0].plan_step", Message: "x"},
		{Path: "summary.verdict", Message: "x"},
	}
	byKind, other := OffendingItems(errs)
	if !reflect.DeepEqual(byKind["issues"], []int{1, 3}) {
		t.Errorf("issues = %v", byKind["issues"])
	}
	if !reflect.DeepEqual(byKind["questions"], []int{0}) {
		t.Errorf("questions = %v", byKind["questions"])
	}
	if !reflect.DeepEqual(byKind["patches"], []int{2}) {
		t.Errorf("patches = %v", byKind["patches"])
	}
	if !reflect.DeepEqual(byKind["coverage.requirements"], []int{4}) || !reflect.DeepEqual(byKind["coverage.out_of_scope"], []int{0}) {
		t.Errorf("coverage kinds = %v", byKind)
	}
	if len(other) != 1 || other[0].Path != "summary.verdict" {
		t.Errorf("other = %v", other)
	}
}

func TestAutoFixDoesNotStealLaterValidIDs(t *testing.T) {
	r := &review.Review{
		Summary: review.Summary{Verdict: review.VerdictExecutable},
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

func TestAutoFixFillsMissingVerdict(t *testing.T) {
	r := &review.Review{Issues: []review.Issue{{ID: "ISSUE-0001", Severity: review.SeverityWarn, Category: review.CategoryAmbiguity, Title: "t", Description: "d", Evidence: []review.Evidence{ev("plan", "p", 1, 1)}}}}
	fixes := AutoFix(r, 10, nil)
	if !r.Summary.Verdict.Valid() {
		t.Errorf("verdict should be filled, got %q", r.Summary.Verdict)
	}
	if len(fixes) != 1 || !strings.Contains(fixes[0], "summary.verdict") {
		t.Errorf("expected one verdict fix, got %v", fixes)
	}
	if errs := Validate(r, 10, nil); len(errs) != 0 {
		t.Errorf("review should validate after AutoFix, got %v", errs)
	}
	r.Summary.Verdict = "MAYBE"
	AutoFix(r, 10, nil)
	if r.Summary.Verdict != review.VerdictWithClarifications {
		t.Errorf("invalid verdict should be replaced, got %q", r.Summary.Verdict)
	}
}

func TestModelOutputSchemaCoverageShape(t *testing.T) {
	root := ModelOutputSchema(OutputShape{Coverage: true})
	walkStrict(t, "coverage-root", root)
	props := root["properties"].(map[string]any)
	cov, ok := props["coverage"].(map[string]any)
	if !ok {
		t.Fatal("coverage shape should add a coverage object")
	}
	cp := cov["properties"].(map[string]any)
	if _, ok := cp["requirements"]; !ok {
		t.Error("coverage.requirements missing")
	}
	if _, ok := cp["out_of_scope"]; !ok {
		t.Error("coverage.out_of_scope missing")
	}
	if _, ok := cp["summary"]; ok {
		t.Error("summary is computed locally and must not be requested from the model")
	}
	if _, ok := ModelOutputSchema(OutputShape{})["properties"].(map[string]any)["coverage"]; ok {
		t.Error("default shape must not request coverage")
	}
}

func TestValidateCoverage(t *testing.T) {
	specEv := func(start, end int) review.Evidence {
		return review.Evidence{Source: "context", Path: "SPEC.md", LineStart: start, LineEnd: end}
	}
	planEvidence := func(start, end int) review.Evidence {
		return review.Evidence{Source: "plan", Path: "plan.md", LineStart: start, LineEnd: end}
	}
	good := &review.Review{
		Summary: review.Summary{Verdict: review.VerdictExecutable},
		Coverage: &review.Coverage{
			Requirements: []review.Requirement{
				{ID: "REQ-0001", Requirement: "Auth", Status: review.CoverageCovered, SpecEvidence: []review.Evidence{specEv(3, 4)}, PlanEvidence: []review.Evidence{planEvidence(10, 12)}},
				{ID: "REQ-0002", Requirement: "Audit log", Status: review.CoverageUncovered, SpecEvidence: []review.Evidence{specEv(9, 9)}},
			},
			OutOfScope: []review.ScopeItem{{ID: "SCOPE-0001", PlanStep: "Dark mode", PlanEvidence: []review.Evidence{planEvidence(30, 31)}}},
		},
	}
	if errs := Validate(good, 40, map[string]int{"SPEC.md": 20}); len(errs) != 0 {
		t.Fatalf("valid coverage should pass, got %v", errs)
	}

	bad := &review.Review{
		Summary: review.Summary{Verdict: review.VerdictExecutable},
		Coverage: &review.Coverage{
			Requirements: []review.Requirement{
				{ID: "REQ-0001", Requirement: "", Status: "MAYBE", SpecEvidence: []review.Evidence{planEvidence(3, 4)}},               // empty text, bad status, plan-sourced spec cite
				{ID: "REQ-0001", Requirement: "Dup", Status: review.CoverageCovered, SpecEvidence: []review.Evidence{specEv(50, 50)}}, // dup id, beyond spec, covered w/o plan cite
			},
			OutOfScope: []review.ScopeItem{{ID: "", PlanStep: "", PlanEvidence: []review.Evidence{specEv(1, 1)}}},
		},
	}
	errs := Validate(bad, 40, map[string]int{"SPEC.md": 20})
	paths := map[string]bool{}
	for _, e := range errs {
		paths[e.Path] = true
	}
	for _, want := range []string{
		"coverage.requirements[0].requirement", "coverage.requirements[0].status", "coverage.requirements[0].spec_evidence[0].source",
		"coverage.requirements[1].id", "coverage.requirements[1].spec_evidence[0].line_end", "coverage.requirements[1].plan_evidence",
		"coverage.out_of_scope[0].id", "coverage.out_of_scope[0].plan_step", "coverage.out_of_scope[0].plan_evidence[0].source",
	} {
		if !paths[want] {
			t.Errorf("expected a validation error at %s; got %v", want, errs)
		}
	}
}

func TestAutoFixCoverage(t *testing.T) {
	r := &review.Review{
		Summary: review.Summary{Verdict: review.VerdictExecutable},
		Coverage: &review.Coverage{
			Requirements: []review.Requirement{
				{ID: "", Requirement: "a", Status: review.CoverageUncovered, SpecEvidence: []review.Evidence{{Source: "context", Path: "SPEC.md", LineStart: 5, LineEnd: 99}}},
				{ID: "REQ-0001", Requirement: "b", Status: review.CoverageUncovered, SpecEvidence: []review.Evidence{{Source: "context", Path: "SPEC.md", LineStart: 9, LineEnd: 7}}},
			},
			OutOfScope: []review.ScopeItem{{ID: "", PlanStep: "x", PlanEvidence: []review.Evidence{{Source: "plan", Path: "plan.md", LineStart: 0, LineEnd: 2}}}},
		},
	}
	fixes := AutoFix(r, 40, map[string]int{"SPEC.md": 20})
	c := r.Coverage
	if c.Requirements[0].ID != "REQ-0002" || c.Requirements[1].ID != "REQ-0001" || c.OutOfScope[0].ID != "SCOPE-0001" {
		t.Errorf("coverage IDs should be assigned without stealing existing ones: %+v", c)
	}
	if ev := c.Requirements[0].SpecEvidence[0]; ev.LineEnd != 20 {
		t.Errorf("spec citation should be clamped to the spec length, got %+v", ev)
	}
	if ev := c.Requirements[1].SpecEvidence[0]; ev.LineStart != 7 || ev.LineEnd != 9 {
		t.Errorf("inverted spec range should be swapped, got %+v", ev)
	}
	if ev := c.OutOfScope[0].PlanEvidence[0]; ev.LineStart != 1 {
		t.Errorf("plan citation start should be raised to 1, got %+v", ev)
	}
	if len(fixes) != 5 {
		t.Errorf("expected 5 fixes, got %d: %v", len(fixes), fixes)
	}
	if errs := Validate(r, 40, map[string]int{"SPEC.md": 20}); len(errs) != 0 {
		t.Errorf("coverage should validate after AutoFix, got %v", errs)
	}
}

func TestModelOutputSchemaJSONPropertyOrder(t *testing.T) {
	raw := string(ModelOutputSchemaJSON(OutputShape{Coverage: true, Patches: true, Checklists: true}))
	// Top-level generation order: findings first, summary last.
	idx := func(key string) int {
		i := strings.Index(raw, `"`+key+`":{"type"`)
		if i < 0 {
			t.Fatalf("top-level key %q not found", key)
		}
		return i
	}
	order := []string{"issues", "questions", "coverage", "patches", "checklists", "summary"}
	for n := 1; n < len(order); n++ {
		if idx(order[n-1]) > idx(order[n]) {
			t.Errorf("%q must be serialized before %q", order[n-1], order[n])
		}
	}
	// Nested objects follow their required list too: evidence starts
	// with source and ends with line_end, not alphabetically.
	if !strings.Contains(raw, `"properties":{"source":`) || strings.Contains(raw, `"properties":{"line_end":`) {
		t.Error("evidence properties should be ordered source, path, line_start, line_end")
	}
	// Keywords come in a fixed order.
	if !strings.HasPrefix(raw, `{"type":"object","properties":{"issues":`) {
		t.Errorf("unexpected prefix: %.80s", raw)
	}
	// Still valid JSON with identical content to the map form.
	var viaOrdered, viaMap any
	if err := json.Unmarshal([]byte(raw), &viaOrdered); err != nil {
		t.Fatal(err)
	}
	mapJSON, _ := json.Marshal(ModelOutputSchema(OutputShape{Coverage: true, Patches: true, Checklists: true}))
	_ = json.Unmarshal(mapJSON, &viaMap)
	if !reflect.DeepEqual(viaOrdered, viaMap) {
		t.Error("ordered serialization must carry the same content as json.Marshal")
	}
}
