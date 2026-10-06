// Package context handles reading and line-numbering context files.
package context

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// File holds a loaded context file with its content and metadata.
type File struct {
	FilePath string
	Raw      string
	Lines    []string
	Hash     string
	// Role marks what the file is to the review. Empty is plain
	// grounding context; "spec" is the specification the plan must
	// implement, which turns on the coverage matrix.
	Role string
}

// RoleSpec is the Role of the specification file given with --spec.
const RoleSpec = "spec"

// Load reads a context file and computes its SHA-256 hash.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("context.Load: %w", err)
	}
	raw := string(data)
	h := sha256.Sum256(data)
	return &File{
		FilePath: path,
		Raw:      raw,
		Lines:    strings.Split(raw, "\n"),
		Hash:     fmt.Sprintf("sha256:%x", h),
	}, nil
}

// LineNumbered returns the context text with each line prefixed by its
// 1-based number and a bar ("12|text"). The compact prefix costs about
// 2.5 fewer tokens per line than the earlier zero-padded "L012: " form
// (measured: 19,834 vs 17,173 tokens for this repository's PLAN.md plus
// SPEC.md), and because every line starts with a digit the content still
// cannot forge a ##PLANCRITIC_* delimiter.
func LineNumbered(f *File) string {
	var b strings.Builder
	for i, line := range f.Lines {
		b.WriteString(strconv.Itoa(i + 1))
		b.WriteByte('|')
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}
