package patch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dshills/plancritic/internal/review"
)

func TestWritePatchFile(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "patch.diff")

	patches := []review.Patch{
		{ID: "P-1", DiffUnified: "--- a\n+++ b\n@@ -1 +1 @@\n-old\n+new"},
		{ID: "P-2", DiffUnified: "--- c\n+++ d\n@@ -1 +1 @@\n-foo\n+bar\n"},
	}

	err := WritePatchFile(patches, out)
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}

	content := string(data)
	if !strings.Contains(content, "-old") || !strings.Contains(content, "+bar") {
		t.Errorf("patch file content unexpected: %s", content)
	}
}

func TestWritePatchFileEmpty(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "patch.diff")

	err := WritePatchFile(nil, out)
	if err != nil {
		t.Fatal(err)
	}

	// File should not exist
	if _, err := os.Stat(out); err == nil {
		t.Error("expected no file for empty patches")
	}
}
