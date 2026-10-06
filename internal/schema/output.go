package schema

import (
	"encoding/json"
	"sync"
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
func ModelOutputSchema() map[string]any {
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

	return obj(map[string]any{
		"summary":    summary,
		"questions":  arr(question),
		"issues":     arr(issue),
		"patches":    arr(patch),
		"checklists": arr(checklist),
	}, []string{"summary", "questions", "issues", "patches", "checklists"})
}

var modelOutputSchemaJSON = sync.OnceValue(func() json.RawMessage {
	data, err := json.Marshal(ModelOutputSchema())
	if err != nil {
		// The schema is a static literal; a marshal failure is a bug.
		panic("schema: marshal model output schema: " + err.Error())
	}
	return data
})

// ModelOutputSchemaJSON returns ModelOutputSchema serialized once for
// embedding in provider requests.
func ModelOutputSchemaJSON() json.RawMessage {
	return modelOutputSchemaJSON()
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
