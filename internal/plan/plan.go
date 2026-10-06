// Package plan handles reading, hashing, and line-numbering plan files.
package plan

import (
	"crypto/sha256"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Plan holds a loaded plan file with its content and metadata.
type Plan struct {
	FilePath string
	Raw      string
	Lines    []string
	Hash     string
}

// StepID represents an inferred plan step identifier.
type StepID struct {
	ID        string
	LineStart int
	LineEnd   int
	Text      string
}

// Load reads a plan file and computes its SHA-256 hash.
func Load(path string) (*Plan, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("plan.Load: %w", err)
	}
	raw := string(data)
	h := sha256.Sum256(data)
	return &Plan{
		FilePath: path,
		Raw:      raw,
		Lines:    strings.Split(raw, "\n"),
		Hash:     fmt.Sprintf("sha256:%x", h),
	}, nil
}

// LineNumbered returns the plan text with each line prefixed by its
// 1-based number and a bar ("12|text"). The compact prefix costs about
// 2.5 fewer tokens per line than the earlier zero-padded "L012: " form
// (measured: 19,834 vs 17,173 tokens for this repository's PLAN.md plus
// SPEC.md), and because every line starts with a digit the content still
// cannot forge a ##PLANCRITIC_* delimiter.
func LineNumbered(p *Plan) string {
	var b strings.Builder
	for i, line := range p.Lines {
		b.WriteString(strconv.Itoa(i + 1))
		b.WriteByte('|')
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

var (
	// Markdown heading: ## Title or ## 1. Title
	headingPattern = regexp.MustCompile(`^#{1,6}\s+(?:\d+[\.\)]\s*)?(.+)`)
	// Numbered bullet: 1. Step text
	numberedPattern = regexp.MustCompile(`^\d+[\.\)]\s+(.+)`)
)

// InferStepIDs scans the plan for markdown headings and numbered list
// items and assigns P-NNN IDs in document order.
//
// Dash bullets ("- text") are deliberately not treated as steps. In
// real plans they are overwhelmingly sub-items (file lists, risks,
// acceptance criteria) rather than steps, and including them made the
// step index the single largest block of the prompt — a near-verbatim
// second copy of the plan that the model already receives line-numbered.
func InferStepIDs(p *Plan) []StepID {
	var steps []StepID
	seq := 1

	for i, line := range p.Lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		var text string
		switch {
		case headingPattern.MatchString(trimmed):
			text = headingPattern.FindStringSubmatch(trimmed)[1]
		case numberedPattern.MatchString(trimmed):
			text = numberedPattern.FindStringSubmatch(trimmed)[1]
		default:
			continue
		}

		steps = append(steps, StepID{
			ID:        fmt.Sprintf("P-%03d", seq),
			LineStart: i + 1,
			LineEnd:   i + 1,
			Text:      strings.TrimSpace(text),
		})
		seq++
	}

	return steps
}
