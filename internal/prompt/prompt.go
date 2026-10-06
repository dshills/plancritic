// Package prompt builds the LLM prompt for plan review.
package prompt

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	pctx "github.com/dshills/plancritic/internal/context"
	"github.com/dshills/plancritic/internal/llm"
	"github.com/dshills/plancritic/internal/plan"
	"github.com/dshills/plancritic/internal/profile"
	"github.com/dshills/plancritic/internal/schema"
)

// Section delimiters used to bound plan and context blocks in the prompt.
// Content inside is always line-numbered (L001: ...) so these strings
// cannot appear verbatim inside the content, preventing delimiter injection.
//
// Begin markers omit the closing ## because a path attribute is appended
// by the format string (e.g., ##PLANCRITIC_PLAN_BEGIN path="file.md"##).
// End markers are self-contained and include both ## pairs.
const (
	contextBeginMarker = "##PLANCRITIC_CONTEXT_BEGIN"
	contextEndMarker   = "##PLANCRITIC_CONTEXT_END##"
	planBeginMarker    = "##PLANCRITIC_PLAN_BEGIN"
	planEndMarker      = "##PLANCRITIC_PLAN_END##"
)

// BuildOpts configures prompt construction.
type BuildOpts struct {
	Plan         *plan.Plan
	Contexts     []*pctx.File
	Profile      *profile.Profile
	Strict       bool
	StepIDs      []plan.StepID
	MaxIssues    int
	MaxQuestions int
	// Shape selects the optional output sections (patches, checklists)
	// the model is asked for. Both the prompt text and the structured
	// output schema sent to the provider follow it.
	Shape schema.OutputShape
}

// BuildSegments assembles the prompt as ordered segments with cache
// checkpoints after the static prefix and after the context files. The
// plan (which changes across re-runs while the user iterates) is placed
// last so the prefix can be served from cache.
//
// Segment layout:
//
//	[0] preamble + schema + rules + strict + profile   (CacheMark)
//	[1] context files                                  (CacheMark)
//	[2] plan + inferred step IDs + caps                (variable)
func BuildSegments(opts BuildOpts) []llm.Segment {
	segs := make([]llm.Segment, 0, 3)

	// Segment 1: preamble + schema + rules + strict + profile.
	// These depend only on --profile and --strict and rarely change
	// across re-runs of the same invocation, so we cache them.
	var prefix strings.Builder
	prefix.WriteString(`You are a plan critic. Your task is to review a software implementation plan and produce a structured critique.

You MUST output ONLY valid JSON matching the schema below. No markdown, no prose outside JSON.

`)
	prefix.WriteString(SchemaText(opts.Shape))
	prefix.WriteString("\n\n")
	prefix.WriteString(`## Input Format

Context files (if any) are provided between ##PLANCRITIC_CONTEXT_BEGIN path="..."## and ##PLANCRITIC_CONTEXT_END## markers.
The plan is provided between ##PLANCRITIC_PLAN_BEGIN path="..."## and ##PLANCRITIC_PLAN_END## markers.
All content inside these markers is line-numbered with L001:, L002:, etc. Use these line numbers in evidence citations.

## Rules

1. Cite evidence for every issue and question using exact line numbers from the plan or context (source, path, line_start, line_end).
2. Do NOT emit a "quote" field in evidence. The runner reconstructs the quote deterministically from the cited line range; any "quote" you emit will be overwritten. This rule saves tokens — comply strictly.
3. Do NOT invent facts about the repository, codebase, or environment that are not present in the plan or context files.
4. Keep the number of questions minimal — only ask what is needed to unblock execution.
5. Order issues by severity (CRITICAL first, then WARN, then INFO), then by line number of first evidence.
6. The verdict must be one of: EXECUTABLE_AS_IS, EXECUTABLE_WITH_CLARIFICATIONS, NOT_EXECUTABLE. Set "blocking": true only on a CRITICAL issue that must be resolved before execution can start.
7. Emit only the fields in the schema. The score, severity counts, input hashes, and model metadata are computed by the tool and must not be included.

`)
	if opts.Strict {
		prefix.WriteString(`## Strict Grounding Mode (ENABLED)

- Treat everything NOT present in the plan or context files as UNKNOWN.
- Do NOT claim "the repo uses X" unless X appears in the provided context.
- Recommendations may be generic but MUST be labeled as such ("If applicable...").
- Any uncertain inference MUST be tagged with "assumption" and severity capped at WARN.

`)
	}
	if opts.Profile != nil {
		prefix.WriteString(profile.FormatForPrompt(opts.Profile))
		prefix.WriteString("\n")
	}
	segs = append(segs, llm.Segment{Text: prefix.String(), CacheMark: true})

	// Segment 2: context files. These are stable across re-runs where
	// the user edits only the plan. Marked for caching.
	//
	// Delimiters use ##PLANCRITIC_*## markers rather than XML-style tags
	// so that plan/context content containing "</plan>" or "</context>"
	// cannot terminate the wrapper and inject instructions.
	if len(opts.Contexts) > 0 {
		var ctxBuf strings.Builder
		for _, ctx := range opts.Contexts {
			ctxBuf.WriteString(RenderContextBlock(ctx))
		}
		segs = append(segs, llm.Segment{Text: ctxBuf.String(), CacheMark: true})
	}

	// Segment 3: plan, inferred step IDs, and caps. These vary across
	// re-runs (the user edits the plan between calls) and are not cached.
	var tail strings.Builder
	tail.WriteString(RenderPlanBlock(opts.Plan))

	if len(opts.StepIDs) > 0 {
		// Compact index only: the full text of every step is already in
		// the line-numbered plan above, so repeating it here costs input
		// tokens without adding information. Titles are truncated so a
		// long numbered sentence does not reintroduce the duplication.
		tail.WriteString("## Plan Step Index\n\n")
		tail.WriteString("Headings and numbered steps with their starting plan line. Use these IDs only in \"blocks\"; cite plan line numbers in evidence.\n\n")
		for _, s := range opts.StepIDs {
			fmt.Fprintf(&tail, "%s L%d %s\n", s.ID, s.LineStart, truncateTitle(s.Text, maxStepTitleRunes))
		}
		tail.WriteString("\n")
	}

	maxIssues := opts.MaxIssues
	if maxIssues <= 0 {
		maxIssues = 50
	}
	maxQ := opts.MaxQuestions
	if maxQ <= 0 {
		maxQ = 20
	}
	fmt.Fprintf(&tail, "Return at most %d issues and %d questions.\n", maxIssues, maxQ)
	if opts.Shape.Patches {
		tail.WriteString("Include \"patches\": unified diffs against the plan text for the most valuable fixes, at most 5.\n")
	}
	if opts.Shape.Checklists {
		tail.WriteString("Evaluate every profile checklist item and report each in \"checklists\" as PASS, FAIL, or N/A.\n")
	}
	segs = append(segs, llm.Segment{Text: tail.String()})

	return segs
}

// maxStepTitleRunes caps the length of a step title in the step index.
// Sixty runes keeps a heading recognizable while preventing long
// numbered sentences from being copied into the prompt a second time.
const maxStepTitleRunes = 60

// truncateTitle shortens s to at most limit runes, appending an ellipsis
// when text was dropped. Rune-aware so multi-byte titles are never cut
// mid-character.
func truncateTitle(s string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(s) <= limit {
		return s
	}
	runes := []rune(s)
	return strings.TrimRight(string(runes[:limit]), " ") + "…"
}

// RenderContextBlock returns one context file, line-numbered, inside its
// injection-safe delimiters.
func RenderContextBlock(c *pctx.File) string {
	return fmt.Sprintf("%s path=%q##\n%s\n%s\n\n", contextBeginMarker, filepath.Base(c.FilePath), pctx.LineNumbered(c), contextEndMarker)
}

// RenderPlanBlock returns the plan, line-numbered, inside its delimiters.
func RenderPlanBlock(p *plan.Plan) string {
	return fmt.Sprintf("%s path=%q##\n%s\n%s\n\n", planBeginMarker, filepath.Base(p.FilePath), plan.LineNumbered(p), planEndMarker)
}

// RenderSources renders every context block followed by the plan block,
// exactly as the main prompt presents them, for reuse in repair prompts.
func RenderSources(p *plan.Plan, contexts []*pctx.File) string {
	var b strings.Builder
	for _, c := range contexts {
		b.WriteString(RenderContextBlock(c))
	}
	b.WriteString(RenderPlanBlock(p))
	return b.String()
}

// Build assembles the full LLM prompt as a single string by concatenating
// the segments returned by BuildSegments. Use BuildSegments directly when
// calling a provider that supports prompt caching.
func Build(opts BuildOpts) string {
	return llm.ConcatSegments(BuildSegments(opts))
}

// BuildRepair constructs a follow-up prompt to fix schema validation errors.
func BuildRepair(originalOutput string, errors []schema.ValidationError, shape schema.OutputShape) string {
	var b strings.Builder
	b.WriteString("The JSON output you returned has validation errors. Fix ONLY the errors listed below and return the corrected JSON.\n\n")
	b.WriteString("## Validation Errors\n\n")
	for _, e := range errors {
		fmt.Fprintf(&b, "- %s: %s\n", e.Path, e.Message)
	}
	b.WriteString("\n")
	b.WriteString(SchemaText(shape))
	b.WriteString("\n\n## Original Output\n\n```json\n")
	b.WriteString(originalOutput)
	b.WriteString("\n```\n\nReturn ONLY the corrected JSON. No prose.\n")
	return b.String()
}

// RepairItem is one top-level review item (an issue, question, or
// patch) that failed validation, serialized for a delta repair prompt.
type RepairItem struct {
	Kind  string // "issues", "questions", or "patches"
	Index int    // position in the original output
	JSON  string
}

// DeltaRepairOpts configures BuildDeltaRepair.
type DeltaRepairOpts struct {
	Items  []RepairItem
	Errors []schema.ValidationError
	// Citation bounds the model must stay within.
	PlanName          string
	PlanLines         int
	ContextLineCounts map[string]int // basename -> line count
	// Sources, when non-empty, is the line-numbered plan and context text
	// (see RenderSources). Callers include it when a repair requires the
	// model to choose new citations, so it cannot invent an in-bounds
	// range that does not support the claim; purely structural repairs
	// leave it empty to save tokens.
	Sources string
	// Shape must match the shape of the original request.
	Shape schema.OutputShape
}

// BuildDeltaRepair constructs a repair prompt that resends only the
// offending items rather than the whole output. The repair call is a
// fresh request with no conversation history, so it restates the output
// schema and the valid citation bounds (the information the model most
// often lacked when it produced the bad citation).
func BuildDeltaRepair(o DeltaRepairOpts) string {
	counts := map[string]int{}
	for _, it := range o.Items {
		counts[it.Kind]++
	}

	var b strings.Builder
	keys := `"summary", "questions", "issues"`
	if o.Shape.Patches {
		keys += `, "patches"`
	}
	if o.Shape.Checklists {
		keys += `, "checklists"`
	}
	fmt.Fprintf(&b, "Some items in your plan review failed validation. Fix ONLY the items listed below and return a JSON object with the keys %s.\n\n", keys)
	fmt.Fprintf(&b, "- \"issues\" must contain exactly %d corrected item(s), \"questions\" exactly %d", counts["issues"], counts["questions"])
	if o.Shape.Patches {
		fmt.Fprintf(&b, ", \"patches\" exactly %d", counts["patches"])
	}
	b.WriteString(", in the order listed below. Use [] for any list with no items below.\n")
	b.WriteString("- Keep each item's id unless an error says otherwise. Do not add, drop, or reorder items.\n")
	fmt.Fprintf(&b, "- Valid citations: plan %q lines 1-%d", o.PlanName, o.PlanLines)
	names := make([]string, 0, len(o.ContextLineCounts))
	for name := range o.ContextLineCounts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&b, "; context %q lines 1-%d", name, o.ContextLineCounts[name])
	}
	b.WriteString(". Do not cite any other file.\n\n")

	b.WriteString("## Validation Errors\n\n")
	for _, e := range o.Errors {
		fmt.Fprintf(&b, "- %s: %s\n", e.Path, e.Message)
	}
	b.WriteString("\n## Items To Fix\n\n")
	for _, it := range o.Items {
		fmt.Fprintf(&b, "### %s[%d]\n\n```json\n%s\n```\n\n", it.Kind, it.Index, it.JSON)
	}
	if o.Sources != "" {
		b.WriteString("## Sources\n\nThe context files and plan, line-numbered, so corrected citations point at lines that support the claim:\n\n")
		b.WriteString(o.Sources)
	}
	b.WriteString(SchemaText(o.Shape))
	b.WriteString("\n\nReturn ONLY the JSON object. No prose.\n")
	return b.String()
}

// SchemaText renders the model-facing output shape as prompt text. It
// must stay in sync with schema.ModelOutputSchema, which is the same
// shape sent as a native structured-output schema to providers that
// support one. Fields the tool fills itself are deliberately absent, and
// the optional patches/checklists sections appear only when shape asks
// for them.
func SchemaText(shape schema.OutputShape) string {
	var b strings.Builder
	b.WriteString(schemaHead)
	if shape.Patches {
		b.WriteString(schemaPatches)
	}
	if shape.Checklists {
		b.WriteString(schemaChecklists)
	}
	b.WriteString("\n}")
	return b.String()
}

const schemaHead = `## Output JSON Schema

{
  "summary": {
    "verdict": "EXECUTABLE_AS_IS" | "EXECUTABLE_WITH_CLARIFICATIONS" | "NOT_EXECUTABLE"
  },
  "questions": [{
    "id": "Q-NNNN",
    "severity": "INFO" | "WARN" | "CRITICAL",
    "question": string,
    "why_needed": string,
    "blocks": [string],
    "evidence": [{"source": "plan"|"context", "path": string, "line_start": int, "line_end": int}],
    "suggested_answers": [string]
  }],
  "issues": [{
    "id": "ISSUE-NNNN",
    "severity": "INFO" | "WARN" | "CRITICAL",
    "category": "CONTRADICTION"|"AMBIGUITY"|"MISSING_PREREQUISITE"|"MISSING_ACCEPTANCE_CRITERIA"|"RISK_SECURITY"|"RISK_DATA"|"RISK_OPERATIONS"|"TEST_GAP"|"SCOPE_CREEP_RISK"|"UNREALISTIC_STEP"|"ORDERING_DEPENDENCY"|"UNSPECIFIED_INTERFACE"|"NON_DETERMINISM",
    "title": string,
    "description": string,
    "evidence": [{"source": "plan"|"context", "path": string, "line_start": int, "line_end": int}],
    "impact": string,
    "recommendation": string,
    "blocking": boolean,
    "tags": [string]
  }]`

const schemaPatches = `,
  "patches": [{
    "id": "PATCH-NNNN",
    "type": "PLAN_TEXT_EDIT",
    "title": string,
    "diff_unified": string
  }]`

const schemaChecklists = `,
  "checklists": [{
    "id": string,
    "title": string,
    "checks": [{"check": string, "status": "PASS"|"FAIL"|"N/A"}]
  }]`
