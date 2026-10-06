package review

import (
	"regexp"
	"strings"
)

// fabricationPhrases are patterns suggesting the model invented repo knowledge.
var fabricationPhrases = []string{
	"the codebase uses",
	"the repository contains",
	"the existing implementation",
	"currently the system",
	"as seen in the source",
	"the project's",
	"the current codebase",
	"looking at the code",
	"in the source code",
	"the existing code",
}

// GroundingViolation records a potential fabrication in an issue or
// question.
type GroundingViolation struct {
	IssueID string
	Field   string
	Phrase  string
}

// CheckGrounding scans issue and question text for phrases suggesting
// fabricated repo knowledge. A phrase is a violation only when it does
// not appear in the finding's own cited evidence: a finding that quotes
// the plan saying "the existing code uses Cobra" is restating the plan,
// not inventing repo facts. The check is deliberately scoped to the
// finding's citations rather than the whole plan, because phrases such
// as "the project's" are common plan wording and a plan-wide match
// would let any finding repeat them unchecked. Matching is
// case-insensitive and whitespace-normalized; evidence quotes must
// already be reconstructed (see ReconstructQuotes).
func CheckGrounding(r *Review) []GroundingViolation {
	var violations []GroundingViolation
	check := func(id string, evs []Evidence, fields [][2]string) {
		// Each citation is checked on its own: joining them could
		// manufacture a phrase across the boundary between two quotes.
		cited := make([]string, 0, len(evs))
		for _, ev := range evs {
			cited = append(cited, normalizeGrounding(ev.Quote))
		}
		groundedBy := func(phrase string) bool {
			for _, q := range cited {
				if containsPhrase(q, phrase) {
					return true
				}
			}
			return false
		}
		for _, f := range fields {
			text := normalizeGrounding(f[1])
			for _, phrase := range fabricationPhrases {
				if containsPhrase(text, phrase) && !groundedBy(phrase) {
					violations = append(violations, GroundingViolation{IssueID: id, Field: f[0], Phrase: phrase})
				}
			}
		}
	}
	for _, iss := range r.Issues {
		check(iss.ID, iss.Evidence, [][2]string{
			{"description", iss.Description},
			{"impact", iss.Impact},
			{"recommendation", iss.Recommendation},
		})
	}
	for _, q := range r.Questions {
		check(q.ID, q.Evidence, [][2]string{
			{"question", q.Question},
			{"why_needed", q.WhyNeeded},
		})
	}
	return violations
}

// normalizeGrounding lowercases, unifies curly apostrophes, and collapses
// whitespace so line breaks or reflowing in a quote do not hide a match.
func normalizeGrounding(s string) string {
	s = strings.NewReplacer("’", "'", "‘", "'").Replace(strings.ToLower(s))
	return strings.Join(strings.Fields(s), " ")
}

// phrasePatterns holds one compiled pattern per fabrication phrase,
// built once at init so concurrent reviews (the web UI) share them
// without locking.
var phrasePatterns = func() map[string]*regexp.Regexp {
	m := make(map[string]*regexp.Regexp, len(fabricationPhrases))
	for _, p := range fabricationPhrases {
		m[p] = regexp.MustCompile(`(^|[^\pL\pN])` + regexp.QuoteMeta(p) + `($|[^\pL\pN])`)
	}
	return m
}()

// containsPhrase reports whether text contains phrase on word
// boundaries, so "the existing code" does not match "the existing
// codec" and "the project's" does not match "the projects".
func containsPhrase(text, phrase string) bool {
	return phrasePatterns[phrase].MatchString(text)
}

// ApplyGroundingDowngrades tags each issue with a violation as
// UNVERIFIED, plus one "UNVERIFIED:<phrase>" tag per distinct phrase so
// a reader can see why, and downgrades CRITICAL to WARN. Questions are
// reported by CheckGrounding but not modified: they carry no blocking
// severity semantics to downgrade.
func ApplyGroundingDowngrades(r *Review, violations []GroundingViolation) {
	issueMap := make(map[string]*Issue)
	for i := range r.Issues {
		issueMap[r.Issues[i].ID] = &r.Issues[i]
	}
	addTag := func(iss *Issue, tag string) {
		for _, t := range iss.Tags {
			if t == tag {
				return
			}
		}
		iss.Tags = append(iss.Tags, tag)
	}
	for _, v := range violations {
		iss, ok := issueMap[v.IssueID]
		if !ok {
			continue
		}
		addTag(iss, "UNVERIFIED")
		addTag(iss, "UNVERIFIED:"+v.Phrase)
		if iss.Severity == SeverityCritical {
			iss.Severity = SeverityWarn
		}
	}
}
