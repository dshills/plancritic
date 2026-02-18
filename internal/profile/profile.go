// Package profile handles loading and formatting built-in review profiles.
package profile

import (
	"embed"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed builtin/*.yaml
var builtinFS embed.FS

// Profile defines a set of constraints and checklists for plan review.
type Profile struct {
	Name        string                 `yaml:"name"`
	Version     int                    `yaml:"version"`
	Description string                 `yaml:"description"`
	Constraints map[string]interface{} `yaml:"constraints"`
	Checklists  []Checklist            `yaml:"checklists"`
	Heuristics  Heuristics             `yaml:"heuristics"`
}

// Checklist is a named group of checks.
type Checklist struct {
	ID     string   `yaml:"id"`
	Title  string   `yaml:"title"`
	Checks []string `yaml:"checks"`
}

// Heuristics defines pattern-based triggers.
type Heuristics struct {
	Contradictions    []Contradiction `yaml:"contradictions"`
	AmbiguityTriggers []string        `yaml:"ambiguity_triggers"`
}

// Contradiction defines a pair of phrases that indicate a plan contradiction.
type Contradiction struct {
	TriggerA string `yaml:"trigger_a"`
	TriggerB string `yaml:"trigger_b"`
	Severity string `yaml:"severity"`
	Note     string `yaml:"note"`
}

// LoadBuiltin loads a built-in profile by name.
func LoadBuiltin(name string) (*Profile, error) {
	filename := name + ".yaml"
	data, err := builtinFS.ReadFile("builtin/" + filename)
	if err != nil {
		return nil, fmt.Errorf("profile.LoadBuiltin: unknown profile %q: %w", name, err)
	}
	var p Profile
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("profile.LoadBuiltin: parse %q: %w", name, err)
	}
	return &p, nil
}

// List returns the names of all available built-in profiles.
func List() ([]string, error) {
	entries, err := builtinFS.ReadDir("builtin")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasSuffix(n, ".yaml") {
			names = append(names, strings.TrimSuffix(n, ".yaml"))
		}
	}
	return names, nil
}

// FormatForPrompt renders the profile into text suitable for inclusion in the LLM prompt.
func FormatForPrompt(p *Profile) string {
	var b strings.Builder

	fmt.Fprintf(&b, "## Profile: %s\n\n", p.Name)
	if p.Description != "" {
		fmt.Fprintf(&b, "%s\n\n", strings.TrimSpace(p.Description))
	}

	// Render constraints as YAML-like text
	if len(p.Constraints) > 0 {
		b.WriteString("### Constraints\n\n")
		renderConstraints(&b, p.Constraints, "")
		b.WriteString("\n")
	}

	// Render checklists
	if len(p.Checklists) > 0 {
		b.WriteString("### Checklists\n\n")
		for _, cl := range p.Checklists {
			fmt.Fprintf(&b, "**%s** (%s)\n", cl.Title, cl.ID)
			for _, check := range cl.Checks {
				fmt.Fprintf(&b, "- %s\n", check)
			}
			b.WriteString("\n")
		}
	}

	// Render heuristics
	if len(p.Heuristics.Contradictions) > 0 || len(p.Heuristics.AmbiguityTriggers) > 0 {
		b.WriteString("### Heuristics\n\n")
		if len(p.Heuristics.Contradictions) > 0 {
			b.WriteString("Watch for these contradiction pairs:\n")
			for _, c := range p.Heuristics.Contradictions {
				fmt.Fprintf(&b, "- %q vs %q → %s (%s)\n", c.TriggerA, c.TriggerB, c.Severity, c.Note)
			}
			b.WriteString("\n")
		}
		if len(p.Heuristics.AmbiguityTriggers) > 0 {
			b.WriteString("Flag these vague phrases as ambiguity:\n")
			for _, trigger := range p.Heuristics.AmbiguityTriggers {
				fmt.Fprintf(&b, "- %q\n", trigger)
			}
			b.WriteString("\n")
		}
	}

	return b.String()
}

func renderConstraints(b *strings.Builder, m map[string]interface{}, indent string) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		val := m[key]
		switch v := val.(type) {
		case map[string]interface{}:
			fmt.Fprintf(b, "%s- %s:\n", indent, key)
			renderConstraints(b, v, indent+"  ")
		case []interface{}:
			fmt.Fprintf(b, "%s- %s:\n", indent, key)
			for _, item := range v {
				fmt.Fprintf(b, "%s  - %v\n", indent, item)
			}
		default:
			fmt.Fprintf(b, "%s- %s: %v\n", indent, key, v)
		}
	}
}
