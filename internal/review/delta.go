package review

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Delta describes how a review differs from a baseline review of an
// earlier revision of the same plan. Findings are matched by
// fingerprint, never by ID.
type Delta struct {
	BaselineFile     string       `json:"baseline_file"`
	BaselinePlanHash string       `json:"baseline_plan_hash,omitempty"`
	ScoreChange      int          `json:"score_change"`
	Resolved         []DeltaEntry `json:"resolved"`
	New              []DeltaEntry `json:"new"`
	Persisting       []DeltaEntry `json:"persisting"`
}

// DeltaEntry names one finding in a Delta. For resolved entries the ID
// and title come from the baseline; otherwise from the current review.
type DeltaEntry struct {
	Kind        string   `json:"kind"` // "issue" or "question"
	ID          string   `json:"id"`
	Fingerprint string   `json:"fingerprint"`
	Severity    Severity `json:"severity"`
	Title       string   `json:"title"`
}

// LoadBaseline reads a review JSON file written by an earlier run.
// Findings that lack a fingerprint (older output) get one computed from
// their evidence quotes, so any prior JSON output can serve as a baseline
// as long as it kept its quotes. A finding with neither a fingerprint nor
// quotes is rejected: a fingerprint computed from empty quotes would
// match unrelated findings and make the delta lie.
func LoadBaseline(path string) (*Review, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("baseline: %w", err)
	}
	var r Review
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("baseline %s: not a plancritic review: %w", path, err)
	}
	if r.Tool != "plancritic" {
		return nil, fmt.Errorf("baseline %s: not a plancritic review (tool=%q)", path, r.Tool)
	}
	for i := range r.Issues {
		if r.Issues[i].Fingerprint == "" {
			if !hasQuotes(r.Issues[i].Evidence) {
				return nil, fmt.Errorf("baseline %s: issue %s has no fingerprint and no evidence quotes (written with --no-quotes by an older version?); re-run that revision without --no-quotes to use it as a baseline", path, r.Issues[i].ID)
			}
			r.Issues[i].Fingerprint = IssueFingerprint(r.Issues[i])
		}
	}
	for i := range r.Questions {
		if r.Questions[i].Fingerprint == "" {
			if !hasQuotes(r.Questions[i].Evidence) {
				return nil, fmt.Errorf("baseline %s: question %s has no fingerprint and no evidence quotes; re-run that revision without --no-quotes to use it as a baseline", path, r.Questions[i].ID)
			}
			r.Questions[i].Fingerprint = QuestionFingerprint(r.Questions[i])
		}
	}
	return &r, nil
}

// hasQuotes reports whether evs is non-empty and every entry carries a
// quote, the precondition for reconstructing a fingerprint.
func hasQuotes(evs []Evidence) bool {
	if len(evs) == 0 {
		return false
	}
	for _, ev := range evs {
		if strings.TrimSpace(ev.Quote) == "" {
			return false
		}
	}
	return true
}

// ComputeDelta compares current against baseline. Both must already
// carry fingerprints. Fingerprints are matched as multisets: when two
// distinct findings share one fingerprint (same category, same cited
// text), each baseline occurrence pairs with at most one current
// occurrence, so an extra copy on either side is reported as resolved
// or new rather than silently absorbed. Entries are reported in the
// order the findings appear in their respective review; the slices are
// never nil so JSON consumers always see arrays.
func ComputeDelta(baseline, current *Review, baselineFile string) *Delta {
	d := &Delta{
		BaselineFile:     filepath.Base(baselineFile),
		BaselinePlanHash: baseline.Input.PlanHash,
		ScoreChange:      current.Summary.Score - baseline.Summary.Score,
		Resolved:         []DeltaEntry{},
		New:              []DeltaEntry{},
		Persisting:       []DeltaEntry{},
	}

	// Occurrence counts per kind:fingerprint on each side.
	baseCount := make(map[string]int)
	for _, iss := range baseline.Issues {
		baseCount["issue:"+iss.Fingerprint]++
	}
	for _, q := range baseline.Questions {
		baseCount["question:"+q.Fingerprint]++
	}
	curCount := make(map[string]int)
	for _, iss := range current.Issues {
		curCount["issue:"+iss.Fingerprint]++
	}
	for _, q := range current.Questions {
		curCount["question:"+q.Fingerprint]++
	}

	// Walk current findings, consuming baseline occurrences as they match.
	unmatchedBase := make(map[string]int, len(baseCount))
	for k, n := range baseCount {
		unmatchedBase[k] = n
	}
	classify := func(e DeltaEntry) {
		key := e.Kind + ":" + e.Fingerprint
		if unmatchedBase[key] > 0 {
			unmatchedBase[key]--
			d.Persisting = append(d.Persisting, e)
		} else {
			d.New = append(d.New, e)
		}
	}
	for _, iss := range current.Issues {
		classify(DeltaEntry{Kind: "issue", ID: iss.ID, Fingerprint: iss.Fingerprint, Severity: iss.Severity, Title: iss.Title})
	}
	for _, q := range current.Questions {
		classify(DeltaEntry{Kind: "question", ID: q.ID, Fingerprint: q.Fingerprint, Severity: q.Severity, Title: q.Question})
	}

	// Walk baseline findings; the first min(base, current) occurrences of
	// a fingerprint were matched, the rest are resolved.
	matched := make(map[string]int, len(baseCount))
	for k, n := range baseCount {
		matched[k] = min(n, curCount[k])
	}
	resolve := func(e DeltaEntry) {
		key := e.Kind + ":" + e.Fingerprint
		if matched[key] > 0 {
			matched[key]--
			return
		}
		d.Resolved = append(d.Resolved, e)
	}
	for _, iss := range baseline.Issues {
		resolve(DeltaEntry{Kind: "issue", ID: iss.ID, Fingerprint: iss.Fingerprint, Severity: iss.Severity, Title: iss.Title})
	}
	for _, q := range baseline.Questions {
		resolve(DeltaEntry{Kind: "question", ID: q.ID, Fingerprint: q.Fingerprint, Severity: q.Severity, Title: q.Question})
	}
	return d
}

// Status returns "new", "persisting", or "" for the current finding
// with the given kind and ID. IDs are unique within a run, so this is
// exact even when several findings share a fingerprint.
func (d *Delta) Status(kind, id string) string {
	if d == nil {
		return ""
	}
	for _, e := range d.Persisting {
		if e.Kind == kind && e.ID == id {
			return "persisting"
		}
	}
	for _, e := range d.New {
		if e.Kind == kind && e.ID == id {
			return "new"
		}
	}
	return ""
}
