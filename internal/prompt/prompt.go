// Package prompt builds the LLM prompt for plan review.
package prompt

import (
	"fmt"
	"path/filepath"
	"strings"

	pctx "github.com/dshills/plancritic/internal/context"
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

// Build assembles the full LLM prompt.
func Build(opts BuildOpts) string {
	var b strings.Builder

	// 1. System preamble
	b.WriteString(`You are a plan critic. Your task is to review a software implementation plan and produce a structured critique.

You MUST output ONLY valid JSON matching the schema below. No markdown, no prose outside JSON.

`)

	// 2. Schema definition
	b.WriteString(schemaDefinition)
	b.WriteString("\n\n")

	// 3. Grounding rules
	b.WriteString(`## Rules

1. Cite evidence for every issue and question using exact line numbers and quotes from the plan or context.
2. Do NOT invent facts about the repository, codebase, or environment that are not present in the plan or context files.
3. Keep the number of questions minimal — only ask what is needed to unblock execution.
4. Order issues by severity (CRITICAL first, then WARN, then INFO), then by line number of first evidence.
5. The verdict must be one of: EXECUTABLE_AS_IS, EXECUTABLE_WITH_CLARIFICATIONS, NOT_EXECUTABLE.
6. Compute the score starting at 100, subtracting 20 per CRITICAL, 7 per WARN, 2 per INFO, clamped at 0.

`)

	// 4. Strict mode
	if opts.Strict {
		b.WriteString(`## Strict Grounding Mode (ENABLED)

- Treat everything NOT present in the plan or context files as UNKNOWN.
- Do NOT claim "the repo uses X" unless X appears in the provided context.
- Recommendations may be generic but MUST be labeled as such ("If applicable...").
- Any uncertain inference MUST be tagged with "assumption" and severity capped at WARN.

`)
	}

	// 5. Profile
	if opts.Profile != nil {
		b.WriteString(profile.FormatForPrompt(opts.Profile))
		b.WriteString("\n")
	}

	// 6. Plan (use basename to avoid leaking filesystem paths to LLM)
	fmt.Fprintf(&b, "<plan path=%q>\n%s</plan>\n\n", filepath.Base(opts.Plan.FilePath), plan.LineNumbered(opts.Plan))

	// 7. Context files
	for _, ctx := range opts.Contexts {
		fmt.Fprintf(&b, "<context path=%q>\n%s</context>\n\n", filepath.Base(ctx.FilePath), pctx.LineNumbered(ctx))
	}

	// 8. Step IDs
	if len(opts.StepIDs) > 0 {
		b.WriteString("## Inferred Plan Steps\n\n")
		for _, s := range opts.StepIDs {
			fmt.Fprintf(&b, "- %s (L%d): %s\n", s.ID, s.LineStart, s.Text)
		}
		b.WriteString("\n")
	}

	// 9. Caps
	maxIssues := opts.MaxIssues
	if maxIssues <= 0 {
		maxIssues = 50
	}
	maxQ := opts.MaxQuestions
	if maxQ <= 0 {
		maxQ = 20
	}
	fmt.Fprintf(&b, "Return at most %d issues and %d questions.\n", maxIssues, maxQ)

	return b.String()
}

// BuildRepair constructs a follow-up prompt to fix schema validation errors.
func BuildRepair(originalOutput string, errors []schema.ValidationError) string {
	var b strings.Builder
	b.WriteString("The JSON output you returned has validation errors. Fix ONLY the errors listed below and return the corrected JSON.\n\n")
	b.WriteString("## Validation Errors\n\n")
	for _, e := range errors {
		fmt.Fprintf(&b, "- %s: %s\n", e.Path, e.Message)
	}
	b.WriteString("\n## Original Output\n\n```json\n")
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
    "evidence": [{"source": "plan"|"context", "path": string, "line_start": int, "line_end": int, "quote": string}],
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
