package review

const (
	DefaultMaxIssues    = 50
	DefaultMaxQuestions = 20
)

// Truncate caps issues and questions to the given limits.
// If either list exceeds its limit, it is truncated and a synthetic
// WARN issue is appended noting the truncation.
func Truncate(r *Review, maxIssues, maxQuestions int) {
	if maxIssues <= 0 {
		maxIssues = DefaultMaxIssues
	}
	if maxQuestions <= 0 {
		maxQuestions = DefaultMaxQuestions
	}

	truncated := false

	if len(r.Issues) > maxIssues {
		r.Issues = r.Issues[:maxIssues-1]
		truncated = true
	}

	if len(r.Questions) > maxQuestions {
		r.Questions = r.Questions[:maxQuestions]
		truncated = true
	}

	if truncated {
		r.Issues = append(r.Issues, Issue{
			ID:             "ISSUE-TRUNC",
			Severity:       SeverityWarn,
			Category:       CategoryAmbiguity,
			Title:          "Output truncated",
			Description:    "The number of issues or questions exceeded the configured limits. Increase limits to see all results.",
			Recommendation: "Re-run with higher limits.",
			Evidence: []Evidence{
				{Source: "plan", Path: "plan", LineStart: 1, LineEnd: 1, Quote: "(truncation notice)"},
			},
		})
	}
}
