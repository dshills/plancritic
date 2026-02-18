package plan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad(t *testing.T) {
	content := "line one\nline two\nline three"
	path := writeTempFile(t, content)

	p, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Lines) != 3 {
		t.Errorf("expected 3 lines, got %d", len(p.Lines))
	}
	if !strings.HasPrefix(p.Hash, "sha256:") {
		t.Errorf("expected sha256 prefix, got %s", p.Hash)
	}
	if p.Raw != content {
		t.Error("raw content mismatch")
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load("/nonexistent/file.md")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLineNumbered(t *testing.T) {
	p := &Plan{Lines: []string{"first", "second", "third"}}
	got := LineNumbered(p)
	if !strings.Contains(got, "L001: first") {
		t.Errorf("expected L001 prefix, got:\n%s", got)
	}
	if !strings.Contains(got, "L003: third") {
		t.Errorf("expected L003 prefix, got:\n%s", got)
	}
}

func TestInferStepIDs(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    int
	}{
		{"numbered headings", "# 1. Setup\n# 2. Build\n# 3. Deploy", 3},
		{"numbered bullets", "1. First step\n2. Second step", 2},
		{"dash bullets", "- Alpha\n- Beta\n- Gamma", 3},
		{"markdown headings", "## Overview\n## Implementation", 2},
		{"empty", "", 0},
		{"mixed", "# Intro\n1. Step one\n- Detail", 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plan{Lines: strings.Split(tt.content, "\n")}
			steps := InferStepIDs(p)
			if len(steps) != tt.want {
				t.Errorf("got %d steps, want %d", len(steps), tt.want)
			}
			for i, s := range steps {
				if s.ID == "" {
					t.Errorf("step %d has empty ID", i)
				}
			}
		})
	}
}
