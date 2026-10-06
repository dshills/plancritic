package render

import (
	"fmt"
	"strings"

	"github.com/dshills/plancritic/internal/review"
)

// Compact renders a review as deterministic plain text with one line per
// finding. It is meant to be read by a coding agent that already has the
// plan in its context: evidence is given as file:line references rather
// than quoted text, descriptions and impact are omitted in favor of the
// title and recommendation, and there is no indentation or markup to
// pay for. The first line is always the verdict summary (see
// CompactHeader); every later line starts with the finding's ID.
//
// Line formats:
//
//	VERDICT <verdict> score=<n> critical=<n> warn=<n> info=<n> [cached] [truncated] [new=<n> persisting=<n> resolved=<n> score_change=<+n>] [in=<n> out=<n> [cache_read=<n> cache_write=<n>]]
//	<ISSUE-ID> <SEVERITY>[(blocking)] <CATEGORY> <loc>[,<loc>...] "<title>" -> <recommendation> [tags]
//	<Q-ID> <SEVERITY> <loc> "<question>" -> <why needed>
//	RESOLVED <kind> <baseline id> "<title>"
//	COVERAGE covered=<n> partial=<n> uncovered=<n> out_of_scope=<n>
//	<REQ-ID> PARTIAL|UNCOVERED <spec loc> "<requirement>" -> <note>
//	<SCOPE-ID> OUT_OF_SCOPE <plan loc> "<plan step>" -> <note>
//	<PATCH-ID> "<title>" (diff available via --patch-out)
//	CHECK <checklist id> pass=<n> fail=<n> na=<n>
//	CHECK <checklist id> FAIL "<check>"
//
// where <loc> is <file>:L<start>[-<end>].
func Compact(r *review.Review) string {
	var b strings.Builder
	b.WriteString(CompactHeader(r))
	b.WriteByte('\n')

	for _, iss := range r.Issues {
		sev := string(iss.Severity)
		if iss.Blocking {
			sev += "(blocking)"
		}
		fmt.Fprintf(&b, "%s %s %s %s %q", iss.ID, sev, iss.Category, locs(iss.Evidence, r.Input.PlanFile), oneLine(iss.Title))
		if rec := oneLine(iss.Recommendation); rec != "" {
			fmt.Fprintf(&b, " -> %s", rec)
		}
		writeTags(&b, r.Delta.Status("issue", iss.ID), iss.Tags)
		b.WriteByte('\n')
	}

	for _, q := range r.Questions {
		fmt.Fprintf(&b, "%s %s %s %q", q.ID, q.Severity, locs(q.Evidence, r.Input.PlanFile), oneLine(q.Question))
		if why := oneLine(q.WhyNeeded); why != "" {
			fmt.Fprintf(&b, " -> %s", why)
		}
		writeTags(&b, r.Delta.Status("question", q.ID), nil)
		b.WriteByte('\n')
	}

	if r.Delta != nil {
		for _, e := range r.Delta.Resolved {
			fmt.Fprintf(&b, "RESOLVED %s %s %q\n", e.Kind, e.ID, oneLine(e.Title))
		}
	}

	if c := r.Coverage; c != nil {
		s := c.Summary
		fmt.Fprintf(&b, "COVERAGE covered=%d partial=%d uncovered=%d out_of_scope=%d\n", s.Covered, s.Partial, s.Uncovered, s.OutOfScope)
		// Covered requirements are summarized by the count above; only
		// the gaps get their own line.
		for _, req := range c.Requirements {
			if req.Status == review.CoverageCovered {
				continue
			}
			fmt.Fprintf(&b, "%s %s %s %q", req.ID, req.Status, locs(req.SpecEvidence, r.Input.PlanFile), oneLine(req.Requirement))
			if note := oneLine(req.Note); note != "" {
				fmt.Fprintf(&b, " -> %s", note)
			}
			b.WriteByte('\n')
		}
		for _, item := range c.OutOfScope {
			fmt.Fprintf(&b, "%s OUT_OF_SCOPE %s %q", item.ID, locs(item.PlanEvidence, r.Input.PlanFile), oneLine(item.PlanStep))
			if note := oneLine(item.Note); note != "" {
				fmt.Fprintf(&b, " -> %s", note)
			}
			b.WriteByte('\n')
		}
	}

	for _, p := range r.Patches {
		// The renderer cannot know whether a diff file was written, so
		// this names the flag that produces one rather than claiming it ran.
		fmt.Fprintf(&b, "%s %q (diff available via --patch-out)\n", p.ID, oneLine(p.Title))
	}

	for _, cl := range r.Checklists {
		var pass, fail, na int
		for _, c := range cl.Checks {
			switch c.Status {
			case "PASS":
				pass++
			case "FAIL":
				fail++
			default:
				na++
			}
		}
		fmt.Fprintf(&b, "CHECK %s pass=%d fail=%d na=%d\n", cl.ID, pass, fail, na)
		for _, c := range cl.Checks {
			if c.Status == "FAIL" {
				fmt.Fprintf(&b, "CHECK %s FAIL %q\n", cl.ID, oneLine(c.Check))
			}
		}
	}

	return b.String()
}

// CompactHeader is the one-line verdict summary that opens Compact
// output and is all that --quiet prints.
func CompactHeader(r *review.Review) string {
	var b strings.Builder
	fmt.Fprintf(&b, "VERDICT %s score=%d critical=%d warn=%d info=%d",
		r.Summary.Verdict, r.Summary.Score, r.Summary.CriticalCount, r.Summary.WarnCount, r.Summary.InfoCount)
	if r.Meta.Cached {
		b.WriteString(" cached")
	}
	if r.Meta.Truncated {
		b.WriteString(" truncated")
	}
	if d := r.Delta; d != nil {
		fmt.Fprintf(&b, " new=%d persisting=%d resolved=%d score_change=%+d", len(d.New), len(d.Persisting), len(d.Resolved), d.ScoreChange)
	}
	if u := r.Meta.Usage; u != nil {
		fmt.Fprintf(&b, " in=%d out=%d", u.InputTokens, u.OutputTokens)
		if u.CacheReadInputTokens > 0 || u.CacheCreationInputTokens > 0 {
			fmt.Fprintf(&b, " cache_read=%d cache_write=%d", u.CacheReadInputTokens, u.CacheCreationInputTokens)
		}
	}
	return b.String()
}

// writeTags appends " [a,b]" combining an optional delta status with the
// finding's own tags; nothing is written when both are empty.
func writeTags(b *strings.Builder, status string, tags []string) {
	all := make([]string, 0, len(tags)+1)
	if status != "" {
		all = append(all, status)
	}
	all = append(all, tags...)
	if len(all) > 0 {
		fmt.Fprintf(b, " [%s]", strings.Join(all, ","))
	}
}

// locs renders evidence as comma-separated file:line references. Plan
// citations use the reviewed plan's file name so an agent can open the
// location directly; context citations already carry their basename.
func locs(evs []review.Evidence, planFile string) string {
	if len(evs) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(evs))
	for _, ev := range evs {
		file := ev.Path
		if ev.Source == "plan" && planFile != "" {
			file = planFile
		}
		if ev.LineEnd > ev.LineStart {
			parts = append(parts, fmt.Sprintf("%s:L%d-%d", file, ev.LineStart, ev.LineEnd))
		} else {
			parts = append(parts, fmt.Sprintf("%s:L%d", file, ev.LineStart))
		}
	}
	return strings.Join(parts, ",")
}

// oneLine collapses all whitespace runs (including newlines) to single
// spaces so every finding stays on one line.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
