---
name: plancritic
description: Run plancritic against PLAN.md (or another implementation plan) to validate it before code generation begins. Use when the user invokes /plancritic, asks to review/validate/critique/check a plan, or whenever a PLAN.md is about to be handed off for implementation. Surfaces contradictions, ambiguities, missing prerequisites, risks, test gaps, and ordering problems as a structured JSON critique with a verdict and score.
disable-model-invocation: true
allowed-tools: Bash, Read, Write
---

# plancritic

Pipeline gate that critiques an implementation plan before any code is written. Reads `PLAN.md`, optionally grounded with context files (SPEC, repo tree, constraints), and returns a structured JSON review with issues, questions, and suggested patches.

Pipeline position: `SPEC.md → speccritic → PLAN.md → plancritic → CODE → realitycheck → prism → clarion`. plancritic is the **last gate before code is written**. Do not proceed to implementation until it returns an executable verdict (or the user explicitly waives blocking issues).

## Preconditions

Before running:

1. `PLAN.md` exists in the repo root (or the user named another plan file).
2. `speccritic` has already passed against `SPEC.md`. If `SPEC.md` exists but speccritic has not been run, suggest running `/speccritic` first rather than reviewing a plan against an unvalidated spec.
3. `plancritic` is on `$PATH`. Verify with `command -v plancritic`. If missing, instruct the user to install:
   ```bash
   go install github.com/dshills/plancritic/cmd/plancritic@latest
   ```
4. `ANTHROPIC_API_KEY` (preferred) or `OPENAI_API_KEY` is set in the environment.

## Profile Selection

Pick the profile from repo signals — do not ask the user unless the signals genuinely conflict:

| Repo signal | Profile |
|---|---|
| `go.mod` present | `go-backend` |
| `package.json` with React + TypeScript | `react-frontend` |
| Terraform / CDK / SAM / CloudFormation | `aws-deploy` |
| Polyglot or unclear | `general` |

## Context Files

plancritic critiques what it can see. Always pass relevant grounding via `--context`:

- `SPEC.md` — required if it exists; plan is critiqued against the spec.
- Repo tree (`tree -L 3 -I 'node_modules|vendor|.git' > /tmp/tree.txt`) — gives the model structural awareness.
- Architecture / constraints docs — anything in `docs/` describing invariants, contracts, or non-functional requirements.
- Compliance / regulatory docs — pass any domain-specific controls documents (e.g. `docs/cfr11-controls.md` for clinical systems) so those obligations are in scope.

If context is thin or the codebase is unfamiliar, add `--strict`. This forces the model to cite evidence rather than assume repo state, and downgrades unverifiable claims to `UNVERIFIED`.

## Workflow

1. **Run plancritic with JSON output**, even if the user wants a Markdown summary later — JSON is what gets parsed and acted on.

   ```bash
   plancritic check PLAN.md \
     --context SPEC.md \
     --context /tmp/tree.txt \
     --profile <selected-profile> \
     --format json \
     --out /tmp/plancritic.json \
     --patch-out /tmp/plancritic.patch \
     --fail-on not_executable
   ```

   Add `--strict` for unfamiliar repos. Add other `--context` flags as appropriate.

2. **Capture the exit code.** `0` = passed the fail threshold. `2` = verdict at/above the fail threshold (blocking). `3/4/5` = input, provider, or schema errors — surface these as setup problems, not plan problems.

3. **Parse `/tmp/plancritic.json`** and report in this order:

   - **Verdict and score** — one line. e.g. `EXECUTABLE_WITH_CLARIFICATIONS · score 72 · 2 critical, 5 warn, 7 info`.
   - **Critical issues** — every one, with file/section reference and the cited evidence excerpt. These are blocking.
   - **Open questions** — list them; these are what the user needs to answer to unblock execution.
   - **Warnings** — group by category (`RISK_SECURITY`, `TEST_GAP`, `ORDERING_DEPENDENCY`, etc.). Summarize rather than dump.
   - **Info** — only mention if the count is small or if a specific item is genuinely actionable.

4. **Cross-reference against SPEC.md.** plancritic checks plan internal consistency; the gate also needs to check spec coverage. Flag:
   - Plan steps that implement work not in the spec (scope creep).
   - Spec requirements with no corresponding plan step (coverage gap).
   - Plan ordering that contradicts the spec's backward-planned delivery sequence.

5. **Patches.** If `/tmp/plancritic.patch` is non-empty, summarize what the patch changes and ask whether to apply it (`git apply /tmp/plancritic.patch`). Never apply automatically — the user reviews plan edits before they land.

6. **Decide.**
   - `EXECUTABLE_AS_IS` → confirm the plan is ready, suggest committing PLAN.md, then proceed to implementation.
   - `EXECUTABLE_WITH_CLARIFICATIONS` → list the questions, wait for answers, recommend re-running plancritic after the plan is updated.
   - `NOT_EXECUTABLE` → halt. The plan must be revised before any code is written. Offer concrete patch suggestions from the JSON or from the `--patch-out` diff.

## Re-run Discipline

After the user revises PLAN.md, re-run plancritic with the same flags. A revision that resolves the cited issues should move the verdict up and the score should rise meaningfully (>10 points). If the score barely changes, the revision did not actually address the cited evidence — say so plainly.

## Flag Reference (most-used)

| Flag | When to use |
|---|---|
| `--context <path>` (repeatable) | Always. Pass SPEC, tree, constraints. |
| `--profile <name>` | Always. Match the repo. |
| `--strict` | Unfamiliar repos, or when the plan makes unverifiable claims about codebase state. |
| `--format json` | Default for parsing. Use `md` only when the user asks for a human-readable report. |
| `--out <path>` | Always write JSON to a file so it can be re-read. |
| `--patch-out <path>` | Always — costs nothing and gives the user an applyable diff if patches are suggested. |
| `--fail-on not_executable` | Hard gate. Use in CI and in manual runs where blocking matters. |
| `--severity-threshold warn` | When info-level chatter is drowning out signal in a large plan. |
| `--model <id>` | Only when the user explicitly overrides. Default model is fine. |
| `--seed <int>` | When reproducing a previous run for comparison. |
| `--debug` | Only when troubleshooting a suspected redaction or prompt issue. |

## Issue Category Cheatsheet

When summarizing issues, group them by category so the user can scan:

- **CONTRADICTION** — plan steps that conflict with each other or with the spec
- **AMBIGUITY** — wording that admits multiple valid implementations
- **MISSING_PREREQUISITE** — a step depends on something the plan never establishes
- **MISSING_ACCEPTANCE_CRITERIA** — no observable definition of done
- **RISK_SECURITY / RISK_DATA / RISK_OPERATIONS** — threat surface introduced by the plan
- **TEST_GAP** — work that ships without verification
- **SCOPE_CREEP_RISK** — work outside the spec's stated scope
- **UNREALISTIC_STEP** — a step that cannot be done as written
- **ORDERING_DEPENDENCY** — steps in the wrong sequence
- **UNSPECIFIED_INTERFACE** — contract between components is undefined
- **NON_DETERMINISM** — outcomes depend on unstated environmental state

## What Not to Do

- Do not re-run plancritic to "see if it passes this time" without a real plan revision in between. The output is non-deterministic enough that this hides problems.
- Do not summarize away CRITICAL issues. Quote them with their evidence.
- Do not proceed to code generation on a `NOT_EXECUTABLE` verdict, even if the user pushes — the gate exists to prevent expensive downstream rework. Push back, then defer to the user if they explicitly waive.
- Do not apply `--patch-out` diffs automatically. Plan edits are the user's call.
