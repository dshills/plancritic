# PlanCritic Improvements (agent-usage focus)

Scope: plancritic is run almost exclusively by coding agents (Claude Code, Codex) in a
"write plan → check → revise → re-check" loop. The items below are ranked by expected
payoff for that loop across three axes: **Accuracy**, **Speed/Throughput**, and **Tokens**
(both provider tokens billed per run and the tokens the *calling agent* spends reading the
output). Each item cites the code it changes and the evidence behind it.

## Measured baseline

Prompt built for this repo's own `specs/PLAN.md` (597 lines) with `specs/SPEC.md` as context,
`general` profile (measured via `prompt.BuildSegments`, 4 chars/token estimate):

| Segment | Chars | ~Tokens | Cached? |
|---|---|---|---|
| Preamble + schema + rules + profile | 4,517 | 1,129 | yes (Anthropic breakpoint) |
| Context (SPEC.md, line-numbered) | 17,040 | 4,260 | yes |
| Plan (line-numbered) | 25,843 | 6,460 | no |
| **Inferred Plan Steps list** | **18,973** | **4,740** | no |
| Caps line | ~50 | ~15 | no |
| **Total** | **~66k** | **~16.6k** | |

The uncached segment is 44k chars, and 43% of it is the inferred-steps list, which repeats
every heading *and every dash bullet* of the plan (255 "steps" for a 597-line plan).

---

## Priority list

### 1. Drop or compress the "Inferred Plan Steps" section  — Tokens, Accuracy

**Status: done** (commit `5a9a341`). Dash bullets are no longer steps; the prompt
emits a compact index with titles truncated to 60 runes. Measured: index 18,973 →
5,074 chars, total prompt ~65.7k → ~52.5k chars on the baseline.

**Evidence:** `plan.InferStepIDs` treats every `- ` bullet as a step; `prompt.BuildSegments`
then re-emits each one as `- P-123 (L45): <full line text>`. On the baseline plan this is
~4.7k tokens per run, every run, in the non-cacheable segment, and it is pure duplication of
text the model already has. The `P-NNN` IDs are only referenced by the free-form
`questions[].blocks` field, which nothing downstream parses.

**Change:** Remove the section, or (if P-IDs are kept) emit only heading-level steps as a
compact map (`P-001=L12,P-002=L40,…`) with no text. Measured saving: ~29% of total input
tokens on the baseline, more on bullet-heavy plans. Accuracy improves slightly because the
model is no longer invited to cite the duplicate copy instead of the plan lines.

**Effort:** Small (`internal/prompt/prompt.go`, `internal/plan/plan.go`, tests).

### 2. Local result cache keyed by input hash  — Speed, Tokens

**Status: done.** `internal/resultcache` stores reviews keyed on the exact prompt
text plus provider, effective model, temperature, seed, and max-tokens; output-only
flags are applied on hit. `--no-result-cache` bypasses it, `--no-cache` disables it
together with prompt caching, `PLANCRITIC_CACHE_DIR` relocates it, and hits carry
`meta.cached: true`. The SDK facade now materializes in-memory documents under
deterministic names so its callers get hits too.

**Evidence:** Agents routinely re-run with an unchanged plan (the shipped SKILL.md even warns
against it, which means it happens). Every such run pays full LLM latency and tokens for an
answer that already exists. The pipeline already computes `plan.Hash` and context hashes and
has a disk store (`internal/cachestore`) used only for Gemini cache handles.

**Change:** Before calling the provider, compute `sha256(plan hash, context hashes, profile
name+version, strict, provider/model, temperature, max-issues, max-questions,
severity-threshold)` and look it up in `~/.cache/plancritic/results/`. On hit, emit the stored
review with `meta.cached: true` and exit in milliseconds with zero tokens. `--no-cache`
already exists; extend it (or add `--no-result-cache`) to bypass. Store the *post-processed*
review so output flags (`--format`, `--severity-threshold`, `--out`) still apply.

**Effort:** Small-medium (`internal/reviewer/reviewer.go`, reuse `cachestore`).

### 3. Use provider-native structured output and retire the repair round trip  — Accuracy, Speed, Tokens

**Status: done.** `schema.ModelOutputSchema` is a strict-mode JSON Schema of the
model-facing shape (no tool/version/input/score/counts/meta, no quote). Anthropic
receives it as `output_config.format` (every family from 4.5 on, including the
default Sonnet 4.6, confirmed live), OpenAI as `response_format: json_schema`
(strict), Gemini as `responseJsonSchema`; all three verified live. If a model rejects
the schema (or, on Anthropic, `temperature`), the request is retried once without
that feature, so a stale capability list never fails a run. Remaining semantic
errors are first auto-fixed locally (duplicate or
empty IDs, inverted or overlong line ranges) and only then repaired by resending just
the offending items with the valid citation bounds. Also landed from item 10: Anthropic
requests omit `temperature` on model families that reject it, so `--model
claude-sonnet-5-5` and `claude-opus-5-5` work.

**Evidence:** Today the model is told "output ONLY valid JSON" and the runner then strips
fences (`llm.ExtractJSON`), rewrites bad escapes (`llm.SanitizeJSON`), validates, and on
failure makes a *second full LLM call* with the whole original output embedded in the repair
prompt (`prompt.BuildRepair`). The repair call also goes through `Generate`, not
`GenerateSegments`, so it gets no prompt-cache benefit. A repair roughly doubles wall-clock
and re-bills the entire output as input.

**Change:**
- Anthropic: send `output_config: {format: {type: "json_schema", schema: …}}` (structured
  outputs; supported on Sonnet 5 / 5.5, Opus 5 / 5.5, Haiku 4.5, not on Sonnet 4.6, so this
  pairs with item 10). Enum, ID-prefix, required-field, and `line_end >= line_start`
  violations become impossible at the source.
- OpenAI: switch `response_format` from `json_object` to `json_schema` with `strict: true`.
- Gemini: set `responseSchema` alongside the existing `responseMimeType`.
- Keep `schema.Validate` for the semantic checks a schema cannot express (line ranges within
  file bounds, context path exists), but make the repair call only for those, and send a
  *delta* repair (the offending issue objects only, not the whole output).

**Effort:** Medium (`internal/llm/*.go`, `internal/prompt/prompt.go`). Generate the JSON
schema from `schema/review.v1.json` minus the server-filled fields (item 4).

### 4. Stop asking the model for fields the runner overwrites; make patches and checklists opt-in  — Tokens, Speed, Accuracy

**Status: done.** Server-filled fields left the model-facing schema in item 3. Patches
and checklists are now governed by `schema.OutputShape`: the JSON Schema sent to the
provider, the prompt's schema text, the tail instructions, and both repair prompts all
omit them unless asked. Patches are requested only when `--patch-out` is given;
checklists only with the new `--checklists` flag (`PLANCRITIC_CHECKLISTS`). The Markdown
renderer now shows checklists when present, and the SDK facade exposes both switches.

**Evidence:** `schemaDefinition` in the prompt asks the model to emit `tool`, `version`,
`input.plan_hash` ("sha256:…", which it cannot know and therefore invents), `input.context_files`
hashes, `summary.score`, severity counts, and `meta`. `reviewer.Run` discards all of them
(`ComputeSummary`, `rev.Input = …`, `rev.Meta = …`). It also always asks for `patches`
(unified diffs are the most expensive and least reliable thing an LLM emits) and for a
filled `checklists` block (15 PASS/FAIL rows for `general`, more for other profiles), even
when `--patch-out` is not set and nothing reads the checklist.

**Change:** Split the prompt schema into a *model-facing* schema containing only `verdict`,
`issues`, `questions`, and optionally `patches`/`checklists`. Request `patches` only when
`--patch-out` is given; request `checklists` only behind a new `--checklists` flag. Output
tokens dominate latency, so this is also the cheapest speed win available.

**Effort:** Small (`internal/prompt/prompt.go`, `internal/reviewer/reviewer.go`).

### 5. Fix the max-tokens default and salvage truncated responses  — Accuracy, Tokens

**Status: done.** The CLI, SDK facade, and web UI default `--max-tokens` to 16384
(the provider default). Providers now return `llm.TruncatedError` carrying the partial
text; the reviewer salvages the longest prefix ending at a complete issue or question
(`llm.SalvageJSON`), runs the normal validate/repair path on it, appends a WARN
"Model output truncated" issue, sets `meta.truncated: true`, and never caches the
result. Only when nothing complete can be recovered does the run fail (exit 4, with a
message pointing at `--max-tokens`). Telling the model about the severity threshold
is deferred to item 9 because it would put the threshold into the result-cache key.

**Evidence:** `cmd/plancritic/check.go` defaults `--max-tokens` to 4096 while the providers
default to 16384 when unset. A 500-line plan with 30 issues plus questions, checklists, and
patches comfortably exceeds 4096 output tokens. When that happens every provider returns a
hard error (`response truncated (hit max_tokens=…)`), the run exits 4, and the full input
spend is wasted. The agent sees a provider error, not a plan problem, and typically retries
with the same flags.

**Change:** Raise the CLI default to 16384 (or 0 = provider default). On a `max_tokens` stop,
attempt to close the JSON (truncate to the last complete issue object) and return it with a
`WARN` truncation issue rather than failing; only fail if nothing parsable survives. Pass the
severity threshold into the prompt (item 9) so the model does not spend output tokens on
INFO items that `--severity-threshold warn` will drop.

**Effort:** Small.

### 6. Retry transient provider errors; fix the hidden 5-minute client timeout  — Speed/Throughput, Accuracy

**Status: done.** `llm.sendWithRetry` wraps every provider request: 408/429/5xx/529 and
transport errors are retried up to three times with exponential backoff plus jitter,
honoring `Retry-After` (seconds or HTTP date), capped at 30s; other 4xx are returned
immediately so the feature-fallback logic from item 3 still runs; a cancelled context
is never retried. Exhaustion produces an error naming the status and attempt count.
Retries are reported through `Settings.OnRetry`, which the reviewer routes to
`--verbose`. The providers' fixed 5-minute `http.Client` timeout is gone, so
`--timeout` is the only bound.

**Evidence:** No provider retries on 429, 500-503, 529 (Anthropic overloaded), or connection
reset. In an agent loop a single transient error aborts the run with exit 4 and the agent
either gives up on the gate or re-runs from scratch (paying full tokens again). Separately,
each provider constructs `http.Client{Timeout: 5 * time.Minute}`, so `--timeout 10m` is
silently capped at five minutes.

**Change:** Add bounded exponential backoff (3 attempts, honor `Retry-After`) in a shared
helper used by all three providers; distinguish retryable from non-retryable status codes.
Build the HTTP client with no `Timeout` and rely solely on the per-request context from
`--timeout`.

**Effort:** Small (`internal/llm/anthropic.go`, `openai.go`, `gemini.go`).

### 7. Add an agent-oriented compact output format  — Tokens (agent side), Speed

**Status: done.** `--format compact` (`render.Compact`) emits a `VERDICT` header plus one
line per issue, question, patch, and checklist with `file:line` references, title,
recommendation, blocking marker, and tags; descriptions, impact, quotes, and markup are
omitted. `--quiet` prints only the header line to stdout, `--no-quotes` drops evidence
quotes from json/md. The SDK facade's `RenderReview` accepts `compact`.

**Evidence:** Agents read the result back into their own context. The default output is
2-space-indented JSON that includes the reconstructed `quote` for every evidence entry, the
`input` hash block, `meta`, and `impact`/`recommendation` prose. For the 35-line sample plan
the JSON is 5.9k chars versus 3.0k for Markdown; the agent already has the plan in context,
so quotes are redundant to it. Agents also tend to `cat` the whole file rather than `jq` it.

**Change:** Add `--format compact` (plain text, deterministic, one line per finding):

```
VERDICT EXECUTABLE_WITH_CLARIFICATIONS score=72 critical=2 warn=5 info=7
ISSUE-0001 CRITICAL CONTRADICTION plan:L41-44 "Title" -> recommendation
Q-0001 WARN plan:L88 "Question text"
```

Also add `--no-quotes` for JSON output and `--quiet` (print only the verdict line to stdout
when `--out` is set). Expect a 3-5x reduction in agent-side read tokens per run.

**Effort:** Small (`internal/render`, `cmd/plancritic/check.go`).

### 8. Stable issue fingerprints and a `--baseline` delta  — Accuracy (loop), Tokens (agent side)

**Status: done.** Every issue and question carries `fingerprint` (hash of category +
source + cited text, normalized; line numbers excluded). `--baseline prev.json` adds
`delta` (resolved / new / persisting entries and score change) to the JSON, a "Changes
Since" section to Markdown, and `[new]`/`[persisting]` tags plus `RESOLVED` lines to
compact output. Older baselines without fingerprints are matched through their quotes.
Feeding persisting findings back into the prompt was not done; the cache key would
have to absorb the baseline and the gain is unproven.

**Evidence:** Issue IDs are assigned fresh by the model each run (`ISSUE-0001…`). After a
revision, the agent cannot tell which findings were resolved, which persist, and which are
new, so it re-reads everything and re-decides. SKILL.md compensates with a heuristic ("score
should rise >10 points"), which is noisy at temperature 0.2.

**Change:** Compute a content fingerprint per issue (`category` + source + normalized text
of the cited lines + title stem) and emit it as `issues[].fingerprint`. Add
`--baseline prev.json`: output `resolved`, `persisting`, and `new` sets (and mark them in
compact format). Optionally feed the baseline's persisting issues back into the prompt so the
model stays consistent across runs instead of re-discovering them with different wording.

**Effort:** Medium (`internal/review`, `internal/reviewer`, renderers).

### 9. Tell the model the output constraints that the runner enforces  — Tokens, Accuracy

**Evidence:** `--severity-threshold`, `--max-issues`, and `--max-questions` are applied
*after* generation (`review.FilterBySeverity`, `review.Truncate`). The model produces
findings that are then thrown away, and the truncation is positional, so when the model
emits more than the cap, the lowest-line-number items survive rather than the most
important ones. Only the caps are currently mentioned in the prompt, not the threshold.

**Change:** Include the severity threshold in the prompt ("do not report findings below
WARN"). Ask the model to emit issues already sorted by severity so positional truncation
keeps the most severe ones. Add a local pre-truncation dedup: issues with the same category
whose evidence ranges overlap are merged (models frequently emit the same ambiguity twice
under two titles).

**Effort:** Small.

### 10. Update default models and add `--effort`; stop sending `temperature` unconditionally  — Accuracy, forward compatibility

**Evidence:** The Anthropic default is `claude-sonnet-4-6` and the request never sets a
`thinking` block, so on that model no reasoning happens before the critique. Current-generation
models (Sonnet 5.5, Opus 5.5) reason by default with depth controlled by
`output_config.effort`, and they return HTTP 400 for non-default `temperature`, which
`anthropic.go` always sends (`Temperature: &s.Temperature`, default 0.2). As soon as a user
passes `--model claude-sonnet-5-5` today, every run fails.

**Change:** Default to `claude-sonnet-5-5` for the inner loop with `claude-opus-5-5`
documented for the final gate (or make Opus the default and expose a `--fast` preset that
selects Sonnet). Add `--effort low|medium|high` mapped to `output_config.effort`
(Anthropic) and reasoning-effort equivalents on OpenAI; agents can use `low` while iterating
and `high` for the gate. Only send `temperature` when the user set `--temperature`
explicitly or the model is known to accept it. Keep the raw-HTTP client (no new dependency),
but move the static prefix into the `system` field so the user turn is only context + plan.

**Effort:** Small-medium (`internal/llm/anthropic.go`, `openai.go`, `check.go`).

### 11. Prompt-cache hygiene: 1-hour TTL, verify minimums, surface usage in the output  — Tokens, Speed

**Evidence:** Cache breakpoints use the default 5-minute TTL. In an agent loop the gap
between runs (agent reads output, edits the plan, re-runs) is routinely longer than five
minutes, so the context segment (4.3k tokens on the baseline, much larger when a repo tree
is passed) is re-written on most runs. The general-profile prefix is ~1.1k estimated tokens,
right at the 1024-token minimum for Sonnet 4.x; on Haiku 4.5 (4096 minimum) it never
caches, silently. Cache statistics are only visible with `--verbose`, so neither the user nor
the agent can tell whether caching works. The `Anthropic-Beta: prompt-caching-2024-07-31`
header is obsolete (caching is GA) and can be removed.

**Change:** Add `--cache-ttl 1h` support for Anthropic (`cache_control: {type: "ephemeral",
ttl: "1h"}`; the flag already exists for Gemini). Add `meta.usage` to the JSON output
(`input_tokens`, `output_tokens`, `cache_read_input_tokens`, `cache_creation_input_tokens`,
`cached_result: bool`) so cost is visible per run and regressions are measurable. Consider
one combined breakpoint (prefix + context) when the prefix alone is below the model's minimum.

**Effort:** Small.

### 12. First-class `--spec` role for context files, with a coverage matrix  — Accuracy, Tokens (agent side)

**Evidence:** SKILL.md instructs the agent to cross-reference the plan against SPEC.md
*itself* after plancritic runs (scope creep, coverage gaps, ordering versus spec). That is
expensive agent work that the LLM call could do in the same pass, since both documents are
already in the prompt. Separately, the user's `/plan` command invokes
`plancritic check --plan ./specs/PLAN.md --spec ./specs/SPEC.md`; neither flag exists, so the
first invocation fails, the agent reads `--help`, and retries. (The README also documents an
`--offline` flag that `check` does not define.)

**Change:** Accept `--spec <path>` as a context file with the role "spec". When present,
add a prompt section asking for a requirement-to-step coverage table and emit it as an
optional `coverage` block (`covered`, `uncovered`, `out_of_scope` with evidence). Accept
`--plan <path>` as an alias for the positional argument. Fix the README flag table.

**Effort:** Medium (prompt, schema v1.1 optional field, renderers). The schema already
allows additional optional fields without breaking v1.

### 13. Zero-token local lint mode  — Speed, Tokens

**Evidence:** Several profile heuristics are literal string matches (`ambiguity_triggers`
such as "fast", "secure", "etc.") that do not need an LLM, and structural checks (steps
with no acceptance-criteria section, empty headings, duplicate step titles, forward
references to undefined phases) are deterministic. Agents iterating on wording pay a full
LLM round trip to learn that "handle edge cases" is still in the text.

**Change:** `plancritic lint <plan>` (or `check --local`) that runs only deterministic
checks and returns the same JSON shape (INFO severity, tagged `local`). Run it first inside
`check` so the model's prompt can say "the following trigger phrases were already flagged;
do not repeat them", trimming output tokens.

**Effort:** Small-medium (`internal/plan`, `internal/profile`, new command).

### 14. Tighten the strict-mode grounding check to avoid false downgrades  — Accuracy

**Evidence:** `review.CheckGrounding` downgrades any issue whose text contains phrases such
as "the project's" or "the existing code", regardless of whether that phrase also appears in
the plan or context. A plan that itself says "the existing code uses Cobra" causes a correct
CRITICAL finding that quotes it to be demoted to WARN and tagged UNVERIFIED.

**Change:** Only treat a phrase as a violation when it does not occur in the plan or any
context file. Record the matched phrase in `tags` (e.g. `UNVERIFIED:the existing code`) so
the agent can see why the downgrade happened.

**Effort:** Small (`internal/review/grounding.go`).

### 15. Cheaper line-number prefix  — Tokens (minor)

**Evidence:** Every plan and context line is prefixed `L001: ` (6 characters, ~3 tokens). On
the baseline's ~1,060 total lines this is ~3k tokens.

**Change:** Use `12|` style prefixes (no padding, no "L", no space). Saves roughly one token
per line. Low priority; only do it after items 1-4 since it changes the citation convention
the model is taught.

---

## Considered and not recommended

- **Streaming responses.** The CLI needs the complete JSON before it can validate; streaming
  does not reduce time-to-result. Only worth it if `--max-tokens` is raised far above 16k.
- **Multi-provider ensemble by default** (as prism does with `--compare`). Doubles or triples
  tokens per run; useful as an opt-in final-gate mode but not for the inner loop.
- **Splitting the review into parallel per-checklist calls.** Each call would need the full
  plan, multiplying input tokens; the latency win is small once output tokens are trimmed
  (items 3-4).

## Suggested order of work

Items 1, 4, 5, 6, 9, 11 are each a short change and together remove roughly a third of input
tokens, most of the wasted output tokens, and the two most common hard failures. Item 2
(result cache) and item 7 (compact format) are the largest wins for agent loops specifically.
Items 3 and 10 should be done together since structured outputs require a current-generation
model. Items 8, 12, 13 are features; 14 and 15 are polish.
