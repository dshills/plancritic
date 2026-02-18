# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

PlanCritic is a Go CLI tool that reviews software implementation plans (written by coding agents or engineers) and returns structured critique: contradictions, ambiguities, missing prerequisites, questions, and suggested patches. Output is JSON-first (Markdown is rendered from JSON). The full specification lives in `specs/SPEC.md`.

## Project Status

Pre-implementation. The repo contains the product spec and one built-in profile (`internal/profile/builtin/davin-go.yaml`). No Go code, go.mod, or build infrastructure exists yet.

## Build & Test Commands (planned)

Once implemented, the project should use standard Go tooling:
- **Build:** `go build -o plancritic ./cmd/plancritic`
- **Test all:** `go test ./...`
- **Test single package:** `go test ./internal/review`
- **Test single test:** `go test ./internal/review -run TestScoreCalculation`
- **Lint:** `golangci-lint run`

## Architecture

### Data Flow
```
Load plan + context files → Redact secrets → Build LLM prompt
→ Call LLM provider → Parse & validate JSON → Post-process (score, sort, truncate)
→ Optionally generate patch diff → Output JSON or render Markdown
```

### Planned Package Layout
- `cmd/plancritic` — CLI entry point (Cobra or urfave)
- `internal/plan` — Read, line-number, hash plan files
- `internal/context` — Load and line-number context files
- `internal/redact` — Pattern-based secret redaction before LLM calls
- `internal/profile` — Load YAML profile checklists (embedded in binary)
- `internal/llm` — Provider interface (`Generate(ctx, prompt, settings) -> string`) with pluggable implementations (OpenAI, Anthropic)
- `internal/schema` — JSON schema validation of LLM output
- `internal/review` — Review types, deterministic scoring, sorting
- `internal/render` — Markdown renderer from JSON
- `internal/patch` — Unified diff generation for plan text edits

### Key Design Decisions
- **Evidence-driven:** Every issue/question must reference specific plan excerpts with line ranges and quotes.
- **Anti-hallucination:** The model must not invent repo facts; it only reasons about plan and context content.
- **Deterministic scoring:** Score starts at 100, subtract 20/CRITICAL, 7/WARN, 2/INFO, clamp at 0.
- **Ordering:** Issues sorted by severity (CRITICAL > WARN > INFO), then by `evidence[0].line_start`.
- **Strict grounding mode (`--strict`):** Everything not in plan/context is unknown; uncertain inferences capped at WARN with `["assumption"]` tag.
- **Output validation:** Parse LLM JSON, validate schema, retry once with repair prompt if invalid, exit code 5 if still invalid.

### Key Enums
- **Verdict:** `EXECUTABLE_AS_IS`, `EXECUTABLE_WITH_CLARIFICATIONS`, `NOT_EXECUTABLE`
- **Severity:** `INFO`, `WARN`, `CRITICAL`
- **Category:** `CONTRADICTION`, `AMBIGUITY`, `MISSING_PREREQUISITE`, `MISSING_ACCEPTANCE_CRITERIA`, `RISK_SECURITY`, `RISK_DATA`, `RISK_OPERATIONS`, `TEST_GAP`, `SCOPE_CREEP_RISK`, `UNREALISTIC_STEP`, `ORDERING_DEPENDENCY`, `UNSPECIFIED_INTERFACE`, `NON_DETERMINISM`

### Exit Codes
- 0: success / verdict below fail threshold
- 2: verdict meets/exceeds fail threshold
- 3: input error
- 4: model/provider error
- 5: schema validation error

### Profiles
Profiles are YAML checklists + constraints embedded in the binary. Built-in profiles to implement: `general` (default), `go-backend`, `react-frontend`, `aws-deploy`. See `internal/profile/builtin/davin-go.yaml` for the pattern.

### Phase 2 Seams (do not implement, but leave room)
- `ReviewInput` struct should have an optional `Artifacts` list (diffs, test output)
- Prompt builder should support additional evidence sources
- Output schema should allow version bump / optional fields without breaking v1

## Code Quality

After writing or modifying code, run `prism review staged` before committing.
If findings are severity high, fix them before proceeding.
For security-sensitive changes, use compare mode:
  prism review staged --compare openai:gpt-5.2,gemini:gemini-3-flash-preview

## Conventions from the Spec
- Prefer standard library; minimize dependencies (any new dep needs explicit justification)
- Line-number plan text for the LLM using `L###:` prefix format
- Redaction is on by default — replace secrets with `[REDACTED]`
- JSON output must be strictly validated against the schema
- Cap issues at 50, questions at 20; emit truncation warning if exceeded
- Profiles are local files embedded in the binary, not fetched remotely
