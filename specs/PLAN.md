# PlanCritic Implementation Plan

Reference: `specs/SPEC.md`

---

## Phase 1: Project Scaffolding

### 1.1 Initialize Go module and directory structure

Create `go.mod` with module path `github.com/dshills/plancritic` (adjust as needed). Create the full directory tree:

```
cmd/plancritic/
internal/review/
internal/plan/
internal/context/
internal/redact/
internal/profile/
  builtin/          (already exists with davin-go.yaml)
internal/llm/
internal/schema/
internal/render/
internal/patch/
internal/prompt/
schema/
examples/
```

Note: the spec lists prompt construction as part of the data flow but doesn't give it a dedicated package. It warrants one (`internal/prompt`) since it assembles plan, context, profile, schema definition, and grounding rules into the final LLM prompt — a non-trivial responsibility distinct from the LLM provider itself.

### 1.2 Add initial dependencies

Minimal set:
- `github.com/spf13/cobra` — CLI framework (spec suggests cobra/urfave; cobra is more common in Go)
- `gopkg.in/yaml.v3` — profile YAML parsing
- No other external deps at this stage. JSON schema validation and diff generation will use stdlib or small focused libraries evaluated in their respective steps.

---

## Phase 2: Core Types (`internal/review`)

This package has zero internal dependencies and everything else builds on it. Implement first.

### 2.1 Define all types and enums

Types to define as Go structs with JSON tags:

- `Review` — top-level output object (`tool`, `version`, `input`, `summary`, `questions`, `issues`, `patches`, `checklists`, `meta`)
- `Input` — plan file path, plan hash, context files, profile name, strict flag
- `ContextFile` — path + hash
- `Summary` — verdict, score, counts
- `Issue` — id, severity, category, title, description, evidence, impact, recommendation, blocking, tags
- `Question` — id, severity, question, why_needed, blocks, evidence, suggested_answers
- `Patch` — id, type, title, diff_unified
- `ChecklistResult` — id, title, checks with pass/fail/na status
- `Evidence` — source ("plan"|"context"), path, line_start, line_end, quote
- `Meta` — model, temperature

Enums as string types with const blocks:
- `Verdict`: `EXECUTABLE_AS_IS`, `EXECUTABLE_WITH_CLARIFICATIONS`, `NOT_EXECUTABLE`
- `Severity`: `INFO`, `WARN`, `CRITICAL`
- `Category`: all 13 values from spec (`CONTRADICTION` through `NON_DETERMINISM`)
- `PatchType`: `PLAN_TEXT_EDIT`

Include a `Valid()` method on each enum type for use during validation.

### 2.2 Implement deterministic scoring

Function: `ComputeScore(issues []Issue) int`
- Start at 100
- Subtract 20 per CRITICAL, 7 per WARN, 2 per INFO
- Clamp at 0

### 2.3 Implement deterministic sorting

Function: `SortIssues(issues []Issue)` — sort by severity (CRITICAL > WARN > INFO), then by `evidence[0].line_start` ascending. Same for `SortQuestions`.

Define a severity ordering helper (CRITICAL=0, WARN=1, INFO=2) for sort comparisons.

### 2.4 Implement summary computation

Function: `ComputeSummary(issues []Issue, questions []Question) Summary` — derives verdict from issue counts, computes score, counts by severity. Verdict logic:
- Any CRITICAL with `blocking: true` → `NOT_EXECUTABLE`
- Any WARN or CRITICAL → `EXECUTABLE_WITH_CLARIFICATIONS`
- Otherwise → `EXECUTABLE_AS_IS`

### 2.5 Implement truncation

Function: `Truncate(review *Review, maxIssues, maxQuestions int)` — if issues exceed cap (50), truncate and append a synthetic WARN issue "Output truncated; increase limits." Same for questions (cap 20).

### 2.6 Tests for Phase 2

- `TestComputeScore`: table-driven with various issue mixes, including edge cases (all CRITICAL, empty, clamp to 0).
- `TestSortIssues`: verify severity ordering, then line_start tiebreak, issues with no evidence.
- `TestComputeSummary`: verdict derivation for each verdict value.
- `TestTruncate`: over-limit and under-limit cases.
- `TestEnumValid`: valid and invalid values for each enum type.

---

## Phase 3: Input Loading

### 3.1 Plan loading (`internal/plan`)

Types and functions:
- `Plan` struct: `FilePath string`, `Raw string`, `Lines []string`, `Hash string`
- `Load(path string) (*Plan, error)` — read file, split lines, compute SHA-256 hash.
- `LineNumbered(p *Plan) string` — produce the `L001: ...` formatted text for the prompt.
- `InferStepIDs(p *Plan) []StepID` — scan for numbered headings/bullets, assign P-001, P-002, etc. Return slice of `{ID, LineStart, LineEnd, Text}`. If no structure detected, assign IDs to top-level paragraphs/sections.

Tests:
- `TestLoad`: file read, correct line count, stable hash.
- `TestLineNumbered`: verify format `L001: first line\nL002: second line`.
- `TestInferStepIDs`: numbered bullets, markdown headings, unstructured text.

### 3.2 Context loading (`internal/context`)

Types and functions:
- `ContextFile` struct: `FilePath string`, `Raw string`, `Lines []string`, `Hash string`
- `Load(path string) (*ContextFile, error)` — same pattern as plan.
- `LineNumbered(c *ContextFile) string` — same L-prefix format, labeled with filename.

Tests:
- `TestLoad`: read, hash, line split.
- `TestLineNumbered`: correct output format.

### 3.3 Redaction (`internal/redact`)

Function: `Redact(text string) string`

Built-in regex patterns to match and replace with `[REDACTED]`:
- AWS access key IDs: `AKIA[0-9A-Z]{16}`
- AWS secret keys: 40-char base64 following known prefixes
- Generic API tokens/keys: `(?i)(api[_-]?key|api[_-]?secret|token|password|secret)\s*[:=]\s*\S+`
- Private key blocks: `-----BEGIN [A-Z ]+ PRIVATE KEY-----[\s\S]*?-----END [A-Z ]+ PRIVATE KEY-----`
- Bearer tokens: `Bearer\s+[A-Za-z0-9\-._~+/]+=*`
- Generic high-entropy strings in assignment context (conservative — only when following `=` or `:` in config-like lines)

Design: patterns stored as a `[]regexp.Regexp` slice, initialized once. `Redact` applies all patterns sequentially.

Tests:
- `TestRedact`: AWS keys replaced, private key blocks replaced, surrounding text preserved, non-secret text untouched.

---

## Phase 4: Profiles (`internal/profile`)

### 4.1 Profile types

```go
type Profile struct {
    Name         string
    Version      int
    Description  string
    Constraints  map[string]any  // flexible structure matching YAML
    Checklists   []Checklist
    Heuristics   Heuristics
}

type Checklist struct {
    ID     string
    Title  string
    Checks []string
}

type Heuristics struct {
    Contradictions    []Contradiction
    AmbiguityTriggers []string
}

type Contradiction struct {
    TriggerA string
    TriggerB string
    Severity string
    Note     string
}
```

### 4.2 Embed built-in profiles

Use `//go:embed builtin/*.yaml` to embed all YAML files from `internal/profile/builtin/`.

Function: `LoadBuiltin(name string) (*Profile, error)` — look up by name in embedded FS, parse YAML.

Function: `List() []string` — return available profile names.

### 4.3 Create remaining built-in profiles

Write YAML files for the three remaining profiles specified:
- `general.yaml` — language-agnostic baseline checks (contracts, tests, rollback, security, observability)
- `react-frontend.yaml` — frontend-specific (accessibility, state management, bundle size, API contracts, error boundaries)
- `aws-deploy.yaml` — deployment-specific (IAM, networking, rollback, monitoring, cost, infrastructure-as-code)

Model these after the existing `davin-go.yaml` structure.

### 4.4 Profile-to-prompt text

Function: `FormatForPrompt(p *Profile) string` — render the profile's constraints, checklists, and heuristics into the text block that gets injected into the LLM prompt.

### 4.5 Tests

- `TestLoadBuiltin`: load each of the 4 profiles, verify name and non-empty checklists.
- `TestLoadBuiltinNotFound`: unknown profile returns error.
- `TestFormatForPrompt`: output contains checklist IDs and check text.

---

## Phase 5: LLM Provider (`internal/llm`)

### 5.1 Provider interface

```go
type Settings struct {
    Model       string
    Temperature float64
    MaxTokens   int
    Seed        *int
}

type Provider interface {
    Generate(ctx context.Context, prompt string, settings Settings) (string, error)
    Name() string
}
```

### 5.2 Anthropic provider implementation

Implement using the Anthropic Messages API via `net/http` (no SDK dependency needed — the API is a single POST endpoint).

- Read API key from `ANTHROPIC_API_KEY` env var.
- Default model: `claude-sonnet-4-20250514` (configurable via `--model`).
- Map `Settings` to the Messages API request body.
- Parse the response, extract the text content block.
- Return the raw text string (JSON parsing happens in the caller).

### 5.3 OpenAI provider implementation

Implement using the OpenAI Chat Completions API via `net/http`.

- Read API key from `OPENAI_API_KEY` env var.
- Default model: `gpt-4o` (configurable via `--model`).
- Use `response_format: { type: "json_object" }` to encourage JSON output.
- Map `Settings` fields including optional `seed`.

### 5.4 Provider resolution

Function: `ResolveProvider(modelFlag string) (Provider, error)`

Logic:
- If `--model` starts with `anthropic:` or `claude-`, use Anthropic provider.
- If `--model` starts with `openai:` or `gpt-`, use OpenAI provider.
- If `--model` not specified: check which API key env vars are set. Use the first available. If both set, prefer Anthropic (arbitrary default).
- If `--offline` flag is set and no provider resolves, return error.
- If no API key found, return clear error message naming the expected env vars.

### 5.5 Tests

- `TestResolveProvider`: model flag parsing, env var fallback logic (use `t.Setenv`).
- Provider implementations: test request construction with a local HTTP test server (`httptest.NewServer`) that validates the request shape and returns canned JSON. Do not make real API calls in unit tests.

---

## Phase 6: Schema Validation (`internal/schema`)

### 6.1 Validation function

Function: `Validate(review *review.Review) []ValidationError`

Perform structural validation in Go code (no external JSON Schema library needed for Phase 1 — the schema is known at compile time):

Checks:
- Required fields present and non-zero: `tool`, `version`, `summary.verdict`, `summary.score`
- `verdict` is valid enum value
- Every issue has: `id`, `severity` (valid), `category` (valid), `title`, `description`, at least one evidence entry
- Every evidence has: `source` ∈ {"plan", "context"}, `path` non-empty, `line_start` > 0, `line_end` >= `line_start`, `quote` non-empty
- Every question has: `id`, `severity` (valid), `question`, `why_needed`, at least one evidence entry
- Every patch has: `id`, `type` (valid), `title`, `diff_unified`
- Score matches recomputed score (consistency check)
- Issue/question IDs are unique
- `line_end` >= `line_start` for all evidence
- Evidence `line_start`/`line_end` do not exceed the plan's total line count (requires plan line count as input)

Return type: `[]ValidationError` where each error has a path (e.g., `issues[2].evidence[0].line_start`) and message.

### 6.2 Write formal JSON Schema file

Write `schema/review.v1.json` — a standard JSON Schema document matching the spec's output structure. This is a deliverable for documentation/external tooling, not used by the Go validation code directly.

### 6.3 Tests

- `TestValidate`: valid review passes; missing fields, invalid enums, bad line ranges, duplicate IDs each produce the expected error.
- Table-driven with one case per validation rule.

---

## Phase 7: Prompt Construction (`internal/prompt`)

### 7.1 Build function

Function: `Build(opts BuildOpts) string`

```go
type BuildOpts struct {
    Plan          *plan.Plan
    Contexts      []*context.ContextFile
    Profile       *profile.Profile
    Strict        bool
    StepIDs       []plan.StepID  // optional
    MaxIssues     int
    MaxQuestions   int
}
```

Assemble the prompt in this order:
1. **Role/system preamble**: "You are a plan critic. Output only valid JSON matching the following schema."
2. **Schema definition**: compact description of the output JSON structure with all enums listed.
3. **Grounding rules**: cite evidence for every issue/question; do not invent repo facts; keep questions minimal; order issues by severity then line number.
4. **Strict mode addendum** (if enabled): treat everything not in plan/context as unknown; cap uncertain inferences at WARN; tag with "assumption".
5. **Profile section**: constraints, checklists, and heuristics rendered via `profile.FormatForPrompt`.
6. **Plan section**: `<plan path="plan.md">\nL001: ...\nL002: ...\n</plan>`
7. **Context sections**: one `<context path="file.md">...</context>` block per file, line-numbered.
8. **Step IDs** (if inferred): list of `P-001: "step text"` for the model to reference in `blocks` fields.
9. **Caps reminder**: "Return at most {maxIssues} issues and {maxQuestions} questions."

### 7.2 Repair prompt

Function: `BuildRepair(originalOutput string, errors []schema.ValidationError) string`

Construct a follow-up prompt that includes:
- The original (invalid) output
- The list of validation errors
- Instructions to fix only the errors and return corrected JSON

### 7.3 Tests

- `TestBuild`: verify prompt contains plan lines with L-prefix, profile checklist text, schema description, context blocks.
- `TestBuildStrict`: strict mode adds grounding language.
- `TestBuildRepair`: includes original output and error descriptions.

---

## Phase 8: Strict Grounding Post-Check

This is part of `internal/review` (or a subpackage) rather than a separate package.

### 8.1 Fabrication heuristic scanner

Function: `CheckGrounding(review *Review) []GroundingViolation`

Scan `description`, `impact`, and `recommendation` fields of all issues for phrases that suggest fabricated repo knowledge:
- "the codebase uses"
- "the repository contains"
- "the existing implementation"
- "currently the system"
- "as seen in the source"
- "the project's [X] file"
- Other configurable patterns

Each match produces a `GroundingViolation{IssueID, Field, Phrase}`.

### 8.2 Apply grounding violations

Function: `ApplyGroundingDowngrades(review *Review, violations []GroundingViolation)`

For each violation:
- Add `"UNVERIFIED"` to the issue's tags.
- If severity is CRITICAL, downgrade to WARN.

### 8.3 Tests

- `TestCheckGrounding`: issues with fabricated phrases detected; clean issues pass.
- `TestApplyGroundingDowngrades`: tag added, severity downgraded.

---

## Phase 9: Markdown Renderer (`internal/render`)

### 9.1 Render function

Function: `Markdown(review *review.Review) string`

Render sections in this order per spec:
1. **Summary**: verdict, score, counts as a heading + table or bullet list.
2. **Critical Issues**: each with title, category badge, evidence quotes (blockquote), impact, recommendation.
3. **Warnings**: same format.
4. **Info**: same format.
5. **Questions**: question text, why needed, suggested answers, evidence.
6. **Suggested Patches**: title + fenced diff block.
7. **Context Used**: list of context file paths.

Use `strings.Builder` for assembly. No template engine needed — the structure is fixed.

### 9.2 Tests

- `TestMarkdown`: render a known review, verify output contains expected headings, issue titles, evidence blockquotes. Use substring checks rather than exact match to avoid brittleness.
- `TestMarkdownEmpty`: empty issues/questions produce "No issues found" or equivalent.

---

## Phase 10: Patch Output (`internal/patch`)

### 10.1 Write patch file

Function: `WritePatchFile(patches []review.Patch, outPath string) error`

Concatenate all `diff_unified` fields from patches and write to the output file. If no patches exist, write nothing and return nil (no empty file).

### 10.2 Tests

- `TestWritePatchFile`: writes concatenated diffs.
- `TestWritePatchFileEmpty`: no patches, no file created.

---

## Phase 11: CLI (`cmd/plancritic`)

### 11.1 Root command and `check` subcommand

Set up Cobra with:
- Root command: `plancritic` with version flag.
- `check` subcommand: takes one positional arg (plan file path).

### 11.2 Wire all flags

Register all flags from spec on the `check` command:
- `--format` (string, default "json")
- `--out` (string, default "")
- `--context` (string slice)
- `--profile` (string, default "general")
- `--strict` (bool)
- `--model` (string)
- `--max-tokens` (int, default 4096)
- `--temperature` (float64, default 0.2)
- `--seed` (int, optional)
- `--severity-threshold` (string, default "info")
- `--patch-out` (string)
- `--fail-on` (string)
- `--redact` (bool, default true)
- `--offline` (bool)
- `--verbose` (bool)
- `--debug` (bool)

### 11.3 Implement `check` run function

This is the main orchestration. Steps in order:

1. **Validate inputs**: plan file exists and is readable. Context files exist. Profile name is valid. Exit code 3 on failure.
2. **Load plan**: `plan.Load(path)`. Infer step IDs.
3. **Load context files**: `context.Load(path)` for each `--context`.
4. **Redact** (if `--redact`): apply `redact.Redact` to plan and context raw text.
5. **Load profile**: `profile.LoadBuiltin(name)`.
6. **Resolve LLM provider**: `llm.ResolveProvider(modelFlag)`. Exit code 4 on failure.
7. **Build prompt**: `prompt.Build(...)`.
8. **Verbose/debug output**: if `--verbose`, log each step to stderr. If `--debug`, write prompt to `plancritic-debug-prompt.txt` (redacted).
9. **Call LLM**: `provider.Generate(ctx, prompt, settings)`. Exit code 4 on error.
10. **Parse JSON**: `json.Unmarshal` the response into `review.Review`.
11. **Validate**: `schema.Validate(review, planLineCount)`. If errors:
    - Build repair prompt, call LLM again (once).
    - Re-parse and re-validate.
    - If still invalid, exit code 5 with validation errors on stderr.
12. **Post-process**:
    - Recompute score (overwrite LLM's score with deterministic calculation).
    - Recompute summary counts and verdict.
    - Sort issues and questions.
    - Truncate if over limits.
    - If `--strict`, run grounding post-check and apply downgrades.
    - Apply `--severity-threshold` filter (remove issues/questions below threshold).
13. **Fill `input` and `meta` fields**: plan hash, context hashes, profile name, strict flag, model, temperature.
14. **Output**:
    - If `--format json`: marshal to indented JSON.
    - If `--format md`: call `render.Markdown`.
    - Write to `--out` file or stdout.
15. **Patch output**: if `--patch-out`, call `patch.WritePatchFile`.
16. **Exit code**: if `--fail-on` is set, compare verdict against threshold. Exit 2 if meets/exceeds.

### 11.4 Environment variable support

Also support configuration via env vars as fallback for common flags:
- `PLANCRITIC_MODEL` → `--model`
- `ANTHROPIC_API_KEY` → used by Anthropic provider
- `OPENAI_API_KEY` → used by OpenAI provider
- `PLANCRITIC_NO_TELEMETRY` → acknowledged (no-op for Phase 1)

---

## Phase 12: Golden Tests

### 12.1 Test fixtures

Create `testdata/` directory with:
- `testdata/plans/simple.md` — a small plan with deliberate contradictions, ambiguities, and missing prerequisites.
- `testdata/contexts/constraints.md` — a context file with constraints the plan violates.
- `testdata/golden/simple-review.json` — the expected review output.

### 12.2 Mock LLM provider

Create `internal/llm/mock.go`:
```go
type MockProvider struct {
    Response string
    Err      error
}
func (m *MockProvider) Generate(ctx context.Context, prompt string, s Settings) (string, error) {
    return m.Response, m.Err
}
func (m *MockProvider) Name() string { return "mock" }
```

### 12.3 Golden test implementation

Write a test that:
1. Loads the fixture plan and context.
2. Injects the mock provider with the canned JSON from the golden file.
3. Runs the full pipeline (prompt build → "LLM call" → validate → post-process → output).
4. Asserts:
   - Output JSON is valid per schema validation.
   - Issues are sorted correctly.
   - Score matches deterministic calculation.
   - Contains expected issue IDs/titles.
   - Ordering is stable across runs.

---

## Phase 13: Integration Test (Optional)

### 13.1 Live provider test

A test gated by `PLANCRITIC_INTEGRATION=1` env var:
1. Requires a real API key.
2. Sends a tiny plan (5-10 lines) through the real pipeline.
3. Asserts only: output parses as JSON, validates against schema, has non-empty issues or questions.
4. Does NOT assert specific content (too variable).

---

## Phase 14: Deliverables and Polish

### 14.1 README.md

Write `README.md` covering:
- What PlanCritic does (one paragraph).
- Installation: `go install github.com/dshills/plancritic/cmd/plancritic@latest`.
- Configuration: setting API key env vars.
- Usage examples (from spec's CLI examples section).
- Profiles: list built-in profiles, how to see available profiles.
- Strict mode explanation.
- Exit codes reference.

### 14.2 Example files

Create `examples/` directory with:
- `examples/sample-plan.md` — a realistic plan with issues.
- `examples/sample-review.json` — the corresponding review output.
- `examples/sample-review.md` — the Markdown rendering.

### 14.3 Makefile

```makefile
build:
	go build -o plancritic ./cmd/plancritic

test:
	go test ./...

lint:
	golangci-lint run

install:
	go install ./cmd/plancritic
```

---

## Implementation Order Summary

The phases above are ordered by dependency. Within each phase, implement types → functions → tests before moving to the next phase. The critical path is:

```
Phase 1 (scaffold)
  → Phase 2 (review types — everything depends on these)
    → Phase 3 (plan, context, redact — independent of each other, parallel)
    → Phase 4 (profiles)
    → Phase 5 (LLM providers)
    → Phase 6 (schema validation)
    → Phase 7 (prompt construction — depends on 3, 4)
    → Phase 8 (strict grounding — depends on 2)
    → Phase 9 (markdown render — depends on 2)
    → Phase 10 (patch output — depends on 2)
  → Phase 11 (CLI — wires everything together)
    → Phase 12 (golden tests)
    → Phase 13 (integration test)
    → Phase 14 (deliverables)
```

Phases 3, 4, 5, 6, 8, 9, 10 can be developed in parallel once Phase 2 is complete. Phase 7 requires 3 and 4. Phase 11 requires all preceding phases.
