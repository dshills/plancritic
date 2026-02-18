// Package plan handles reading, hashing, and line-numbering plan files.
package plan

import (
	"crypto/sha256"
	"fmt"
	"os"
	"regexp"
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

// LineNumbered returns the plan text with each line prefixed by L-padded numbers.
// The width adjusts based on total line count.
func LineNumbered(p *Plan) string {
	width := lineNumberWidth(len(p.Lines))
	format := fmt.Sprintf("L%%0%dd: %%s\n", width)
	var b strings.Builder
	for i, line := range p.Lines {
		fmt.Fprintf(&b, format, i+1, line)
	}
	return b.String()
}

func lineNumberWidth(totalLines int) int {
	switch {
	case totalLines >= 10000:
		return 5
	case totalLines >= 1000:
		return 4
	default:
		return 3
	}
}

var (
	// Markdown heading: ## Title or ## 1. Title
	headingPattern = regexp.MustCompile(`^#{1,6}\s+(?:\d+[\.\)]\s*)?(.+)`)
	// Numbered bullet: 1. Step text
	numberedPattern = regexp.MustCompile(`^\d+[\.\)]\s+(.+)`)
	// Dash bullet: - Step text
	dashPattern = regexp.MustCompile(`^-\s+(.+)`)
)

// InferStepIDs scans the plan for numbered headings or bullets and assigns P-NNN IDs.
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
		case dashPattern.MatchString(trimmed):
			text = dashPattern.FindStringSubmatch(trimmed)[1]
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
