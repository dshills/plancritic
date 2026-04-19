// Package prompt builds the LLM prompt for plan review.
package prompt

import (
	"fmt"
	"path/filepath"
	"strings"

	pctx "github.com/dshills/plancritic/internal/context"
	"github.com/dshills/plancritic/internal/llm"
	"github.com/dshills/plancritic/internal/plan"
	"github.com/dshills/plancritic/internal/profile"
	"github.com/dshills/plancritic/internal/schema"
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
	prefix.WriteString(schemaDefinition)
	prefix.WriteString("\n\n")
	prefix.WriteString(`## Rules

1. Cite evidence for every issue and question using exact line numbers from the plan or context (source, path, line_start, line_end).
2. Do NOT emit a "quote" field in evidence. The runner reconstructs the quote deterministically from the cited line range; any "quote" you emit will be overwritten. This rule saves tokens — comply strictly.
3. Do NOT invent facts about the repository, codebase, or environment that are not present in the plan or context files.
4. Keep the number of questions minimal — only ask what is needed to unblock execution.
5. Order issues by severity (CRITICAL first, then WARN, then INFO), then by line number of first evidence.
6. The verdict must be one of: EXECUTABLE_AS_IS, EXECUTABLE_WITH_CLARIFICATIONS, NOT_EXECUTABLE.
7. Compute the score starting at 100, subtracting 20 per CRITICAL, 7 per WARN, 2 per INFO, clamped at 0.

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
	if len(opts.Contexts) > 0 {
		var ctxBuf strings.Builder
		for _, ctx := range opts.Contexts {
			fmt.Fprintf(&ctxBuf, "<context path=%q>\n%s</context>\n\n", filepath.Base(ctx.FilePath), pctx.LineNumbered(ctx))
		}
		segs = append(segs, llm.Segment{Text: ctxBuf.String(), CacheMark: true})
	}

	// Segment 3: plan, inferred step IDs, and caps. These vary across
	// re-runs (the user edits the plan between calls) and are not cached.
	var tail strings.Builder
	fmt.Fprintf(&tail, "<plan path=%q>\n%s</plan>\n\n", filepath.Base(opts.Plan.FilePath), plan.LineNumbered(opts.Plan))

	if len(opts.StepIDs) > 0 {
		tail.WriteString("## Inferred Plan Steps\n\n")
		for _, s := range opts.StepIDs {
			fmt.Fprintf(&tail, "- %s (L%d): %s\n", s.ID, s.LineStart, s.Text)
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
	segs = append(segs, llm.Segment{Text: tail.String()})

	return segs
}

// Build assembles the full LLM prompt as a single string by concatenating
// the segments returned by BuildSegments. Use BuildSegments directly when
// calling a provider that supports prompt caching.
func Build(opts BuildOpts) string {
	return llm.ConcatSegments(BuildSegments(opts))
}

// BuildRepair constructs a follow-up prompt to fix schema validation errors.
func BuildRepair(originalOutput string, errors []schema.ValidationError) string {
	var b strings.Builder
	b.WriteString("The JSON output you returned has validation errors. Fix ONLY the errors listed below and return the corrected JSON.\n\n")
	b.WriteString("## Validation Errors\n\n")
	for _, e := range errors {
		fmt.Fprintf(&b, "- %s: %s\n", e.Path, e.Message)
	}
	b.WriteString("\n")
	b.WriteString(schemaDefinition)
	b.WriteString("\n\n## Original Output\n\n```json\n")
	b.WriteString(originalOutput)
	b.WriteString("\n```\n\nReturn ONLY the corrected JSON. No prose.\n")
	return b.String()
}

const schemaDefinition = `## Output JSON Schema

{
  "tool": "plancritic",
  "version": "1.0",
  "input": {
    "plan_file": string,
    "plan_hash": "sha256:...",
    "context_files": [{"path": string, "hash": "sha256:..."}],
    "profile": string,
    "strict": boolean
  },
  "summary": {
    "verdict": "EXECUTABLE_AS_IS" | "EXECUTABLE_WITH_CLARIFICATIONS" | "NOT_EXECUTABLE",
    "score": integer (0-100),
    "critical_count": integer,
    "warn_count": integer,
    "info_count": integer
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
    "evidence": [{...}],
    "impact": string,
    "recommendation": string,
    "blocking": boolean,
    "tags": [string]
  }],
  "patches": [{
    "id": "PATCH-NNNN",
    "type": "PLAN_TEXT_EDIT",
    "title": string,
    "diff_unified": string
  }],
  "checklists": [{
    "id": string,
    "title": string,
    "checks": [{"check": string, "status": "PASS"|"FAIL"|"N/A"}]
  }],
  "meta": {
    "model": string,
    "temperature": float
  }
}`
