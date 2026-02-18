package context

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "ctx.md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad(t *testing.T) {
	content := "constraint one\nconstraint two"
	path := writeTempFile(t, content)

	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Lines) != 2 {
		t.Errorf("expected 2 lines, got %d", len(f.Lines))
	}
	if !strings.HasPrefix(f.Hash, "sha256:") {
		t.Errorf("expected sha256 prefix, got %s", f.Hash)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load("/nonexistent/ctx.md")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLineNumbered(t *testing.T) {
	f := &File{FilePath: "ctx.md", Lines: []string{"alpha", "beta"}}
	got := LineNumbered(f)
	if !strings.Contains(got, "L001: alpha") {
		t.Errorf("expected L001 prefix, got:\n%s", got)
	}
	if !strings.Contains(got, "L002: beta") {
		t.Errorf("expected L002 prefix, got:\n%s", got)
	}
}
