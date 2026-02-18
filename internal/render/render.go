// Package render produces Markdown output from a review.
package render

import (
	"fmt"
	"strings"

	"github.com/dshills/plancritic/internal/review"
)

// Markdown renders a review as a Markdown report.
func Markdown(r *review.Review) string {
	var b strings.Builder

	// Summary
	b.WriteString("# PlanCritic Review\n\n")
	fmt.Fprintf(&b, "**Verdict:** %s\n", r.Summary.Verdict)
	fmt.Fprintf(&b, "**Score:** %d / 100\n", r.Summary.Score)
	fmt.Fprintf(&b, "**Issues:** %d critical, %d warnings, %d info\n\n",
		r.Summary.CriticalCount, r.Summary.WarnCount, r.Summary.InfoCount)

	// Issues by severity
	criticals := filterIssues(r.Issues, review.SeverityCritical)
	warns := filterIssues(r.Issues, review.SeverityWarn)
	infos := filterIssues(r.Issues, review.SeverityInfo)

	if len(criticals) > 0 {
		b.WriteString("## Critical Issues\n\n")
		for _, iss := range criticals {
			renderIssue(&b, iss)
		}
	}

	if len(warns) > 0 {
		b.WriteString("## Warnings\n\n")
		for _, iss := range warns {
			renderIssue(&b, iss)
		}
	}

	if len(infos) > 0 {
		b.WriteString("## Info\n\n")
		for _, iss := range infos {
			renderIssue(&b, iss)
		}
	}

	if len(r.Issues) == 0 {
		b.WriteString("No issues found.\n\n")
	}

	// Questions
	if len(r.Questions) > 0 {
		b.WriteString("## Questions\n\n")
		for _, q := range r.Questions {
			fmt.Fprintf(&b, "### %s [%s]\n\n", q.Question, q.Severity)
			fmt.Fprintf(&b, "%s\n\n", q.WhyNeeded)
			for _, ev := range q.Evidence {
				fmt.Fprintf(&b, "> %s (L%d-%d)\n", ev.Quote, ev.LineStart, ev.LineEnd)
			}
			if len(q.SuggestedAnswers) > 0 {
				b.WriteString("\n**Suggested answers:**\n")
				for _, a := range q.SuggestedAnswers {
					fmt.Fprintf(&b, "- %s\n", a)
				}
			}
			b.WriteString("\n")
		}
	}

	// Patches
	if len(r.Patches) > 0 {
		b.WriteString("## Suggested Patches\n\n")
		for _, p := range r.Patches {
			fmt.Fprintf(&b, "### %s\n\n", p.Title)
			b.WriteString("```diff\n")
			b.WriteString(p.DiffUnified)
			b.WriteString("\n```\n\n")
		}
	}

	// Context used
	if len(r.Input.ContextFiles) > 0 {
		b.WriteString("## Context Used\n\n")
		for _, cf := range r.Input.ContextFiles {
			fmt.Fprintf(&b, "- %s\n", cf.Path)
		}
		b.WriteString("\n")
	}

	return b.String()
}

func filterIssues(issues []review.Issue, sev review.Severity) []review.Issue {
	var result []review.Issue
	for _, iss := range issues {
		if iss.Severity == sev {
			result = append(result, iss)
		}
	}
	return result
}

func renderIssue(b *strings.Builder, iss review.Issue) {
	fmt.Fprintf(b, "### %s [%s / %s]\n\n", iss.Title, iss.Severity, iss.Category)
	fmt.Fprintf(b, "%s\n\n", iss.Description)
	for _, ev := range iss.Evidence {
		fmt.Fprintf(b, "> %s (L%d-%d)\n", ev.Quote, ev.LineStart, ev.LineEnd)
	}
	b.WriteString("\n")
	fmt.Fprintf(b, "**Impact:** %s\n\n", iss.Impact)
	fmt.Fprintf(b, "**Recommendation:** %s\n\n", iss.Recommendation)
}
