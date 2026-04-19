package review

import "testing"

func TestReconstructQuotesPlanSource(t *testing.T) {
	r := &Review{
		Issues: []Issue{{
			ID: "I1",
			Evidence: []Evidence{
				{Source: "plan", Path: "plan.md", LineStart: 2, LineEnd: 3, Quote: "whatever the LLM sent"},
			},
		}},
	}
	src := QuoteSource{
		PlanLines: []string{"line 1", "line 2", "line 3", "line 4"},
	}
	misses := ReconstructQuotes(r, src)
	if misses != 0 {
		t.Fatalf("unexpected misses: %d", misses)
	}
	got := r.Issues[0].Evidence[0].Quote
	want := "line 2\nline 3"
	if got != want {
		t.Errorf("quote = %q, want %q (LLM quote must be overwritten)", got, want)
	}
}

func TestReconstructQuotesContextSource(t *testing.T) {
	r := &Review{
		Questions: []Question{{
			ID: "Q1",
			Evidence: []Evidence{
				{Source: "context", Path: "constraints.md", LineStart: 1, LineEnd: 1},
			},
		}},
	}
	src := QuoteSource{
		ContextsByBasename: map[string][]string{
			"constraints.md": {"first rule", "second rule"},
		},
	}
	misses := ReconstructQuotes(r, src)
	if misses != 0 {
		t.Fatalf("unexpected misses: %d", misses)
	}
	if got := r.Questions[0].Evidence[0].Quote; got != "first rule" {
		t.Errorf("quote = %q, want %q", got, "first rule")
	}
}

func TestReconstructQuotesSingleLine(t *testing.T) {
	r := &Review{
		Issues: []Issue{{
			Evidence: []Evidence{
				{Source: "plan", LineStart: 1, LineEnd: 1},
			},
		}},
	}
	src := QuoteSource{PlanLines: []string{"only line"}}
	ReconstructQuotes(r, src)
	if got := r.Issues[0].Evidence[0].Quote; got != "only line" {
		t.Errorf("quote = %q, want %q", got, "only line")
	}
}

func TestReconstructQuotesNormalizesFullPathFromLLM(t *testing.T) {
	// If the LLM returns a full path despite the prompt showing only the
	// basename, the lookup should still resolve.
	r := &Review{
		Issues: []Issue{{
			Evidence: []Evidence{
				{Source: "context", Path: "docs/constraints.md", LineStart: 1, LineEnd: 1},
			},
		}},
	}
	src := QuoteSource{
		ContextsByBasename: map[string][]string{"constraints.md": {"rule one"}},
	}
	if misses := ReconstructQuotes(r, src); misses != 0 {
		t.Fatalf("unexpected misses: %d", misses)
	}
	if got := r.Issues[0].Evidence[0].Quote; got != "rule one" {
		t.Errorf("quote = %q, want %q", got, "rule one")
	}
}

func TestReconstructQuotesUnknownContextPath(t *testing.T) {
	r := &Review{
		Issues: []Issue{{
			Evidence: []Evidence{
				{Source: "context", Path: "missing.md", LineStart: 1, LineEnd: 1},
			},
		}},
	}
	misses := ReconstructQuotes(r, QuoteSource{})
	if misses != 1 {
		t.Errorf("expected 1 miss, got %d", misses)
	}
	if got := r.Issues[0].Evidence[0].Quote; got != unavailableQuote {
		t.Errorf("quote = %q, want placeholder %q", got, unavailableQuote)
	}
}

func TestReconstructQuotesOutOfRange(t *testing.T) {
	r := &Review{
		Issues: []Issue{{
			Evidence: []Evidence{
				{Source: "plan", LineStart: 5, LineEnd: 10},
			},
		}},
	}
	src := QuoteSource{PlanLines: []string{"a", "b", "c"}}
	misses := ReconstructQuotes(r, src)
	if misses != 1 {
		t.Errorf("expected 1 miss, got %d", misses)
	}
	if got := r.Issues[0].Evidence[0].Quote; got != unavailableQuote {
		t.Errorf("quote = %q, want placeholder", got)
	}
}

func TestReconstructQuotesInvertedRange(t *testing.T) {
	r := &Review{
		Issues: []Issue{{
			Evidence: []Evidence{
				{Source: "plan", LineStart: 3, LineEnd: 1},
			},
		}},
	}
	src := QuoteSource{PlanLines: []string{"a", "b", "c"}}
	misses := ReconstructQuotes(r, src)
	if misses != 1 {
		t.Errorf("expected inverted range to count as miss, got %d", misses)
	}
}

func TestReconstructQuotesUnknownSource(t *testing.T) {
	r := &Review{
		Issues: []Issue{{
			Evidence: []Evidence{
				{Source: "filesystem", LineStart: 1, LineEnd: 1},
			},
		}},
	}
	misses := ReconstructQuotes(r, QuoteSource{PlanLines: []string{"a"}})
	if misses != 1 {
		t.Errorf("unknown source should be a miss, got %d", misses)
	}
}

func TestReconstructQuotesMultipleEvidence(t *testing.T) {
	r := &Review{
		Issues: []Issue{
			{Evidence: []Evidence{
				{Source: "plan", LineStart: 1, LineEnd: 1},
				{Source: "context", Path: "a.md", LineStart: 2, LineEnd: 2},
			}},
		},
		Questions: []Question{
			{Evidence: []Evidence{
				{Source: "plan", LineStart: 2, LineEnd: 2},
			}},
		},
	}
	src := QuoteSource{
		PlanLines:          []string{"plan-1", "plan-2"},
		ContextsByBasename: map[string][]string{"a.md": {"ctx-1", "ctx-2"}},
	}
	if misses := ReconstructQuotes(r, src); misses != 0 {
		t.Fatalf("unexpected misses: %d", misses)
	}
	if got := r.Issues[0].Evidence[0].Quote; got != "plan-1" {
		t.Errorf("issue ev[0] = %q, want plan-1", got)
	}
	if got := r.Issues[0].Evidence[1].Quote; got != "ctx-2" {
		t.Errorf("issue ev[1] = %q, want ctx-2", got)
	}
	if got := r.Questions[0].Evidence[0].Quote; got != "plan-2" {
		t.Errorf("question ev[0] = %q, want plan-2", got)
	}
}
