// Package lint runs deterministic, zero-token checks over a plan: the
// profile's ambiguity trigger phrases and contradiction pairs, unresolved
// placeholders, empty sections, duplicate headings, references to phases
// that do not exist, and phases with no acceptance criteria.
//
// Every finding is INFO, tagged "local", and never blocking: these are
// lexical signals an agent can fix while iterating on wording without a
// model call, not judgments. When a model review runs, the same findings
// are recorded ahead of time and the model is told not to repeat them.
package lint

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/dshills/plancritic/internal/plan"
	"github.com/dshills/plancritic/internal/profile"
	"github.com/dshills/plancritic/internal/review"
)

// maxEvidence caps the citations attached to one finding; the
// description carries the total count.
const maxEvidence = 10

// Run returns the local findings for p under prof (prof may be nil).
func Run(p *plan.Plan, prof *profile.Profile) []review.Issue {
	l := &linter{plan: p, path: planBase(p.FilePath)}
	l.fences()
	if prof != nil {
		l.triggers(prof.Heuristics.AmbiguityTriggers)
		l.contradictions(prof.Heuristics.Contradictions)
	}
	l.placeholders()
	l.structure()
	return l.issues
}

// occurrenceNote matches the suffix setOccurrences appends.
var occurrenceNote = regexp.MustCompile(` \(\d+ occurrences; first \d+ cited\)$`)

// Unsuperseded removes, occurrence by occurrence, the local lines that a
// model issue of the same category has confirmed (overlapping plan
// lines), then re-derives each finding's capped evidence from what is
// left. A finding is dropped only when every occurrence is confirmed,
// so confirming one TODO never hides the others. planLines supplies the
// quotes for re-derived evidence.
func Unsuperseded(local, model []review.Issue, planLines []string) []review.Issue {
	out := make([]review.Issue, 0, len(local))
	for _, l := range local {
		occ := l.Occurrences
		if len(occ) == 0 { // not produced by Run; fall back to its citations
			for _, ev := range l.Evidence {
				occ = append(occ, ev.LineStart)
			}
		}
		var remaining []int
		for _, n := range occ {
			line := review.Evidence{Source: "plan", LineStart: n, LineEnd: n}
			confirmed := false
			for _, m := range model {
				if m.Category == l.Category && overlaps(line, m.Evidence) {
					confirmed = true
					break
				}
			}
			if !confirmed {
				remaining = append(remaining, n)
			}
		}
		if len(remaining) == 0 {
			continue
		}
		// A contradiction candidate is one claim spanning both triggers;
		// a model contradiction on either side confirms the pair, so
		// partially trimming it would leave a finding that cites only
		// one side of a two-sided claim.
		if len(remaining) < len(occ) && slices.Contains(l.Tags, "contradiction-pair") {
			continue
		}
		if len(remaining) < len(occ) && len(planLines) > 0 {
			path := ""
			if len(l.Evidence) > 0 {
				path = l.Evidence[0].Path
			}
			setOccurrences(&l, occurrenceNote.ReplaceAllString(l.Description, ""), remaining, planLines, path)
		}
		out = append(out, l)
	}
	return out
}

func overlaps(x review.Evidence, ys []review.Evidence) bool {
	for _, y := range ys {
		if x.Source == "plan" && y.Source == "plan" && x.LineStart <= y.LineEnd && y.LineStart <= x.LineEnd {
			return true
		}
	}
	return false
}

// Summaries renders findings as one short line each, for the prompt's
// "already flagged" list.
func Summaries(issues []review.Issue) []string {
	out := make([]string, 0, len(issues))
	for _, iss := range issues {
		lines := make([]string, 0, len(iss.Evidence))
		for _, ev := range iss.Evidence {
			if ev.LineEnd > ev.LineStart {
				lines = append(lines, fmt.Sprintf("L%d-%d", ev.LineStart, ev.LineEnd))
			} else {
				lines = append(lines, fmt.Sprintf("L%d", ev.LineStart))
			}
		}
		out = append(out, fmt.Sprintf("%s: %s (%s)", iss.Category, iss.Title, strings.Join(lines, ", ")))
	}
	return out
}

type linter struct {
	plan    *plan.Plan
	path    string
	issues  []review.Issue
	inFence []bool // per line: inside a ``` or ~~~ fenced block
}

var fencePattern = regexp.MustCompile("^\\s{0,3}(`{3,}|~{3,})(.*)$")

// fences marks the lines inside fenced code blocks (the fence lines
// themselves included) so that shell comments or embedded Markdown
// examples are not mistaken for headings or phase references. As in
// CommonMark, a block opened with N fence characters closes only on a
// line of at least N of the same character and nothing else, so a
// four-backtick fence can quote a three-backtick example.
func (l *linter) fences() {
	l.inFence = make([]bool, len(l.plan.Lines))
	var openChar byte
	openLen := 0
	for i, line := range l.plan.Lines {
		m := fencePattern.FindStringSubmatch(line)
		switch {
		case m == nil:
			l.inFence[i] = openLen > 0
		case openLen == 0:
			openChar, openLen = m[1][0], len(m[1])
			l.inFence[i] = true
		case m[1][0] == openChar && len(m[1]) >= openLen && strings.TrimSpace(m[2]) == "":
			openLen = 0
			l.inFence[i] = true
		default:
			l.inFence[i] = true
		}
	}
}

func planBase(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

func (l *linter) add(category review.Category, check, title, description, recommendation string, lines []int) {
	if len(lines) == 0 {
		return
	}
	iss := review.Issue{
		ID:             fmt.Sprintf("ISSUE-LINT-%04d", len(l.issues)+1),
		Severity:       review.SeverityInfo,
		Category:       category,
		Title:          title,
		Impact:         "Found by a local text check, not by the model; confirm before acting.",
		Recommendation: recommendation,
		Blocking:       false,
		Tags:           []string{"local", check},
	}
	setOccurrences(&iss, description, lines, l.plan.Lines, l.path)
	l.issues = append(l.issues, iss)
}

// setOccurrences records every occurrence on iss and derives the capped
// evidence and the description's occurrence note from them. The first
// maxEvidence lines are kept in the order given (bothSides relies on
// this) and only those are sorted for display.
func setOccurrences(iss *review.Issue, description string, lines []int, planLines []string, path string) {
	iss.Occurrences = append([]int(nil), lines...)
	kept := append([]int(nil), lines[:min(len(lines), maxEvidence)]...)
	sort.Ints(kept)
	iss.Evidence = make([]review.Evidence, 0, len(kept))
	for _, n := range kept {
		iss.Evidence = append(iss.Evidence, review.Evidence{Source: "plan", Path: path, LineStart: n, LineEnd: n, Quote: planLines[n-1]})
	}
	if len(lines) > maxEvidence {
		description += fmt.Sprintf(" (%d occurrences; first %d cited)", len(lines), maxEvidence)
	}
	iss.Description = description
}

// phrasePattern matches phrase as whole words, case-insensitively, with
// Unicode-aware boundaries so "fast" does not match "breakfast" and
// "etc." still matches at a line end.
func phrasePattern(phrase string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)(^|[^\pL\pN])` + regexp.QuoteMeta(strings.TrimSpace(phrase)) + `($|[^\pL\pN])`)
}

func (l *linter) linesMatching(re *regexp.Regexp) []int {
	var lines []int
	for i, line := range l.plan.Lines {
		if re.MatchString(line) {
			lines = append(lines, i+1)
		}
	}
	return lines
}

func (l *linter) triggers(phrases []string) {
	seen := make(map[string]bool)
	for _, phrase := range phrases {
		phrase = strings.TrimSpace(phrase)
		key := strings.ToLower(phrase)
		if phrase == "" || seen[key] {
			continue
		}
		seen[key] = true
		lines := l.linesMatching(phrasePattern(phrase))
		l.add(review.CategoryAmbiguity, "trigger-phrase",
			fmt.Sprintf("Vague phrase %q", phrase),
			fmt.Sprintf("The profile lists %q as a phrase that usually hides an unstated, unmeasurable requirement.", phrase),
			"Replace it with a measurable criterion or a concrete mechanism, or delete it.",
			lines)
	}
}

func (l *linter) contradictions(pairs []profile.Contradiction) {
	for _, c := range pairs {
		if strings.TrimSpace(c.TriggerA) == "" || strings.TrimSpace(c.TriggerB) == "" {
			continue
		}
		a := l.linesMatching(phrasePattern(c.TriggerA))
		b := l.linesMatching(phrasePattern(c.TriggerB))
		if len(a) == 0 || len(b) == 0 {
			continue
		}
		desc := fmt.Sprintf("The plan contains both %q and %q, which the profile marks as a %s-level contradiction when confirmed. %s", c.TriggerA, c.TriggerB, strings.ToUpper(strings.TrimSpace(c.Severity)), strings.TrimSpace(c.Note))
		l.add(review.CategoryContradiction, "contradiction-pair",
			fmt.Sprintf("Possible contradiction: %q vs %q", c.TriggerA, c.TriggerB),
			strings.TrimSpace(desc),
			"Confirm whether both statements are intended; if so, reconcile them in the plan text.",
			bothSides(a, b))
	}
}

// bothSides merges two citation lists so that, after the evidence cap,
// each side of a contradiction is still represented: up to half the
// budget from each list first, then the remainder, with duplicates
// removed. The total count is still reported from the full union.
func bothSides(a, b []int) []int {
	seen := make(map[int]bool)
	var out []int
	take := func(src []int, n int) {
		for _, x := range src {
			if n == 0 {
				return
			}
			if !seen[x] {
				seen[x] = true
				out = append(out, x)
				n--
			}
		}
	}
	half := maxEvidence / 2
	take(a, half)
	take(b, half)
	take(a, len(a))
	take(b, len(b))
	return out
}

var placeholderPattern = regexp.MustCompile(`(?i)\b(todo|tbd|fixme|tk)\b|\bXXX\b|\?\?\?`)

func (l *linter) placeholders() {
	l.add(review.CategoryAmbiguity, "placeholder",
		"Unresolved placeholder",
		"The plan still contains a TODO/TBD/FIXME-style marker, which means a decision was deferred.",
		"Resolve the placeholder or turn it into an explicit open question.",
		l.linesMatching(placeholderPattern))
}

var (
	headingPattern  = regexp.MustCompile(`^ {0,3}(#{1,6})\s+(.*\S)\s*$`) // CommonMark ATX: up to 3 leading spaces
	closingHashes   = regexp.MustCompile(`\s+#+\s*$`)                    // optional ATX closing sequence
	numberingPrefix = regexp.MustCompile(`^(\d+[.)]|phase\s+\d+[:.)-]?)\s*`)
	phaseHeading    = regexp.MustCompile(`(?i)^phase\s+(\d+)\b`)
	phaseMention    = regexp.MustCompile(`(?i)\bphase\s+(\d+)\b`)
	criteriaPattern = regexp.MustCompile(`(?i)\b(acceptance|criteria|done when|definition of done|exit condition)\b`)
)

type heading struct {
	line  int // 1-based
	level int
	text  string
}

func (l *linter) headings() []heading {
	var hs []heading
	for i, line := range l.plan.Lines {
		if l.inFence[i] {
			continue
		}
		if m := headingPattern.FindStringSubmatch(line); m != nil {
			text := closingHashes.ReplaceAllString(m[2], "")
			hs = append(hs, heading{line: i + 1, level: len(m[1]), text: text})
		}
	}
	return hs
}

// parentOf returns the index of the nearest preceding heading with a
// lower level, or -1 at the top.
func parentOf(hs []heading, i int) int {
	for j := i - 1; j >= 0; j-- {
		if hs[j].level < hs[i].level {
			return j
		}
	}
	return -1
}

func normalizeHeading(s string) string {
	s = strings.ToLower(strings.Join(strings.Fields(s), " "))
	return strings.TrimSpace(numberingPrefix.ReplaceAllString(s, ""))
}

// sectionEnd returns the last line (1-based, inclusive) of the section
// that starts at hs[i]: up to the next heading of the same or a higher
// level, or the end of the plan.
func (l *linter) sectionEnd(hs []heading, i int) int {
	for j := i + 1; j < len(hs); j++ {
		if hs[j].level <= hs[i].level {
			return hs[j].line - 1
		}
	}
	return len(l.plan.Lines)
}

func (l *linter) structure() {
	hs := l.headings()

	// Empty sections: nothing but blank lines before the next heading of
	// the same or a higher level (a parent heading that only introduces
	// subsections is fine).
	var empty []int
	for i, h := range hs {
		end := l.sectionEnd(hs, i)
		hasChild := i+1 < len(hs) && hs[i+1].line <= end && hs[i+1].level > h.level
		if hasChild {
			continue
		}
		content := false
		for n := h.line + 1; n <= end; n++ {
			if strings.TrimSpace(l.plan.Lines[n-1]) != "" {
				content = true
				break
			}
		}
		if !content {
			empty = append(empty, h.line)
		}
	}
	l.add(review.CategoryAmbiguity, "empty-section",
		"Empty section",
		"A heading has no content beneath it, so the plan promises a section it does not deliver.",
		"Fill the section in or remove the heading.",
		empty)

	// Duplicate headings among siblings. The same subsection title under
	// different parents ("Tests" in every phase) is normal structure;
	// two "Overview" sections under one parent are not.
	type dupKey struct {
		parent int
		text   string
	}
	byKey := make(map[dupKey][]int)
	var order []dupKey
	for i, h := range hs {
		key := dupKey{parent: parentOf(hs, i), text: normalizeHeading(h.text)}
		if key.text == "" {
			continue
		}
		if _, ok := byKey[key]; !ok {
			order = append(order, key)
		}
		byKey[key] = append(byKey[key], h.line)
	}
	for _, key := range order {
		if lines := byKey[key]; len(lines) > 1 {
			l.add(review.CategoryAmbiguity, "duplicate-heading",
				fmt.Sprintf("Duplicate heading %q", key.text),
				"The same heading appears more than once in one section, so references to it are ambiguous.",
				"Merge the sections or give each a distinct heading.",
				lines)
		}
	}

	// Phases: references to undefined phases, and phases without any
	// acceptance criteria. Both apply only to plans that use phases.
	defined := make(map[int]bool)
	var phases []int // indexes into hs
	for i, h := range hs {
		if m := phaseHeading.FindStringSubmatch(h.text); m != nil {
			n, _ := strconv.Atoi(m[1])
			defined[n] = true
			phases = append(phases, i)
		}
	}
	if len(phases) == 0 {
		return
	}
	undefined := make(map[int][]int)
	for i, line := range l.plan.Lines {
		if l.inFence[i] {
			continue
		}
		for _, m := range phaseMention.FindAllStringSubmatch(line, -1) {
			n, _ := strconv.Atoi(m[1])
			if !defined[n] {
				undefined[n] = append(undefined[n], i+1)
			}
		}
	}
	nums := make([]int, 0, len(undefined))
	for n := range undefined {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	for _, n := range nums {
		l.add(review.CategoryOrderingDependency, "undefined-phase",
			fmt.Sprintf("Reference to undefined Phase %d", n),
			fmt.Sprintf("The plan refers to Phase %d but has no heading that defines it.", n),
			"Add the phase or fix the reference.",
			undefined[n])
	}
	{
		var missing []int
		for _, i := range phases {
			end := l.sectionEnd(hs, i)
			found := false
			for n := hs[i].line + 1; n <= end; n++ {
				if !l.inFence[n-1] && criteriaPattern.MatchString(l.plan.Lines[n-1]) {
					found = true
					break
				}
			}
			if !found {
				missing = append(missing, hs[i].line)
			}
		}
		l.add(review.CategoryMissingAcceptanceCriteria, "phase-without-criteria",
			"Phase without acceptance criteria",
			"The phase says nothing about acceptance, criteria, or a definition of done, so there is no observable way to tell when it is finished.",
			"Add measurable acceptance criteria to each phase.",
			missing)
	}
}
