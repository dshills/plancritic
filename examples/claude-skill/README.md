# plancritic Claude Code skill

A [Claude Code](https://claude.com/claude-code) skill that teaches Claude when and how to run `plancritic` against a `PLAN.md` before code generation begins.

Once installed, Claude will invoke plancritic with the right flags, grounding context, and profile; parse the JSON result; and report the verdict, critical issues, open questions, and suggested patches in a structured form.

## Install

Copy `SKILL.md` into your Claude Code skills directory:

```bash
mkdir -p ~/.claude/skills/plancritic
cp SKILL.md ~/.claude/skills/plancritic/SKILL.md
```

Claude Code will pick it up on the next session.

## Prerequisites

- `plancritic` on `$PATH`:
  ```bash
  go install github.com/dshills/plancritic/cmd/plancritic@latest
  ```
- `ANTHROPIC_API_KEY` (preferred) or `OPENAI_API_KEY` set in the environment.

## Usage

Trigger the skill by asking Claude to review, validate, critique, or check a plan — for example:

- "run plancritic on PLAN.md"
- "validate this plan before we start coding"
- "critique the implementation plan"

Claude will:

1. Select a profile from repo signals (`go-backend`, `react-frontend`, `aws-deploy`, or `general`).
2. Pass `SPEC.md` and a repo tree as `--context` when available.
3. Run `plancritic check` with `--format json --out /tmp/plancritic.json --patch-out /tmp/plancritic.patch --fail-on not_executable`.
4. Report verdict, score, and issues grouped by severity and category.
5. Halt on `NOT_EXECUTABLE`; wait for answers on `EXECUTABLE_WITH_CLARIFICATIONS`; green-light on `EXECUTABLE_AS_IS`.

## Customize

`SKILL.md` is a plain Markdown file with YAML frontmatter. Edit your installed copy to:

- Add project-specific context files that should always be passed via `--context` (architecture docs, compliance controls, API contracts).
- Add or rename profiles to match your team's conventions.
- Change the default `--fail-on` threshold or add `--strict` by default.

## Pipeline context

plancritic is the last gate before code is written:

```
SPEC.md → speccritic → PLAN.md → plancritic → CODE → realitycheck → prism → clarion
```

Companion skills for the other gates exist in their respective project repos.
