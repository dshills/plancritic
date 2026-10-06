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
//	VERDICT <verdict> score=<n> critical=<n> warn=<n> info=<n> [cached] [truncated]
//	<ISSUE-ID> <SEVERITY>[(blocking)] <CATEGORY> <loc>[,<loc>...] "<title>" -> <recommendation> [tags]
//	<Q-ID> <SEVERITY> <loc> "<question>" -> <why needed>
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
		if len(iss.Tags) > 0 {
			fmt.Fprintf(&b, " [%s]", strings.Join(iss.Tags, ","))
		}
		b.WriteByte('\n')
	}

	for _, q := range r.Questions {
		fmt.Fprintf(&b, "%s %s %s %q", q.ID, q.Severity, locs(q.Evidence, r.Input.PlanFile), oneLine(q.Question))
		if why := oneLine(q.WhyNeeded); why != "" {
			fmt.Fprintf(&b, " -> %s", why)
		}
		b.WriteByte('\n')
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
	return b.String()
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
