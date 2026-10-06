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

	// Changes since baseline (only present with --baseline)
	if d := r.Delta; d != nil {
		fmt.Fprintf(&b, "## Changes Since %s\n\n", d.BaselineFile)
		fmt.Fprintf(&b, "Score change: %+d. New: %d, persisting: %d, resolved: %d.\n\n", d.ScoreChange, len(d.New), len(d.Persisting), len(d.Resolved))
		renderDeltaList(&b, "Resolved", d.Resolved)
		renderDeltaList(&b, "New", d.New)
		renderDeltaList(&b, "Persisting", d.Persisting)
	}

	// Specification coverage (only present with --spec)
	if c := r.Coverage; c != nil {
		b.WriteString("## Specification Coverage\n\n")
		fmt.Fprintf(&b, "Covered: %d, partial: %d, uncovered: %d, out of scope: %d.\n\n", c.Summary.Covered, c.Summary.Partial, c.Summary.Uncovered, c.Summary.OutOfScope)
		if len(c.Requirements) > 0 {
			b.WriteString("| ID | Status | Requirement | Spec | Plan | Note |\n|---|---|---|---|---|---|\n")
			for _, req := range c.Requirements {
				fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n", req.ID, req.Status, mdCell(req.Requirement), mdLocs(req.SpecEvidence), mdLocs(req.PlanEvidence), mdCell(req.Note))
			}
			b.WriteString("\n")
		}
		if len(c.OutOfScope) > 0 {
			b.WriteString("**Out of scope (in the plan, not in the spec):**\n")
			for _, item := range c.OutOfScope {
				fmt.Fprintf(&b, "- %s %s (%s) %s\n", item.ID, mdCell(item.PlanStep), mdLocs(item.PlanEvidence), mdCell(item.Note))
			}
			b.WriteString("\n")
		}
	}

	// Checklists (only present when requested with --checklists)
	if len(r.Checklists) > 0 {
		b.WriteString("## Checklists\n\n")
		for _, cl := range r.Checklists {
			fmt.Fprintf(&b, "### %s\n\n", cl.Title)
			for _, c := range cl.Checks {
				fmt.Fprintf(&b, "- [%s] %s\n", c.Status, c.Check)
			}
			b.WriteString("\n")
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

func renderDeltaList(b *strings.Builder, heading string, entries []review.DeltaEntry) {
	if len(entries) == 0 {
		return
	}
	fmt.Fprintf(b, "**%s:**\n", heading)
	for _, e := range entries {
		fmt.Fprintf(b, "- %s %s [%s] %s\n", e.Kind, e.ID, e.Severity, e.Title)
	}
	b.WriteString("\n")
}

// mdLocs renders evidence as "L12-14, L30" for a table cell.
func mdLocs(evs []review.Evidence) string {
	if len(evs) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(evs))
	for _, ev := range evs {
		if ev.LineEnd > ev.LineStart {
			parts = append(parts, fmt.Sprintf("L%d-%d", ev.LineStart, ev.LineEnd))
		} else {
			parts = append(parts, fmt.Sprintf("L%d", ev.LineStart))
		}
	}
	return strings.Join(parts, ", ")
}

// mdCell escapes pipes and newlines so free text cannot break a table.
func mdCell(s string) string {
	return strings.ReplaceAll(strings.Join(strings.Fields(s), " "), "|", "\\|")
}
