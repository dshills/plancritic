# PlanCritic

A CLI tool that reviews software implementation plans and returns structured critique: contradictions, ambiguities, missing prerequisites, questions, and suggested patches. Output is JSON-first (Markdown is rendered from JSON).

Designed for use by engineers and coding agents who want machine-readable feedback on a plan before execution begins.

## Installation

```bash
go install github.com/dshills/plancritic/cmd/plancritic@latest
```

Or build from source:

```bash
git clone https://github.com/dshills/plancritic.git
cd plancritic
go build -o plancritic ./cmd/plancritic
```

## Configuration

Set an API key for your LLM provider:

```bash
# Anthropic (preferred)
export ANTHROPIC_API_KEY=sk-ant-...

# OpenAI
export OPENAI_API_KEY=sk-...
```

If both are set, Anthropic is used by default. Use `--model` to override.

> **Privacy note:** Input content (plan and context files, after redaction) is sent to the configured model provider. Redaction is enabled by default.

## Usage

```bash
# Basic review (JSON output)
plancritic check plan.md

# Markdown report
plancritic check plan.md --format md

# With context files and a specific profile
plancritic check plan.md --context constraints.md --context tree.txt --profile go-backend

# Strict grounding mode (no assumptions about the codebase)
plancritic check plan.md --strict

# Write output to file
plancritic check plan.md --out review.json

# Generate patch suggestions (unified diff)
plancritic check plan.md --patch-out fixes.diff

# CI mode: exit non-zero if verdict is not executable
plancritic check plan.md --fail-on not_executable

# Filter to warnings and above only
plancritic check plan.md --severity-threshold warn

# Override model
plancritic check plan.md --model anthropic/claude-opus-4-6

# Verbose output (shows each pipeline stage)
plancritic check plan.md --verbose
```

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--format` | `json` | Output format: `json` or `md` |
| `--out` | stdout | Output file path |
| `--context <path>` | — | Additional grounding files (repeatable) |
| `--profile <name>` | `general` | Built-in checklist profile |
| `--strict` | false | Strict grounding mode (see below) |
| `--model <id>` | — | Model override |
| `--max-tokens <n>` | 4096 | Cap LLM response size |
| `--temperature <float>` | 0.2 | LLM temperature |
| `--seed <int>` | — | Seed for reproducibility (if supported) |
| `--severity-threshold` | `info` | Minimum severity included in output |
| `--patch-out <path>` | — | Write suggested plan edits as unified diff |
| `--fail-on <level>` | — | Exit code 2 if verdict meets/exceeds this level |
| `--redact` | true | Redact secrets before sending to model |
| `--offline` | false | Fail if no provider is configured |
| `--verbose` | false | Print pipeline steps |
| `--debug` | false | Save redacted prompt to local file |

## Profiles

Built-in profiles steer the critique with domain-specific checklists:

| Profile | Description |
|---------|-------------|
| `general` | Language-agnostic baseline (default) |
| `go-backend` | Go backend: minimal deps, explicit contracts, error handling, tests |
| `react-frontend` | React/TypeScript: components, state management, accessibility, bundle size |
| `aws-deploy` | AWS infrastructure: IAM least-privilege, networking, rollback, IaC, cost |
| `davin-go` | Opinionated Go backend house rules |

Profiles are embedded in the binary — no network access required.

## Strict Mode

With `--strict`, the model treats everything not present in the plan or context files as unknown:

- Issues must not claim "the repo uses X" unless it appears in provided context.
- Uncertain inferences are capped at WARN severity and tagged with `"assumption"`.
- A post-check scans descriptions for phrases suggesting fabricated repo knowledge and downgrades those issues to `UNVERIFIED`.

Use strict mode when reviewing plans for unfamiliar codebases or when you want conservative, citation-only output.

## Output Format

JSON output follows a strict schema:

```json
{
  "tool": "plancritic",
  "version": "1.0",
  "input": {
    "plan_file": "plan.md",
    "plan_hash": "sha256:...",
    "context_files": [],
    "profile": "general",
    "strict": false
  },
  "summary": {
    "verdict": "EXECUTABLE_WITH_CLARIFICATIONS",
    "score": 72,
    "critical_count": 2,
    "warn_count": 5,
    "info_count": 7
  },
  "questions": [ ... ],
  "issues": [ ... ],
  "patches": [ ... ],
  "meta": {
    "model": "anthropic/claude-opus-4-6",
    "temperature": 0.2
  }
}
```

### Verdicts

| Verdict | Meaning |
|---------|---------|
| `EXECUTABLE_AS_IS` | Plan is clear and complete enough to hand off |
| `EXECUTABLE_WITH_CLARIFICATIONS` | Minor gaps; answering questions unblocks execution |
| `NOT_EXECUTABLE` | Critical blockers; plan must be revised first |

### Score

Score is computed deterministically: start at 100, subtract 20 per CRITICAL, 7 per WARN, 2 per INFO, clamped at 0.

### Issue Categories

`CONTRADICTION`, `AMBIGUITY`, `MISSING_PREREQUISITE`, `MISSING_ACCEPTANCE_CRITERIA`, `RISK_SECURITY`, `RISK_DATA`, `RISK_OPERATIONS`, `TEST_GAP`, `SCOPE_CREEP_RISK`, `UNREALISTIC_STEP`, `ORDERING_DEPENDENCY`, `UNSPECIFIED_INTERFACE`, `NON_DETERMINISM`

Every issue includes evidence citations with line numbers and quoted excerpts from the plan.

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success, verdict below fail threshold |
| 2 | Verdict meets/exceeds `--fail-on` threshold |
| 3 | Input error (missing file, bad format) |
| 4 | Model/provider error |
| 5 | Schema validation error (model returned invalid JSON) |

## Examples

See the [`examples/`](examples/) directory for a sample plan, JSON review output, and Markdown report.

## License

MIT — see [LICENSE](LICENSE).
