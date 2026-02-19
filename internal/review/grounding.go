package review

import "strings"

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

// GroundingViolation records a potential fabrication in an issue.
type GroundingViolation struct {
	IssueID string
	Field   string
	Phrase  string
}

// CheckGrounding scans issue and question text fields for phrases suggesting fabricated repo knowledge.
func CheckGrounding(r *Review) []GroundingViolation {
	var violations []GroundingViolation
	for _, iss := range r.Issues {
		for _, field := range []struct {
			name string
			text string
		}{
			{"description", iss.Description},
			{"impact", iss.Impact},
			{"recommendation", iss.Recommendation},
		} {
			lower := strings.ToLower(field.text)
			for _, phrase := range fabricationPhrases {
				if strings.Contains(lower, phrase) {
					violations = append(violations, GroundingViolation{
						IssueID: iss.ID,
						Field:   field.name,
						Phrase:  phrase,
					})
				}
			}
		}
	}
	for _, q := range r.Questions {
		for _, field := range []struct {
			name string
			text string
		}{
			{"question", q.Question},
			{"why_needed", q.WhyNeeded},
		} {
			lower := strings.ToLower(field.text)
			for _, phrase := range fabricationPhrases {
				if strings.Contains(lower, phrase) {
					violations = append(violations, GroundingViolation{
						IssueID: q.ID,
						Field:   field.name,
						Phrase:  phrase,
					})
				}
			}
		}
	}
	return violations
}

// ApplyGroundingDowngrades marks issues with UNVERIFIED tag and downgrades CRITICAL to WARN.
func ApplyGroundingDowngrades(r *Review, violations []GroundingViolation) {
	issueMap := make(map[string]*Issue)
	for i := range r.Issues {
		issueMap[r.Issues[i].ID] = &r.Issues[i]
	}

	downgraded := make(map[string]bool)
	for _, v := range violations {
		iss, ok := issueMap[v.IssueID]
		if !ok {
			continue
		}
		if downgraded[v.IssueID] {
			continue
		}
		downgraded[v.IssueID] = true

		// Add UNVERIFIED tag
		hasTag := false
		for _, tag := range iss.Tags {
			if tag == "UNVERIFIED" {
				hasTag = true
				break
			}
		}
		if !hasTag {
			iss.Tags = append(iss.Tags, "UNVERIFIED")
		}

		// Downgrade CRITICAL to WARN
		if iss.Severity == SeverityCritical {
			iss.Severity = SeverityWarn
		}
	}
}
