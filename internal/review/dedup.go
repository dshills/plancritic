package review

import (
	"fmt"
	"slices"
	"strings"
)

// DedupIssues merges issues that report the same finding twice. Models
// frequently emit one ambiguity under two titles with the same or
// nearly the same citation; each copy costs the reader tokens and
// inflates the score.
//
// Two issues are duplicates when they share a category, at least one
// evidence entry overlaps (same source and file, intersecting line
// ranges), and their titles are the same statement (see
// titleRestated). Identical citations alone are not enough: one plan
// line can carry two unrelated findings of the same category.
//
// The first issue in slice order is kept (call after SortIssues so that
// is the more severe one); the duplicate's evidence entries that the
// kept issue lacks are folded in, its blocking flag is OR-ed into the
// kept issue, its own tags (including any earlier merged:<id> tags)
// are carried over, and its ID is recorded as a "merged:<id>" tag so
// nothing disappears without a trace. The returned slice preserves
// order.
func DedupIssues(issues []Issue) []Issue {
	out := make([]Issue, 0, len(issues))
	for _, cand := range issues {
		merged := false
		for i := range out {
			if !sameFinding(out[i], cand) {
				continue
			}
			for _, ev := range cand.Evidence {
				if !containsEvidence(out[i].Evidence, ev) {
					out[i].Evidence = append(out[i].Evidence, ev)
				}
			}
			out[i].Blocking = out[i].Blocking || cand.Blocking
			for _, tag := range cand.Tags {
				if !slices.Contains(out[i].Tags, tag) {
					out[i].Tags = append(out[i].Tags, tag)
				}
			}
			if cand.ID != "" {
				out[i].Tags = append(out[i].Tags, "merged:"+cand.ID)
			}
			merged = true
			break
		}
		if !merged {
			// Clone the slices that merges append to, so growing a kept
			// issue can never overwrite storage shared with another issue
			// in the caller's slice.
			cand.Evidence = append([]Evidence(nil), cand.Evidence...)
			cand.Tags = append([]string(nil), cand.Tags...)
			out = append(out, cand)
		}
	}
	return out
}

func sameFinding(a, b Issue) bool {
	if a.Category != b.Category || len(a.Evidence) == 0 || len(b.Evidence) == 0 {
		return false
	}
	return evidenceOverlaps(a.Evidence, b.Evidence) && titleRestated(a.Title, b.Title)
}

func evidenceKey(ev Evidence) string {
	file := "plan"
	if ev.Source != "plan" {
		file = NormalizeContextPath(ev.Path)
	}
	return fmt.Sprintf("%s\x00%s\x00%d\x00%d", ev.Source, file, ev.LineStart, ev.LineEnd)
}

func containsEvidence(evs []Evidence, ev Evidence) bool {
	k := evidenceKey(ev)
	for _, e := range evs {
		if evidenceKey(e) == k {
			return true
		}
	}
	return false
}

func evidenceOverlaps(a, b []Evidence) bool {
	for _, x := range a {
		for _, y := range b {
			if x.Source != y.Source {
				continue
			}
			if x.Source != "plan" && NormalizeContextPath(x.Path) != NormalizeContextPath(y.Path) {
				continue
			}
			if x.LineStart <= y.LineEnd && y.LineStart <= x.LineEnd {
				return true
			}
		}
	}
	return false
}

// titleRestated reports whether two titles are the same statement once
// case, edge punctuation, stopwords, and contractions are normalized:
// their significant tokens must be identical and in the same order.
// "Vague step wording" and "The vague step wording." match; nothing
// that adds, drops, reorders, negates, or changes a word does, because
// any such change can change the finding ("Clients retry" vs "Clients
// must retry", "Production overwrites staging" vs "Staging overwrites
// production", "C++" vs "C"). A missed merge costs one duplicate line;
// a wrong merge loses a finding, so the rule errs toward not merging.
func titleRestated(a, b string) bool {
	ta, tb := titleTokens(a), titleTokens(b)
	return len(ta) > 0 && slices.Equal(ta, tb)
}

// titleStopwords are dropped from comparison: articles, prepositions,
// and non-modal auxiliary verbs (which also absorb the stems left by
// expanded contractions). Modal verbs ("must", "may", "should", ...)
// and logical conjunctions ("and", "or") are kept because they change
// what is being required; negation words are counted as polarity.
var titleStopwords = map[string]bool{
	"a": true, "an": true, "the": true, "of": true, "in": true, "on": true, "to": true,
	"for": true, "with": true, "it": true, "this": true, "that": true,
	"is": true, "are": true, "was": true, "were": true, "be": true, "been": true,
	"do": true, "does": true, "did": true, "has": true, "have": true, "had": true,
	"use": true, "used": true, "uses": true,
}

var negationWords = map[string]bool{
	"no": true, "not": true, "never": true, "without": true, "non": true,
}

// expandContraction splits a whole token that is a negative contraction
// (straight or curly apostrophe) or "cannot" into its stem and "not",
// so polarity survives tokenization. Only complete tokens are expanded;
// an identifier such as "cannotConnect" is left alone.
func expandContraction(tok string) []string {
	norm := strings.NewReplacer("’", "'", "‘", "'").Replace(tok)
	switch norm {
	case "can't", "cannot":
		return []string{"can", "not"}
	case "won't":
		return []string{"will", "not"}
	case "shan't":
		return []string{"shall", "not"}
	case "ain't":
		return []string{"not"}
	}
	if stem, ok := strings.CutSuffix(norm, "n't"); ok && stem != "" {
		return []string{stem, "not"}
	}
	return []string{tok}
}

// prosePunctuation is what gets trimmed from token edges: the marks
// prose wraps words in. Identifier characters such as '+', '/', '*',
// '#', '$', and '@' are deliberately not included, so "C++", "/api/*",
// and "@scope/pkg" survive intact.
const prosePunctuation = ".,;:!?\"'`()[]{}<>«»“”‘’"

// titleTokens tokenizes a title into its significant lowercase tokens
// in order. Tokens split on whitespace and lose only edge prose
// punctuation, so technical identifiers keep their own punctuation
// ("/api/users" and "/api-users" stay distinct, "C++" is not "C") and
// non-ASCII terms stay intact. Stopwords are dropped; negation words
// and logical conjunctions are kept because they carry meaning.
func titleTokens(s string) []string {
	var tokens []string
	for _, raw := range strings.Fields(strings.ToLower(s)) {
		raw = strings.Trim(raw, prosePunctuation)
		if raw == "" {
			continue
		}
		for _, f := range expandContraction(raw) {
			if negationWords[f] || !titleStopwords[f] {
				tokens = append(tokens, f)
			}
		}
	}
	return tokens
}
