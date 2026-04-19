package review

import (
	"path"
	"strings"
)

// NormalizeContextPath canonicalizes a path used to key a context
// file across the three sites that need to agree: where check.go
// builds the map, where schema.Validate looks up line counts, and
// where ReconstructQuotes resolves lines. Backslashes are replaced
// with forward slashes explicitly (rather than filepath.ToSlash,
// which only converts on Windows) so an LLM-emitted Windows-style
// path resolves the same way regardless of the host OS. The result
// is the final path component.
func NormalizeContextPath(p0 string) string {
	return path.Base(strings.ReplaceAll(p0, "\\", "/"))
}

// QuoteSource supplies the line text that Evidence citations refer to.
// PlanLines is the plan's lines, 0-indexed (line_start=1 maps to
// PlanLines[0]). ContextsByBasename maps a context file's base name
// (the same name used when embedding it into the prompt) to its lines.
type QuoteSource struct {
	PlanLines          []string
	ContextsByBasename map[string][]string
}

// unavailableQuote marks evidence whose citation could not be resolved
// to a backing source (e.g. the LLM named a path that wasn't provided
// or a line range outside the source's bounds). Surfacing this in the
// output is more useful than leaving Quote empty, since downstream
// renderers assume the field is non-empty.
const unavailableQuote = "(quote unavailable)"

// ReconstructQuotes walks every Evidence in the review and populates
// its Quote field from QuoteSource. Any existing Quote is overwritten:
// the plan/context text is authoritative. Returns the count of
// evidence entries that could not be resolved (for verbose logging).
func ReconstructQuotes(r *Review, src QuoteSource) int {
	misses := 0
	for i := range r.Issues {
		for j := range r.Issues[i].Evidence {
			if !fillQuote(&r.Issues[i].Evidence[j], src) {
				misses++
			}
		}
	}
	for i := range r.Questions {
		for j := range r.Questions[i].Evidence {
			if !fillQuote(&r.Questions[i].Evidence[j], src) {
				misses++
			}
		}
	}
	return misses
}

// fillQuote resolves ev against src and sets ev.Quote. Returns false
// if the source/path couldn't be found or the line range was invalid.
func fillQuote(ev *Evidence, src QuoteSource) bool {
	lines, ok := resolveLines(ev, src)
	if !ok {
		ev.Quote = unavailableQuote
		return false
	}
	// Evidence line numbers are 1-indexed and inclusive on both ends.
	start := ev.LineStart - 1
	end := ev.LineEnd
	if start < 0 || start >= end || end > len(lines) {
		ev.Quote = unavailableQuote
		return false
	}
	ev.Quote = strings.Join(lines[start:end], "\n")
	return true
}

func resolveLines(ev *Evidence, src QuoteSource) ([]string, bool) {
	switch ev.Source {
	case "plan":
		if src.PlanLines == nil {
			return nil, false
		}
		return src.PlanLines, true
	case "context":
		lines, ok := src.ContextsByBasename[NormalizeContextPath(ev.Path)]
		return lines, ok
	default:
		return nil, false
	}
}
