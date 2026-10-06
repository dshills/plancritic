package review

import (
	"reflect"
	"testing"
)

func dupIssue(id string, cat Category, title string, evs ...Evidence) Issue {
	return Issue{ID: id, Severity: SeverityWarn, Category: cat, Title: title, Evidence: evs}
}

func planEv(start, end int) Evidence {
	return Evidence{Source: "plan", Path: "plan.md", LineStart: start, LineEnd: end}
}

func TestDedupIssuesIdenticalEvidence(t *testing.T) {
	issues := []Issue{
		dupIssue("ISSUE-0001", CategoryAmbiguity, "Vague wording in step", planEv(31, 31)),
		dupIssue("ISSUE-0002", CategoryAmbiguity, "The vague wording in step.", planEv(31, 31)), // same finding, restated
		dupIssue("ISSUE-0003", CategoryAmbiguity, "Undefined retry policy", planEv(31, 31)),     // unrelated, same line
		dupIssue("ISSUE-0004", CategoryTestGap, "Vague wording in step", planEv(31, 31)),        // other category
	}
	out := DedupIssues(issues)
	if len(out) != 3 {
		t.Fatalf("expected 3 issues after dedup, got %d: %+v", len(out), out)
	}
	if out[0].ID != "ISSUE-0001" || !reflect.DeepEqual(out[0].Tags, []string{"merged:ISSUE-0002"}) {
		t.Errorf("reworded duplicate should merge into the first with a tag, got %+v", out[0])
	}
	if out[1].ID != "ISSUE-0003" {
		t.Errorf("an unrelated finding on the same line must survive, got %+v", out[1])
	}
	if out[2].ID != "ISSUE-0004" {
		t.Errorf("different category must not merge, got %+v", out[2])
	}
}

func TestDedupIssuesMergesBlockingFlag(t *testing.T) {
	a := dupIssue("ISSUE-0001", CategoryContradiction, "Dependency claim conflicts", planEv(5, 6))
	b := dupIssue("ISSUE-0002", CategoryContradiction, "The dependency claim conflicts.", planEv(5, 6))
	a.Severity, b.Severity = SeverityCritical, SeverityCritical
	b.Blocking = true
	out := DedupIssues([]Issue{a, b})
	if len(out) != 1 || !out[0].Blocking {
		t.Errorf("blocking must be OR-ed into the kept issue, got %+v", out)
	}
}

func TestDedupIssuesOverlapNeedsSimilarTitle(t *testing.T) {
	near := []Issue{
		dupIssue("ISSUE-0001", CategoryAmbiguity, "Use of ambiguous terminology", planEv(31, 32)),
		dupIssue("ISSUE-0002", CategoryAmbiguity, "Use of ambiguous terminology!", planEv(31, 31), planEv(35, 35)),
	}
	out := DedupIssues(near)
	if len(out) != 1 {
		t.Fatalf("overlapping evidence with similar titles should merge, got %d", len(out))
	}
	wantEv := []Evidence{planEv(31, 32), planEv(31, 31), planEv(35, 35)}
	if !reflect.DeepEqual(out[0].Evidence, wantEv) {
		t.Errorf("missing evidence should be folded in, got %+v", out[0].Evidence)
	}

	far := []Issue{
		dupIssue("ISSUE-0001", CategoryAmbiguity, "Vague performance target", planEv(10, 20)),
		dupIssue("ISSUE-0002", CategoryAmbiguity, "Undefined API contract", planEv(15, 15)),
	}
	if out := DedupIssues(far); len(out) != 2 {
		t.Errorf("overlapping evidence with unrelated titles must not merge, got %d", len(out))
	}
}

func TestDedupIssuesContextAndNoEvidence(t *testing.T) {
	ctx := func(path string, line int) Evidence {
		return Evidence{Source: "context", Path: path, LineStart: line, LineEnd: line}
	}
	issues := []Issue{
		dupIssue("ISSUE-0001", CategoryContradiction, "Spec conflict", ctx("docs/SPEC.md", 5)),
		dupIssue("ISSUE-0002", CategoryContradiction, "The spec conflict.", ctx("SPEC.md", 5)), // same file by basename
		dupIssue("ISSUE-0003", CategoryContradiction, "Spec conflict", ctx("OTHER.md", 5)),     // different file
		dupIssue("ISSUE-0004", CategoryContradiction, "Missing evidence"),
		dupIssue("ISSUE-0005", CategoryContradiction, "Missing evidence"),
	}
	out := DedupIssues(issues)
	ids := []string{}
	for _, iss := range out {
		ids = append(ids, iss.ID)
	}
	want := []string{"ISSUE-0001", "ISSUE-0003", "ISSUE-0004", "ISSUE-0005"}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("ids = %v, want %v (basename match merges; no evidence never merges)", ids, want)
	}
}

func TestTitleRestated(t *testing.T) {
	yes := [][2]string{
		{"Use of ambiguous terminology", "The use of ambiguous terminology."},
		{"Vague step wording", "The vague step wording"},
		{"No rollback step defined", "No rollback step is defined."},
		{"Tests aren't listed", "Tests not listed"}, // contraction == explicit not
		{"Rollback can't be undone", "Rollback cannot be undone"},
		{"Rollback can’t be undone", "Rollback can't be undone"}, // curly apostrophe
		{"Missing tests for /café", "Missing tests for the /café"},
		{"Missing tests for /api/users", "Missing tests for /api/users."},
		{"Missing tests for C++", "Missing tests for C++."},
	}
	for _, c := range yes {
		if !titleRestated(c[0], c[1]) || !titleRestated(c[1], c[0]) {
			t.Errorf("%q / %q should be treated as the same title (either order)", c[0], c[1])
		}
	}
	no := [][2]string{
		{"Missing authentication tests", "Missing authorization tests"},
		{"Missing tests", "Missing tests for the login flow"},              // added words narrow the claim
		{"Clients retry", "Clients must retry"},                            // inserted modal
		{"Encryption required", "Encryption may be required"},              // inserted modal
		{"Spec says clients must retry", "Spec says clients may retry"},    // modality differs
		{"Vague step wording", "Step wording is vague"},                    // reordered
		{"Production overwrites staging", "Staging overwrites production"}, // reordered: different direction
		{"Retries required", "Retries not required"},                       // negated
		{"Retries required", "Retries aren't required"},                    // contracted negation
		{"Retries required", "Retries aren’t required"},                    // curly apostrophe
		{"Retries required without backoff", "Retries not required without backoff"},
		{"Retries required without backoff", "Backoff required without retries"},
		{"No rollback step", "Rollback step not defined"},
		{"Requires authentication and authorization", "Requires authentication or authorization"},
		{"Missing tests for /api/users", "Missing tests for /api-users"},
		{"Missing tests for C++", "Missing tests for C"},
		{"Missing tests for /api/*", "Missing tests for /api/"},
		{"Missing tests for cannotConnect", "Missing tests for notConnect"},
		{"Missing tests for /café", "Missing tests for /cafè"},
		{"缺少测试 for login", "缺少回滚 for login"},
		{"", "x"},
		{"the of and", "with for"},
		{"no", "not"},
	}
	for _, c := range no {
		if titleRestated(c[0], c[1]) {
			t.Errorf("%q / %q must not be treated as the same title", c[0], c[1])
		}
	}
}

func TestDedupIssuesKeepsDistinctConcernsOnSameLine(t *testing.T) {
	issues := []Issue{
		dupIssue("ISSUE-0001", CategoryTestGap, "Missing authentication tests", planEv(40, 40)),
		dupIssue("ISSUE-0002", CategoryTestGap, "Missing authorization tests", planEv(40, 40)),
	}
	if out := DedupIssues(issues); len(out) != 2 {
		t.Errorf("titles that differ in a substantive word must not merge, got %d", len(out))
	}
}

func TestDedupIssuesKeepsOppositePolarity(t *testing.T) {
	issues := []Issue{
		dupIssue("ISSUE-0001", CategoryAmbiguity, "Retries required", planEv(12, 12)),
		dupIssue("ISSUE-0002", CategoryAmbiguity, "Retries not required", planEv(12, 12)),
	}
	if out := DedupIssues(issues); len(out) != 2 {
		t.Errorf("negated restatement must not merge, got %d", len(out))
	}
}

func TestDedupIssuesDoesNotAliasCallerSlices(t *testing.T) {
	shared := []Evidence{planEv(1, 1), planEv(2, 2), planEv(3, 3)}
	a := dupIssue("ISSUE-0001", CategoryAmbiguity, "Vague wording", shared[:1]...)
	a.Evidence = shared[:1] // len 1, cap 3: an append would write into shared[1]
	b := dupIssue("ISSUE-0002", CategoryAmbiguity, "The vague wording.", planEv(1, 1), planEv(9, 9))
	c := dupIssue("ISSUE-0003", CategoryTestGap, "Other", shared[1:2]...)
	out := DedupIssues([]Issue{a, b, c})
	if len(out) != 2 {
		t.Fatalf("expected a+b merged and c kept, got %d", len(out))
	}
	if shared[1].LineStart != 2 {
		t.Errorf("merging into the kept issue must not overwrite the caller's shared backing array: %+v", shared)
	}
	if out[1].Evidence[0].LineStart != 2 {
		t.Errorf("the other issue's citation must be intact, got %+v", out[1].Evidence)
	}
}

func TestDedupIssuesKeepsReorderedNegatedTitles(t *testing.T) {
	issues := []Issue{
		dupIssue("ISSUE-0001", CategoryRiskOperations, "Retries required without backoff", planEv(12, 12)),
		dupIssue("ISSUE-0002", CategoryRiskOperations, "Backoff required without retries", planEv(12, 12)),
	}
	if out := DedupIssues(issues); len(out) != 2 {
		t.Errorf("reordered negated titles describe different requirements and must not merge, got %d", len(out))
	}
}

func TestDedupIssuesKeepsDirectionalTitles(t *testing.T) {
	issues := []Issue{
		dupIssue("ISSUE-0001", CategoryRiskData, "Production overwrites staging", planEv(8, 8)),
		dupIssue("ISSUE-0002", CategoryRiskData, "Staging overwrites production", planEv(8, 8)),
	}
	if out := DedupIssues(issues); len(out) != 2 {
		t.Errorf("reversed subject/object titles describe different risks and must not merge, got %d", len(out))
	}
}

func TestDedupIssuesKeepsDistinctIdentifiers(t *testing.T) {
	issues := []Issue{
		dupIssue("ISSUE-0001", CategoryTestGap, "Missing tests for /api/users", planEv(21, 21)),
		dupIssue("ISSUE-0002", CategoryTestGap, "Missing tests for /api-users", planEv(21, 21)),
	}
	if out := DedupIssues(issues); len(out) != 2 {
		t.Errorf("endpoint identifiers that differ only in punctuation must not merge, got %d", len(out))
	}
}

func TestDedupIssuesCarriesProvenanceTags(t *testing.T) {
	a := dupIssue("ISSUE-0001", CategoryAmbiguity, "Vague wording", planEv(1, 1))
	b := dupIssue("ISSUE-0002", CategoryAmbiguity, "The vague wording", planEv(1, 1))
	b.Tags = []string{"UNVERIFIED", "merged:ISSUE-0009"}
	out := DedupIssues([]Issue{a, b})
	if len(out) != 1 {
		t.Fatalf("expected a merge, got %d", len(out))
	}
	want := []string{"UNVERIFIED", "merged:ISSUE-0009", "merged:ISSUE-0002"}
	if !reflect.DeepEqual(out[0].Tags, want) {
		t.Errorf("absorbed tags and earlier provenance must be kept: got %v, want %v", out[0].Tags, want)
	}
}
