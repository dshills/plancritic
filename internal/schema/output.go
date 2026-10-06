package schema

import (
	"bytes"
	"encoding/json"
	"sort"
)

// ModelOutputSchema is the JSON Schema the model's response must satisfy.
// It is the model-facing subset of schema/review.v1.json: fields the
// runner fills itself (tool, version, input, score, severity counts,
// meta) and the evidence quote (reconstructed locally from the cited
// lines) are omitted, so the model never spends output tokens on values
// that would be discarded or, in the case of the plan hash, invented.
//
// The schema is written for provider "strict" modes: every object sets
// additionalProperties:false and lists every property as required, and
// no numeric, string, or array-length constraints are used because
// several providers reject them. Semantic checks that cannot be
// expressed here (evidence line ranges within file bounds, context
// paths that were actually provided, unique IDs, at least one evidence
// entry) remain in Validate.
// OutputShape selects the optional sections the model is asked to
// produce. Patches (unified diffs) and checklists are the most expensive
// and least often consumed parts of a review, so they are requested only
// when a caller will actually use them.
type OutputShape struct {
	Patches    bool
	Checklists bool
	// Coverage asks for a requirement-by-requirement map of the spec
	// onto the plan; set only when a spec file was supplied.
	Coverage bool
}

func ModelOutputSchema(shape OutputShape) map[string]any {
	evidence := obj(map[string]any{
		"source":     enum([]string{"plan", "context"}, "Which input the citation refers to"),
		"path":       str("File name as shown in the input markers"),
		"line_start": integer("First cited line (1-based, inclusive)"),
		"line_end":   integer("Last cited line (inclusive, >= line_start)"),
	}, []string{"source", "path", "line_start", "line_end"})

	issue := obj(map[string]any{
		"id":       str("ISSUE-NNNN, unique"),
		"severity": enum([]string{"INFO", "WARN", "CRITICAL"}, ""),
		"category": enum([]string{
			"CONTRADICTION", "AMBIGUITY", "MISSING_PREREQUISITE",
			"MISSING_ACCEPTANCE_CRITERIA", "RISK_SECURITY", "RISK_DATA",
			"RISK_OPERATIONS", "TEST_GAP", "SCOPE_CREEP_RISK",
			"UNREALISTIC_STEP", "ORDERING_DEPENDENCY",
			"UNSPECIFIED_INTERFACE", "NON_DETERMINISM",
		}, ""),
		"title":          str(""),
		"description":    str(""),
		"evidence":       arr(evidence),
		"impact":         str(""),
		"recommendation": str(""),
		"blocking":       boolean("True only for a CRITICAL issue that must be resolved before execution can start"),
		"tags":           arr(str("")),
	}, []string{"id", "severity", "category", "title", "description", "evidence", "impact", "recommendation", "blocking", "tags"})

	question := obj(map[string]any{
		"id":                str("Q-NNNN, unique"),
		"severity":          enum([]string{"INFO", "WARN", "CRITICAL"}, ""),
		"question":          str(""),
		"why_needed":        str(""),
		"blocks":            arr(str("Plan step IDs (P-NNN) this question blocks")),
		"evidence":          arr(evidence),
		"suggested_answers": arr(str("")),
	}, []string{"id", "severity", "question", "why_needed", "blocks", "evidence", "suggested_answers"})

	patch := obj(map[string]any{
		"id":           str("PATCH-NNNN, unique"),
		"type":         enum([]string{"PLAN_TEXT_EDIT"}, ""),
		"title":        str(""),
		"diff_unified": str("Unified diff against the plan text"),
	}, []string{"id", "type", "title", "diff_unified"})

	check := obj(map[string]any{
		"check":  str(""),
		"status": enum([]string{"PASS", "FAIL", "N/A"}, ""),
	}, []string{"check", "status"})

	checklist := obj(map[string]any{
		"id":     str(""),
		"title":  str(""),
		"checks": arr(check),
	}, []string{"id", "title", "checks"})

	summary := obj(map[string]any{
		"verdict": enum([]string{"EXECUTABLE_AS_IS", "EXECUTABLE_WITH_CLARIFICATIONS", "NOT_EXECUTABLE"}, ""),
	}, []string{"verdict"})

	requirement := obj(map[string]any{
		"id":            str("REQ-NNNN, unique"),
		"requirement":   str("The requirement restated in at most 15 words"),
		"status":        enum([]string{"COVERED", "PARTIAL", "UNCOVERED"}, ""),
		"spec_evidence": arr(evidence),
		"plan_evidence": arr(evidence),
		"note":          str("Why PARTIAL or UNCOVERED; empty when COVERED"),
	}, []string{"id", "requirement", "status", "spec_evidence", "plan_evidence", "note"})
	scopeItem := obj(map[string]any{
		"id":            str("SCOPE-NNNN, unique"),
		"plan_step":     str("The plan work with no basis in the spec, in at most 15 words"),
		"plan_evidence": arr(evidence),
		"note":          str(""),
	}, []string{"id", "plan_step", "plan_evidence", "note"})
	coverage := obj(map[string]any{
		"requirements": arr(requirement),
		"out_of_scope": arr(scopeItem),
	}, []string{"requirements", "out_of_scope"})

	// The required list doubles as the generation order (see
	// ModelOutputSchemaJSON): providers with structured output emit
	// properties in schema order, so the most valuable sections come
	// first and survive if the response is cut off at the output cap.
	// The summary goes last because its only field, the verdict, is
	// recomputed locally from the issues.
	props := map[string]any{
		"issues":    arr(issue),
		"questions": arr(question),
		"summary":   summary,
	}
	required := []string{"issues", "questions"}
	if shape.Coverage {
		props["coverage"] = coverage
		required = append(required, "coverage")
	}
	if shape.Patches {
		props["patches"] = arr(patch)
		required = append(required, "patches")
	}
	if shape.Checklists {
		props["checklists"] = arr(checklist)
		required = append(required, "checklists")
	}
	required = append(required, "summary")
	return obj(props, required)
}

// ModelOutputSchemaJSON returns ModelOutputSchema serialized for
// embedding in provider requests. Unlike json.Marshal, which sorts map
// keys alphabetically, it writes each object's properties in the order
// of its "required" list. Providers with structured output generate
// properties in schema order, so alphabetical order would put
// "coverage" ahead of "issues" and lose every issue when a long
// response is truncated.
func ModelOutputSchemaJSON(shape OutputShape) json.RawMessage {
	var b bytes.Buffer
	if err := writeOrdered(&b, ModelOutputSchema(shape)); err != nil {
		// The schema is a static literal; a marshal failure is a bug.
		panic("schema: marshal model output schema: " + err.Error())
	}
	return b.Bytes()
}

// schemaKeyOrder fixes the order of JSON Schema keywords within a node.
var schemaKeyOrder = []string{"type", "description", "enum", "properties", "required", "additionalProperties", "items"}

// writeOrdered serializes schema nodes deterministically: keywords in
// schemaKeyOrder, and "properties" in the order of the sibling
// "required" list. Values that are not maps are marshaled normally.
func writeOrdered(b *bytes.Buffer, v any) error {
	m, ok := v.(map[string]any)
	if !ok {
		data, err := json.Marshal(v)
		if err != nil {
			return err
		}
		b.Write(data)
		return nil
	}
	keys := make([]string, 0, len(m))
	seen := make(map[string]bool, len(m))
	for _, k := range schemaKeyOrder {
		if _, ok := m[k]; ok {
			keys = append(keys, k)
			seen[k] = true
		}
	}
	var rest []string
	for k := range m {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	keys = append(keys, rest...)

	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kj, _ := json.Marshal(k)
		b.Write(kj)
		b.WriteByte(':')
		if k == "properties" {
			props, _ := m[k].(map[string]any)
			req, _ := m["required"].([]string)
			if err := writeProperties(b, props, req); err != nil {
				return err
			}
			continue
		}
		if err := writeOrdered(b, m[k]); err != nil {
			return err
		}
	}
	b.WriteByte('}')
	return nil
}

// writeProperties writes props in the order of required, then any
// remaining keys alphabetically.
func writeProperties(b *bytes.Buffer, props map[string]any, required []string) error {
	order := make([]string, 0, len(props))
	seen := make(map[string]bool, len(props))
	for _, k := range required {
		if _, ok := props[k]; ok && !seen[k] {
			order = append(order, k)
			seen[k] = true
		}
	}
	var rest []string
	for k := range props {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	order = append(order, rest...)

	b.WriteByte('{')
	for i, k := range order {
		if i > 0 {
			b.WriteByte(',')
		}
		kj, _ := json.Marshal(k)
		b.Write(kj)
		b.WriteByte(':')
		if err := writeOrdered(b, props[k]); err != nil {
			return err
		}
	}
	b.WriteByte('}')
	return nil
}

func obj(props map[string]any, required []string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           props,
		"required":             required,
		"additionalProperties": false,
	}
}

func arr(items any) map[string]any {
	return map[string]any{"type": "array", "items": items}
}

func str(desc string) map[string]any {
	return withDesc(map[string]any{"type": "string"}, desc)
}

func integer(desc string) map[string]any {
	return withDesc(map[string]any{"type": "integer"}, desc)
}

func boolean(desc string) map[string]any {
	return withDesc(map[string]any{"type": "boolean"}, desc)
}

func enum(values []string, desc string) map[string]any {
	return withDesc(map[string]any{"type": "string", "enum": values}, desc)
}

func withDesc(m map[string]any, desc string) map[string]any {
	if desc != "" {
		m["description"] = desc
	}
	return m
}
