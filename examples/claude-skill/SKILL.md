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

1. **Run plancritic**, keeping the full JSON for the next run's `--baseline`:

   ```bash
   plancritic check PLAN.md \
     --spec SPEC.md \
     --context /tmp/tree.txt \
     --profile <selected-profile> \
     --format json \
     --out /tmp/plancritic.json \
     --quiet \
     --fail-on not_executable
   ```

   `--quiet` prints only the `VERDICT` line to stdout. Add `--strict` for unfamiliar repos and other `--context` flags as appropriate. Add `--patch-out /tmp/plancritic.patch` only when the user wants suggested plan edits, because it asks the model to write them.

2. **Capture the exit code.** `0` = passed the fail threshold. `2` = verdict at/above the fail threshold (blocking). `3/4/5` = input, provider, or schema errors — surface these as setup problems, not plan problems.

3. **Read the result in compact form.** Re-run the identical command with `--format compact --out /tmp/plancritic.txt` in place of the JSON flags. The output format does not affect the cache key, so this second run is a local cache hit: no model call, no tokens, `cached` in the header. Read `/tmp/plancritic.txt`, which has one line per finding:

   - `VERDICT <verdict> score=<n> critical=<n> warn=<n> info=<n> ...` is always the first line.
   - `ISSUE-NNNN <SEVERITY>[(blocking)] <CATEGORY> PLAN.md:L<start>[-<end>] "<title>" -> <recommendation> [tags]`
   - `Q-NNNN <SEVERITY> PLAN.md:L<line> "<question>" -> <why needed>`
   - Findings tagged `local` come from a zero-token text check, not the model; treat them as low-priority hints.

   There is no quoted plan text in this format; open the cited PLAN.md lines when you need the wording. Report in this order:

   - **Verdict and score** — one line. e.g. `EXECUTABLE_WITH_CLARIFICATIONS · score 72 · 2 critical, 5 warn, 7 info`.
   - **Critical issues** — every one, with its line reference and the cited plan text. These are blocking.
   - **Open questions** — list them; these are what the user needs to answer to unblock execution.
   - **Warnings** — group by category (`RISK_SECURITY`, `TEST_GAP`, `ORDERING_DEPENDENCY`, etc.). Summarize rather than dump.
   - **Info** — only mention if the count is small or if a specific item is genuinely actionable.

4. **Spec coverage.** With `--spec`, the compact output carries a `COVERAGE covered=<n> partial=<n> uncovered=<n> out_of_scope=<n>` line, then one line per gap: `REQ-NNNN UNCOVERED|PARTIAL SPEC.md:L<line> "<requirement>" -> <what is missing>` and `SCOPE-NNNN OUT_OF_SCOPE PLAN.md:L<line> "<plan step>" -> <note>`. Covered requirements are only counted. Report every `UNCOVERED` and `PARTIAL` requirement and every out-of-scope item; do not re-derive coverage by reading both documents yourself.

5. **Patches.** If you passed `--patch-out` and `/tmp/plancritic.patch` is non-empty, summarize what the patch changes and ask whether to apply it (`git apply /tmp/plancritic.patch`). Never apply automatically — the user reviews plan edits before they land.

6. **Decide.**
   - `EXECUTABLE_AS_IS` → confirm the plan is ready, suggest committing PLAN.md, then proceed to implementation.
   - `EXECUTABLE_WITH_CLARIFICATIONS` → list the questions, wait for answers, recommend re-running plancritic after the plan is updated.
   - `NOT_EXECUTABLE` → halt. The plan must be revised before any code is written. Offer concrete fixes for the cited lines, or the `--patch-out` diff if one was requested.

## Wording Iterations

While rewording a plan (vague phrases, TODOs, empty sections) run `plancritic lint PLAN.md --format compact` between edits: it is instant and costs no tokens. Use `check` once the structure is settled.

## Re-run Discipline

After the user revises PLAN.md, copy the previous result aside
(`cp /tmp/plancritic.json /tmp/plancritic.prev.json`) and re-run steps 1 and 3 with
`--baseline /tmp/plancritic.prev.json` added to both commands. In compact output each
finding is then tagged `[new]` or `[persisting]`, resolved ones appear as `RESOLVED`
lines, and the `VERDICT` line gains `new=… persisting=… resolved=… score_change=…`.
Fingerprints match findings by cited text, not by ID or line number, so you do not have
to diff two reports by hand. A revision that resolves the cited issues should move the verdict up and the score should rise meaningfully (>10 points). If the score barely changes, the revision did not actually address the cited evidence — say so plainly.

## Flag Reference (most-used)

| Flag | When to use |
|---|---|
| `--spec <path>` | Whenever a spec exists. Adds the coverage matrix. |
| `--context <path>` (repeatable) | Repo tree, constraints, architecture docs. |
| `--profile <name>` | Always. Match the repo. |
| `--strict` | Unfamiliar repos, or when the plan makes unverifiable claims about codebase state. |
| `--format json` | For the file you keep as the next run's `--baseline`. |
| `--format compact` | For reading the result. One line per finding with `file:line` references and no quotes. Re-running with only the format changed is a free cache hit. Use `md` only when the user asks for a human-readable report. |
| `--quiet` | Print only the `VERDICT` line to stdout; pair with `--out`. |
| `--out <path>` | Always write to a file so the result can be re-read. |
| `--patch-out <path>` | When the user wants suggested plan edits. It asks the model to write patches, so it costs output tokens. |
| `--fail-on not_executable` | Hard gate. Use in CI and in manual runs where blocking matters. |
| `--severity-threshold warn` | When info-level chatter is drowning out signal in a large plan. |
| `--model <id>` | Only when the user explicitly overrides. Default model is fine. |
| `--baseline <path>` | On every re-run after a revision. Marks findings new, persisting, or resolved. |
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
