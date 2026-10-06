package review

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"sort"
	"strings"
)

// Fingerprints identify a finding across runs. Issue IDs are assigned
// fresh by the model every time, so an agent that revises the plan and
// re-runs cannot otherwise tell which findings it resolved, which
// persist, and which are new.
//
// A fingerprint hashes what a finding is *about* rather than how the
// model happened to word it: the category plus, for each evidence
// entry, the source, the file, and the cited text normalized for case
// and whitespace. Line numbers are deliberately excluded so that edits
// elsewhere in the plan do not change every fingerprint below them.
// Editing the cited lines themselves changes the fingerprint, which is
// the intended signal that the finding was acted on.
//
// Context files are identified by basename because that is all the
// model ever sees: the prompt labels each context block with its
// basename and evidence paths come back the same way. Two context files
// sharing a basename are therefore indistinguishable here as everywhere
// else in the pipeline; the reviewer warns on stderr when that happens.

const fingerprintLen = 12 // hex chars; 48 bits is plenty for a review

// IssueFingerprint returns the fingerprint for iss.
func IssueFingerprint(iss Issue) string {
	return fingerprint("issue", string(iss.Category), iss.Evidence)
}

// QuestionFingerprint returns the fingerprint for q.
func QuestionFingerprint(q Question) string {
	return fingerprint("question", "", q.Evidence)
}

// AssignFingerprints fills the Fingerprint field of every issue and
// question. It is idempotent and expects evidence quotes to be present
// (run it after ReconstructQuotes).
func AssignFingerprints(r *Review) {
	for i := range r.Issues {
		r.Issues[i].Fingerprint = IssueFingerprint(r.Issues[i])
	}
	for i := range r.Questions {
		r.Questions[i].Fingerprint = QuestionFingerprint(r.Questions[i])
	}
}

func fingerprint(kind, category string, evs []Evidence) string {
	// Canonicalize each evidence entry to a tuple and hash the tuples in
	// sorted order, so the model citing the same places in a different
	// order on the next run does not change the fingerprint. The
	// original slice is not touched.
	tuples := make([]string, 0, len(evs))
	for _, ev := range evs {
		file := "plan"
		if ev.Source != "plan" {
			file = path.Base(strings.ReplaceAll(ev.Path, "\\", "/"))
		}
		tuples = append(tuples, ev.Source+"\x00"+file+"\x00"+normalizeText(ev.Quote))
	}
	sort.Strings(tuples)

	h := sha256.New()
	h.Write([]byte(kind))
	h.Write([]byte{0})
	h.Write([]byte(category))
	for _, t := range tuples {
		h.Write([]byte{0, 1}) // tuple separator distinct from the field separator
		h.Write([]byte(t))
	}
	return hex.EncodeToString(h.Sum(nil))[:fingerprintLen]
}

// normalizeText lowercases and collapses whitespace so that reflowed or
// re-indented lines still match.
func normalizeText(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}
