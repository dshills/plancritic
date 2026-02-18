// Package context handles reading and line-numbering context files.
package context

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
)

// File holds a loaded context file with its content and metadata.
type File struct {
	FilePath string
	Raw      string
	Lines    []string
	Hash     string
}

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

// LineNumbered returns the context text with each line prefixed by L-padded numbers.
func LineNumbered(f *File) string {
	width := lineNumberWidth(len(f.Lines))
	format := fmt.Sprintf("L%%0%dd: %%s\n", width)
	var b strings.Builder
	for i, line := range f.Lines {
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
