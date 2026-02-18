# PlanCritic

A CLI tool that reviews software implementation plans and returns structured critique: contradictions, ambiguities, missing prerequisites, questions, and suggested patches.

## Installation

```bash
go install github.com/dshills/plancritic/cmd/plancritic@latest
```

Or build from source:

```bash
make build
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

# Generate patch suggestions
plancritic check plan.md --patch-out fixes.diff

# CI mode: exit non-zero if plan is not executable
plancritic check plan.md --fail-on critical

# Filter to warnings and above only
plancritic check plan.md --severity-threshold warn
```

## Profiles

Built-in profiles steer the critique with domain-specific checklists:

| Profile | Description |
|---------|-------------|
| `general` | Language-agnostic baseline (default) |
| `go-backend` | Go backend: minimal deps, explicit contracts, MySQL conventions |
| `react-frontend` | React/TypeScript: components, state, accessibility, bundle size |
| `aws-deploy` | AWS infrastructure: IAM, networking, rollback, IaC, cost |
| `davin-go` | Opinionated Go backend house rules |

## Strict Mode

With `--strict`, the model treats everything not in the plan or context files as unknown. Uncertain inferences are capped at WARN severity and tagged with `"assumption"`. A post-check scans for phrases suggesting fabricated knowledge and downgrades those issues.

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success, verdict below fail threshold |
| 2 | Verdict meets/exceeds `--fail-on` threshold |
| 3 | Input error (missing file, bad format) |
| 4 | Model/provider error |
| 5 | Schema validation error (model returned invalid JSON) |

## Output Format

JSON output follows a strict schema with:
- **Summary**: verdict, score (0-100), severity counts
- **Issues**: categorized problems with evidence citations (line numbers + quotes)
- **Questions**: minimum set needed to unblock execution
- **Patches**: optional unified diffs for plan text edits

Score is computed deterministically: start at 100, subtract 20 per CRITICAL, 7 per WARN, 2 per INFO.
