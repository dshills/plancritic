Product: PlanCritic CLI

Goal

Create a CLI tool that reviews a software implementation plan (typically written by a coding agent) and returns:
	•	questions to resolve ambiguity,
	•	detected contradictions and inconsistencies,
	•	missing prerequisites and risks,
	•	concrete patches to make the plan executable,
	•	a deterministic, structured result suitable for feeding back into a coding agent.

Non-goals (Phase 1)
	•	No codebase/commit/PR review.
	•	No interactive TUI.
	•	No online browsing.
	•	No “auto-fix code.” Only critique plan text and optionally propose plan edits.

Design principles
	•	Evidence-driven: Every issue references a specific plan excerpt (line range or quoted snippet).
	•	Structured output first: JSON is the primary output; Markdown is a rendering of JSON.
	•	Anti-hallucination: The model must not invent missing repo facts. It may only reason about what’s in the plan and explicitly provided context files.
	•	Deterministic enough: stable outputs across runs when inputs unchanged (use temperature control, schema validation, ordering rules).

⸻

User stories
	1.	As an engineer, I run plancritic check plan.md and get a list of contradictions, ambiguities, and missing prerequisites.
	2.	As a coding-agent operator, I want --format json so I can programmatically feed the results back into another agent.
	3.	As a team lead, I want a Markdown report I can paste into a PR comment.
	4.	As a paranoid adult, I want a “strict grounding mode” that forbids unstated assumptions.

⸻

CLI UX

Commands

plancritic check
Primary command: analyze a plan and produce a review.

Examples
	•	plancritic check plan.md
	•	plancritic check plan.md --format json --out review.json
	•	plancritic check plan.md --context constraints.md --context tree.txt
	•	plancritic check plan.md --profile go-backend
	•	plancritic check plan.md --strict --severity-threshold warn
	•	plancritic check plan.md --patch-out patch.diff

Flags
	•	--format : json (default) | md
	•	--out : output file path (default: stdout)
	•	--context <path> : may be specified multiple times; provides additional grounding material (constraints, architecture notes, API docs, repo tree, etc.)
	•	--profile <name> : apply a predefined checklist and constraints pack (see Profiles)
	•	--strict : enables strict grounding behavior
	•	--model <id> : model selection (implementation-specific)
	•	--max-tokens <n> : cap response size (default sane value)
	•	--temperature <float> : default 0.2 (or equivalent); enforce determinism
	•	--seed <int> : if supported
	•	--severity-threshold : info|warn|critical (default info) – minimum severity included in output
	•	--patch-out <path> : emit suggested plan edits as unified diff against the original plan file
	•	--fail-on <level> : exit non-zero if verdict is at or above this (e.g., critical)
	•	--redact : redacts secrets patterns in input before sending to model (default enabled)
	•	--offline : fail if no model provider is configured (no silent fallback)

Exit codes
	•	0 success, verdict below fail threshold
	•	2 verdict meets/exceeds fail threshold (e.g., not executable)
	•	3 input error (missing file, unreadable)
	•	4 model/provider error
	•	5 schema validation error (model returned invalid JSON)

⸻

Inputs

Plan input
	•	Plain text or Markdown.
	•	Must preserve line numbers for citations.
	•	Plan can include sections and bullet points.

Context inputs (optional)
	•	One or more files: .md, .txt, .json, etc.
	•	These are treated as ground truth for constraints only. The model must reference them explicitly in issues when used.

Redaction

Before sending to model, apply redaction rules:
	•	Replace values matching patterns (AWS keys, tokens, passwords, private keys) with [REDACTED].
	•	Keep enough structure for critique (“auth token needed”) but remove secrets.

⸻

Outputs

Canonical JSON schema (Phase 1)

The tool MUST produce JSON matching this schema (validate strictly).

Top-level

{
  "tool": "plancritic",
  "version": "1.0",
  "input": {
    "plan_file": "plan.md",
    "plan_hash": "sha256:...",
    "context_files": [
      { "path": "constraints.md", "hash": "sha256:..." }
    ],
    "profile": "go-backend",
    "strict": true
  },
  "summary": {
    "verdict": "EXECUTABLE_WITH_CLARIFICATIONS",
    "score": 72,
    "critical_count": 2,
    "warn_count": 5,
    "info_count": 7
  },
  "questions": [ /* see below */ ],
  "issues": [ /* see below */ ],
  "patches": [ /* optional */ ],
  "checklists": [ /* optional */ ],
  "meta": {
    "model": "provider/model",
    "temperature": 0.2
  }
}

Verdict enum
	•	EXECUTABLE_AS_IS
	•	EXECUTABLE_WITH_CLARIFICATIONS
	•	NOT_EXECUTABLE

Issue object

{
  "id": "ISSUE-0001",
  "severity": "CRITICAL",
  "category": "CONTRADICTION",
  "title": "Plan contradicts dependency constraint",
  "description": "The plan states no new dependencies, but later adds Redis client library.",
  "evidence": [
    {
      "source": "plan",
      "path": "plan.md",
      "line_start": 42,
      "line_end": 45,
      "quote": "We will keep the project dependency-free..."
    },
    {
      "source": "plan",
      "path": "plan.md",
      "line_start": 88,
      "line_end": 90,
      "quote": "Add github.com/go-redis/redis for caching..."
    }
  ],
  "impact": "Implementation will violate stated constraints and complicate build/review.",
  "recommendation": "Either remove the dependency or revise the constraint and justify it.",
  "blocking": true,
  "tags": ["deps", "consistency"]
}

Category enum (Phase 1)
	•	CONTRADICTION
	•	AMBIGUITY
	•	MISSING_PREREQUISITE
	•	MISSING_ACCEPTANCE_CRITERIA
	•	RISK_SECURITY
	•	RISK_DATA
	•	RISK_OPERATIONS
	•	TEST_GAP
	•	SCOPE_CREEP_RISK
	•	UNREALISTIC_STEP
	•	ORDERING_DEPENDENCY (steps out of order)
	•	UNSPECIFIED_INTERFACE (API/schema unspecified)
	•	NON_DETERMINISM (plan relies on “optimize later” without metrics)

Severity enum
	•	INFO, WARN, CRITICAL

Question object
Questions are “minimum set” to unblock execution.

{
  "id": "Q-0003",
  "severity": "WARN",
  "question": "What is the latency SLO for the search endpoint?",
  "why_needed": "The plan proposes caching and pagination but lacks target performance metrics.",
  "blocks": ["P-004"],
  "evidence": [
    { "source": "plan", "path": "plan.md", "line_start": 120, "line_end": 123, "quote": "Ensure search is fast." }
  ],
  "suggested_answers": [
    "p95 < 250ms for typical queries (top 50)",
    "p95 < 500ms with cold cache"
  ]
}

Patches
Patches are optional suggestions to edit the plan for executability.

{
  "id": "PATCH-0001",
  "type": "PLAN_TEXT_EDIT",
  "title": "Add acceptance criteria to performance section",
  "diff_unified": "--- plan.md\n+++ plan.md\n@@ ...\n"
}

Score
0–100. Must be computed deterministically from counts/weights:
	•	Start at 100
	•	-20 per CRITICAL
	•	-7 per WARN
	•	-2 per INFO
	•	Clamp at 0
This is intentionally dumb but stable.

⸻

Plan parsing & references

Line numbering
	•	Read plan file, split by \n.
	•	Provide the model with line numbers in the prompt (e.g., prefix each line with L###:).
	•	Evidence objects must include line_start and line_end and a quote.

Plan item IDs (optional but recommended)

The tool may optionally label plan steps:
	•	If the plan has numbered headings or bullets, infer IDs (P-001, P-002).
	•	Otherwise, generate IDs in order of appearance for top-level steps.

These IDs can be used in blocks fields.

⸻

Profiles

Profiles are bundled checklists + constraints that steer critique.

Built-in profiles (Phase 1)
	1.	general (default)
	2.	go-backend
	3.	react-frontend
	4.	aws-deploy

Each profile includes:
	•	A checklist of common failure points (auth, migrations, logging, error handling, tests, rollback).
	•	Constraint hints (e.g., dependency policy, linting, target DB).

Profiles must be implemented as local files embedded in the binary (or packaged).

Example profile snippet (conceptual):

name: go-backend
constraints:
  - Prefer standard library; minimize deps.
  - Explicit error handling; no panic for control flow.
checks:
  - Are API contracts specified?
  - Are migrations and rollback described?
  - Are tests mapped to behaviors?
  - Are observability and logs included?


⸻

Strict grounding mode

When --strict is enabled:
	•	The model must treat everything not present in plan/context as unknown.
	•	Issues must not claim “the repo uses X” unless present in context.
	•	Recommendations may be generic but must be labeled as such (“If applicable…”).
	•	Any uncertain inference must be flagged with tags += ["assumption"] and severity capped at WARN.

Additionally, implement a post-check in the CLI:
	•	Scan returned description/impact/recommendation for forbidden phrases suggesting fabricated repo context (heuristic list).
	•	If detected, downgrade or annotate issue as “UNVERIFIED” in output meta (don’t discard; mark).

⸻

LLM interaction

Providers

Phase 1 can support one provider (OpenAI or Anthropic), but structure the code for multiple.

Provider interface:
	•	Generate(ctx, prompt, settings) -> string
	•	Must support:
	•	model id
	•	temperature
	•	max tokens
	•	optional seed

Prompting contract

The LLM MUST be instructed to output only JSON matching the schema.
No Markdown, no prose outside JSON.

Prompt must include:
	•	The schema definition (or a compact formal description).
	•	The plan with line numbers.
	•	The context files (also line-numbered or clearly delimited).
	•	The profile checklist.
	•	Rules:
	•	cite evidence for each issue/question
	•	do not invent repo facts
	•	keep question count minimal (cap)
	•	order issues by severity then by appearance

Output validation
	•	Parse JSON.
	•	Validate schema:
	•	required fields present
	•	enums valid
	•	evidence line ranges valid
	•	If invalid:
	•	retry once with a “repair” prompt including validation errors and the original output
	•	if still invalid, exit code 5

Ordering rules
	•	issues: sort by severity (CRITICAL > WARN > INFO), then by evidence[0].line_start.
	•	questions: same ordering.

Limits
	•	Cap total issues to e.g. 50 and total questions to 20.
	•	If more found, include a single WARN issue: “Output truncated; increase limits.”

⸻

Rendering Markdown

If --format md, render from JSON deterministically:

Sections:
	1.	Summary (verdict, score, counts)
	2.	Critical issues
	3.	Warnings
	4.	Info
	5.	Questions
	6.	Suggested patches (if any)
	7.	Context used

Each issue shows:
	•	Title + severity/category
	•	Evidence quotes (short)
	•	Impact
	•	Recommendation

⸻

Testing requirements

Unit tests
	1.	Redaction: secrets replaced, structure preserved.
	2.	Line numbering: correct mapping.
	3.	Schema validation: rejects invalid enums/missing fields.
	4.	Ordering: issues sorted properly.
	5.	Score calculation: deterministic.
	6.	Markdown renderer: stable output.

Golden tests

Given a sample plan file, assert:
	•	JSON output passes schema validation.
	•	Contains specific issues/questions (by ID or title).
	•	Deterministic ordering.

(Use a mocked LLM provider returning canned JSON.)

Integration test (optional)

If CI has provider keys:
	•	Run against a tiny plan
	•	Ensure output parses and validates
	•	Don’t assert content (too variable), only schema + non-empty.

⸻

Security & privacy
	•	Default redaction on.
	•	Never write raw plan/context into logs unless --debug and even then redact.
	•	Support PLANCRITIC_NO_TELEMETRY=1 (default off; if telemetry exists later).
	•	Document that input content is sent to the configured model provider.

⸻

Observability
	•	--verbose prints steps (read files, redact, call model, validate, render).
	•	--debug may save the exact prompt to a local file (redacted), for reproducibility.

⸻

Internal architecture

Packages/modules (suggested)
	•	cmd/plancritic (cobra/urfave/flag)
	•	internal/plan (read, line-number, hash)
	•	internal/context (load, line-number)
	•	internal/redact (pattern-based redaction)
	•	internal/profile (load profile checklists)
	•	internal/llm (provider interface + implementations)
	•	internal/schema (JSON schema validation)
	•	internal/review (types + sorting + scoring)
	•	internal/render (md renderer)
	•	internal/patch (diff generation for plan text edits)

Data flow
	1.	Load plan + contexts
	2.	Redact
	3.	Build prompt with rules + schema + plan + context + profile
	4.	Call LLM
	5.	Parse & validate JSON
	6.	Post-process: scoring, sorting, truncation warnings
	7.	Optionally write patch diff
	8.	Output JSON or render Markdown

⸻

Extensibility hooks for Phase 2 (commit vs plan)

Do not implement now, but ensure these seams exist:
	•	ReviewInput struct includes optional Artifacts list (diffs, test output)
	•	Prompt builder supports additional “evidence sources”
	•	Output schema could add coverage later without breaking v1 (version bump or optional field)

⸻

Acceptance criteria (Phase 1)

The tool is considered complete when:
	1.	plancritic check plan.md produces valid JSON with a verdict, score, issues, and questions.
	2.	Output JSON validates against the schema and is stable in ordering.
	3.	Evidence references are correct line ranges from input.
	4.	--format md produces readable report.
	5.	--patch-out writes a unified diff when patches exist.
	6.	Unit tests and golden tests pass.
	7.	--strict reduces speculative claims and enforces grounding behaviors.

⸻

Deliverables
	•	Source code
	•	README.md including:
	•	install/build instructions
	•	configuration of model provider keys
	•	examples
	•	explanation of strict mode
	•	Example plans + example outputs (examples/)
	•	Schema file (schema/review.v1.json) if using JSON Schema formally

⸻

Implementation note to the coding model

Do not “hand-wave” the LLM output. The core of the product is:
	•	prompt discipline,
	•	schema validation,
	•	deterministic rendering,
	•	evidence citations.

Everything else is garnish.

