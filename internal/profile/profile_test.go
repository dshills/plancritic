package profile

import (
	"strings"
	"testing"
)

func TestLoadBuiltinAll(t *testing.T) {
	names := []string{"general", "go-backend", "react-frontend", "aws-deploy", "davin-go"}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			p, err := LoadBuiltin(name)
			if err != nil {
				t.Fatalf("LoadBuiltin(%q): %v", name, err)
			}
			if p.Name == "" {
				t.Error("profile name is empty")
			}
			if len(p.Checklists) == 0 {
				t.Error("profile has no checklists")
			}
			for _, cl := range p.Checklists {
				if cl.ID == "" {
					t.Error("checklist has empty ID")
				}
				if len(cl.Checks) == 0 {
					t.Errorf("checklist %s has no checks", cl.ID)
				}
			}
		})
	}
}

func TestLoadBuiltinNotFound(t *testing.T) {
	_, err := LoadBuiltin("nonexistent")
	if err == nil {
		t.Error("expected error for unknown profile")
	}
}

func TestList(t *testing.T) {
	names, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) < 4 {
		t.Errorf("expected at least 4 profiles, got %d", len(names))
	}
	required := map[string]bool{"general": false, "go-backend": false, "react-frontend": false, "aws-deploy": false}
	for _, n := range names {
		required[n] = true
	}
	for name, found := range required {
		if !found {
			t.Errorf("missing required profile: %s", name)
		}
	}
}

func TestFormatForPrompt(t *testing.T) {
	p, err := LoadBuiltin("go-backend")
	if err != nil {
		t.Fatal(err)
	}

	text := FormatForPrompt(p)

	checks := []string{
		"## Profile: go-backend",
		"### Checklists",
		"CRISP_CONTRACTS",
		"DEPENDENCY_DISCIPLINE",
		"### Heuristics",
		"dependency-free",
	}
	for _, want := range checks {
		if !strings.Contains(text, want) {
			t.Errorf("prompt text missing %q", want)
		}
	}
}
