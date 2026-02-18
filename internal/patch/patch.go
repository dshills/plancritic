// Package patch writes unified diffs from review patches to a file.
package patch

import (
	"fmt"
	"os"
	"strings"

	"github.com/dshills/plancritic/internal/review"
)

// WritePatchFile writes all patch diffs to the given path.
// If there are no patches, no file is created.
func WritePatchFile(patches []review.Patch, outPath string) error {
	if len(patches) == 0 {
		return nil
	}

	var b strings.Builder
	for _, p := range patches {
		b.WriteString(p.DiffUnified)
		if !strings.HasSuffix(p.DiffUnified, "\n") {
			b.WriteString("\n")
		}
	}

	if err := os.WriteFile(outPath, []byte(b.String()), 0644); err != nil {
		return fmt.Errorf("patch.WritePatchFile: %w", err)
	}
	return nil
}
