package reviewer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dshills/plancritic/internal/cachestore"
	pctx "github.com/dshills/plancritic/internal/context"
	"github.com/dshills/plancritic/internal/lint"
	"github.com/dshills/plancritic/internal/llm"
	"github.com/dshills/plancritic/internal/plan"
	"github.com/dshills/plancritic/internal/profile"
	"github.com/dshills/plancritic/internal/prompt"
	"github.com/dshills/plancritic/internal/redact"
	"github.com/dshills/plancritic/internal/resultcache"
	"github.com/dshills/plancritic/internal/review"
	"github.com/dshills/plancritic/internal/schema"
)

type Options struct {
	Format            string
	Out               string
	ContextPaths      []string
	ProfileName       string
	Strict            bool
	ProviderName      string
	Model             string
	MaxTokens         int
	MaxIssues         int
	MaxQuestions      int
	MaxInputTokens    int
	Timeout           string
	Temperature       float64
	Seed              int
	HasSeed           bool
	SeverityThreshold string
	PatchOut          string
	FailOn            string
	RedactEnabled     bool
	NoCache           bool
	CacheTTL          string
	// NoResultCache disables only the local on-disk result cache;
	// provider-side prompt caching is unaffected. NoCache disables both.
	NoResultCache bool
	// Patches asks the model for unified-diff patch suggestions; the CLI
	// sets it only when --patch-out is given. Checklists asks for a
	// PASS/FAIL/N/A evaluation of every profile checklist item. Both are
	// off by default because they are the most expensive parts of a
	// response and rarely consumed by agents.
	Patches    bool
	Checklists bool
	// BaselinePath, when set, is an earlier run's JSON output; the
	// result then carries a Delta of resolved/new/persisting findings.
	BaselinePath string
	// SpecPath, when set, is the specification the plan implements. It
	// is loaded as a context file with the "spec" role and the result
	// carries a Coverage matrix.
	SpecPath string
	// NoLint skips the local, zero-token checks (see package lint) that
	// otherwise run before the model and are merged into the review.
	NoLint bool
	// Effort asks the model to reason less or more (see llm.Settings).
	Effort string
	// Fast selects the provider's cheaper, faster model tier when no
	// Model is given (see llm.FastModel).
	Fast bool
	// ResultCacheDir overrides where cached reviews are stored. Empty
	// selects resultcache.DefaultDir, which honors PLANCRITIC_CACHE_DIR.
	ResultCacheDir string
	Verbose        bool
	Debug          bool
	DebugDir       string
	Provider       llm.Provider
}

func Run(parentCtx context.Context, planPath string, f Options, version string) (review.Review, error) {
	verbose := verboseLogger(f.Verbose)

	// 1. Load plan
	verbose("Loading plan: %s", planPath)
	p, err := plan.Load(planPath)
	if err != nil {
		return review.Review{}, Errorf(3, "failed to load plan: %v", err)
	}

	stepIDs := plan.InferStepIDs(p)
	verbose("Inferred %d plan steps", len(stepIDs))

	// 2. Load context files; the spec (if any) comes first so the model
	// reads it before the other grounding.
	var contexts []*pctx.File
	if f.SpecPath != "" {
		verbose("Loading spec: %s", f.SpecPath)
		sf, err := pctx.Load(f.SpecPath)
		if err != nil {
			return review.Review{}, Errorf(3, "failed to load spec %s: %v", f.SpecPath, err)
		}
		sf.Role = pctx.RoleSpec
		contexts = append(contexts, sf)
	}
	for _, cp := range f.ContextPaths {
		verbose("Loading context: %s", cp)
		cf, err := pctx.Load(cp)
		if err != nil {
			return review.Review{}, Errorf(3, "failed to load context %s: %v", cp, err)
		}
		contexts = append(contexts, cf)
	}

	// 2b. Load the baseline before spending any tokens so a bad path
	// fails fast.
	var baseline *review.Review
	if f.BaselinePath != "" {
		verbose("Loading baseline: %s", f.BaselinePath)
		var err error
		baseline, err = review.LoadBaseline(f.BaselinePath)
		if err != nil {
			return review.Review{}, Errorf(3, "%v", err)
		}
	}

	// 3. Redact
	if f.RedactEnabled {
		verbose("Redacting secrets")
		p.Raw = redact.Redact(p.Raw)
		p.Lines = strings.Split(p.Raw, "\n")
		for _, cf := range contexts {
			cf.Raw = redact.Redact(cf.Raw)
			cf.Lines = strings.Split(cf.Raw, "\n")
		}
	}

	// 4. Load profile
	verbose("Loading profile: %s", f.ProfileName)
	prof, err := profile.LoadBuiltin(f.ProfileName)
	if err != nil {
		return review.Review{}, Errorf(3, "failed to load profile: %v", err)
	}

	// 5. Local lint: deterministic findings recorded before the model
	// runs, and listed in the prompt so the model does not repeat them.
	var localIssues []review.Issue
	if !f.NoLint {
		localIssues = lint.Run(p, prof)
		verbose("Local lint: %d finding(s)", len(localIssues))
	}

	// 6. Resolve LLM provider
	verbose("Resolving LLM provider")
	modelProvider := f.Provider
	if modelProvider == nil {
		var err error
		modelProvider, err = llm.ResolveProvider(f.ProviderName, f.Model)
		if err != nil {
			return review.Review{}, Errorf(4, "model provider error: %v", err)
		}
	}
	verbose("Using provider: %s", modelProvider.Name())

	if !llm.ValidEffort(f.Effort) {
		return review.Review{}, Errorf(3, "invalid --effort %q (valid: %s)", f.Effort, strings.Join(llm.ValidEfforts, ", "))
	}
	// requestModel is what the provider is asked for: the explicit
	// --model, else the fast tier when --fast is set, else empty for the
	// provider's default.
	requestModel := f.Model
	if requestModel == "" && f.Fast {
		if requestModel = llm.FastModel(modelProvider.Name()); requestModel != "" {
			verbose("Fast tier: using %s", requestModel)
		}
	}

	// 6a. Parse the cache TTL once; a typo should fail fast, not
	// silently disable caching.
	cacheTTL, err := time.ParseDuration(f.CacheTTL)
	if f.CacheTTL == "" {
		cacheTTL, err = time.Hour, nil
	}
	if err != nil {
		return review.Review{}, Errorf(3, "invalid --cache-ttl value %q: %v", f.CacheTTL, err)
	}

	// 6b. Parse timeout
	requestTimeoutText := f.Timeout
	if requestTimeoutText == "" {
		requestTimeoutText = "5m"
	}
	timeout, err := time.ParseDuration(requestTimeoutText)
	if err != nil {
		return review.Review{}, Errorf(3, "invalid --timeout value %q: %v", f.Timeout, err)
	}

	// 7. Build prompt
	maxIssues := f.MaxIssues
	if maxIssues <= 0 {
		maxIssues = review.DefaultMaxIssues
	}
	maxQuestions := f.MaxQuestions
	if maxQuestions <= 0 {
		maxQuestions = review.DefaultMaxQuestions
	}
	shape := schema.OutputShape{Patches: f.Patches, Checklists: f.Checklists, Coverage: f.SpecPath != ""}
	promptOpts := prompt.BuildOpts{
		Plan:         p,
		Contexts:     contexts,
		Profile:      prof,
		Strict:       f.Strict,
		StepIDs:      stepIDs,
		MaxIssues:    maxIssues,
		MaxQuestions: maxQuestions,
		Shape:        shape,
		// The threshold is part of the prompt (and so of the result-cache
		// key): a review generated for "warn" never contained INFO
		// findings and must not be served for an "info" request.
		SeverityThreshold: f.SeverityThreshold,
		PreFlagged:        lint.Summaries(localIssues),
	}
	promptSegments := prompt.BuildSegments(promptOpts)
	if f.NoCache {
		// Strip cache markers so providers (Anthropic) won't apply
		// cache_control headers; Gemini orchestration below is also
		// skipped when noCache is set.
		for i := range promptSegments {
			promptSegments[i].CacheMark = false
		}
	}
	promptText := llm.ConcatSegments(promptSegments)

	// 7b. Prompt size check
	estimatedTokens := len(promptText) / estimatedCharsPerToken
	verbose("Prompt size: %d chars (~%d estimated tokens)", len(promptText), estimatedTokens)
	if estimatedTokens > 100000 {
		verbose("WARNING: prompt is very large (~%dk tokens), request may be slow or fail", estimatedTokens/1000)
	}
	if f.MaxInputTokens > 0 && estimatedTokens > f.MaxInputTokens {
		return review.Review{}, Errorf(3, "estimated prompt size ~%d tokens exceeds --max-input-tokens=%d (plan: %d lines, context files: %d). Reduce context, lower --max-issues/--max-questions, or raise the limit",
			estimatedTokens, f.MaxInputTokens, len(p.Lines), len(contexts))
	}

	// 8. Debug output
	if f.Debug {
		debugPath, err := writeDebugFile(f.DebugDir, "plancritic-debug-prompt-*.txt", []byte(promptText))
		if err != nil {
			verbose("Warning: failed to write debug prompt: %v", err)
		} else {
			verbose("Wrote debug prompt to %s", debugPath)
		}
	}

	// 8b. Output-only post-processing, shared by the fresh and cached
	// paths. Severity filtering and truncation are applied here rather
	// than before caching so a cached review can honor whatever output
	// flags the next invocation passes.
	finalize := func(rev review.Review, cached bool, usage *review.Usage) review.Review {
		rev.Issues = review.FilterBySeverity(rev.Issues, f.SeverityThreshold)
		rev.Questions = review.FilterQuestionsBySeverity(rev.Questions, f.SeverityThreshold)
		review.Truncate(&rev, maxIssues, maxQuestions)
		rev.Summary = review.ComputeSummary(rev.Issues)

		rev.Tool = "plancritic"
		rev.Version = version
		rev.Input = review.Input{
			PlanFile: filepath.Base(planPath),
			PlanHash: p.Hash,
			Profile:  f.ProfileName,
			Strict:   f.Strict,
		}
		rev.Input.ContextFiles = nil
		for _, cf := range contexts {
			rev.Input.ContextFiles = append(rev.Input.ContextFiles, review.ContextFile{
				Path: filepath.Base(cf.FilePath),
				Hash: cf.Hash,
			})
		}
		modelName := llm.EffectiveModel(modelProvider, requestModel)
		if modelName == "" {
			modelName = "(default)"
		}
		rev.Meta = review.Meta{
			Model:       modelProvider.Name() + "/" + modelName,
			Temperature: f.Temperature,
			Cached:      cached,
			Usage:       usage,
		}

		// Coverage counts are computed here, never taken from the model.
		// (A missing block when a spec was given is a validation error
		// earlier in the pipeline, so it is always present here.)
		if rev.Coverage != nil {
			if rev.Coverage.Requirements == nil {
				rev.Coverage.Requirements = []review.Requirement{}
			}
			if rev.Coverage.OutOfScope == nil {
				rev.Coverage.OutOfScope = []review.ScopeItem{}
			}
			review.ComputeCoverageSummary(rev.Coverage)
		}

		// Fingerprints need the reconstructed quotes, which every path
		// (fresh, cached, salvaged) has by now.
		review.AssignFingerprints(&rev)
		if baseline != nil {
			rev.Delta = review.ComputeDelta(baseline, &rev, f.BaselinePath)
		}
		return rev
	}

	// 8c. Local result cache. The key covers the exact prompt text, which
	// already encodes the redacted plan and contexts, profile, strict
	// mode, step index, and caps, plus every other setting that shapes
	// the provider's answer. Output-only flags are deliberately excluded.
	var resultStore *resultcache.Store
	var resultKey string
	if !f.NoCache && !f.NoResultCache {
		dir := f.ResultCacheDir
		if dir == "" {
			var dirErr error
			dir, dirErr = resultcache.DefaultDir()
			if dirErr != nil {
				verbose("Result cache unavailable: %v", dirErr)
			}
		}
		if dir != "" {
			seedKey := ""
			if f.HasSeed {
				seedKey = strconv.Itoa(f.Seed)
			}
			resultStore = resultcache.Open(dir, resultcache.DefaultTTL)
			// Key on the model the provider will actually use, not the
			// (possibly empty) requested string, so a provider default
			// change can never serve a review produced by another model.
			resultKey = resultcache.Key(
				"v1",
				version,
				modelProvider.Name(),
				llm.EffectiveModel(modelProvider, requestModel),
				f.Effort,
				strconv.FormatFloat(f.Temperature, 'g', -1, 64),
				seedKey,
				strconv.Itoa(f.MaxTokens),
				promptText,
			)
			if cached, ok := resultStore.Get(resultKey); ok {
				verbose("Result cache hit (%s), skipping LLM call", resultKey[:12])
				return finalize(cached, true, nil), nil
			}
			verbose("Result cache miss (%s)", resultKey[:12])
		}
	}

	// 9. Call LLM
	verbose("Calling LLM (timeout: %s)...", timeout)
	settings := llm.Settings{
		Model:       requestModel,
		Effort:      f.Effort,
		CacheTTL:    cacheTTL,
		Temperature: f.Temperature,
		MaxTokens:   f.MaxTokens,
		// Providers with native structured output enforce the shape at
		// the source; the prompt still carries the same schema as text
		// for providers (and models) without it.
		OutputSchema: schema.ModelOutputSchemaJSON(shape),
		OnRetry: func(attempt int, reason string, delay time.Duration) {
			verbose("Provider request %d failed (%s); retrying in %s", attempt, reason, delay.Round(time.Millisecond))
		},
	}
	if f.HasSeed {
		settings.Seed = &f.Seed
	}

	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	if !f.NoCache {
		if name, err := ensureGeminiCache(ctx, modelProvider, promptSegments, requestModel, cacheTTL, verbose); err != nil {
			verbose("Cache orchestration error (falling back to uncached): %v", err)
		} else if name != "" {
			settings.CachedContentName = name
		}
	}

	var result string
	var usage llm.Usage
	calls := 0
	if sp, ok := modelProvider.(llm.SegmentedProvider); ok {
		result, usage, err = sp.GenerateSegments(ctx, promptSegments, settings)
	} else {
		result, usage, err = modelProvider.Generate(ctx, promptText, settings)
	}
	calls++
	truncated := false
	truncatedAt := 0
	if err != nil {
		var te *llm.TruncatedError
		if !errors.As(err, &te) {
			return review.Review{}, Errorf(4, "LLM call failed: %v", err)
		}
		// The model hit its output cap. The input was already billed and
		// every complete finding emitted so far is still good, so salvage
		// the parseable prefix rather than throwing the run away.
		partial := te.Partial
		if partial == "" {
			partial = result
		}
		salvaged, ok := llm.SalvageJSON(llm.ExtractJSON(partial))
		if !ok {
			return review.Review{}, Errorf(4, "LLM output truncated at max_tokens=%d and nothing complete could be salvaged; raise --max-tokens", te.MaxTokens)
		}
		verbose("LLM output truncated at max_tokens=%d; salvaged %d of %d bytes", te.MaxTokens, len(salvaged), len(partial))
		result = salvaged
		truncated = true
		truncatedAt = te.MaxTokens
	}
	verbose("Received LLM response (%d bytes)", len(result))
	if usage.CacheReadInputTokens > 0 || usage.CacheCreationInputTokens > 0 {
		verbose("Token usage: input=%d (cache read=%d, cache write=%d), output=%d",
			usage.InputTokens, usage.CacheReadInputTokens, usage.CacheCreationInputTokens, usage.OutputTokens)
	} else if usage.InputTokens > 0 {
		verbose("Token usage: input=%d, output=%d", usage.InputTokens, usage.OutputTokens)
	}

	if f.Debug {
		debugRespPath, err := writeDebugFile(f.DebugDir, "plancritic-debug-response-*.txt", []byte(result))
		if err != nil {
			verbose("Warning: failed to write debug response: %v", err)
		} else {
			verbose("Wrote debug response to %s", debugRespPath)
		}
	}

	// 9. Parse JSON
	result = llm.ExtractJSON(result)
	var rev review.Review
	if err := json.Unmarshal([]byte(result), &rev); err != nil {
		// Try sanitizing invalid escape sequences (common with Gemini).
		// Use a fresh Review so partial fields from the failed unmarshal
		// don't bleed into the retry result.
		sanitized := llm.SanitizeJSON(result)
		var rev2 review.Review
		if err2 := json.Unmarshal([]byte(sanitized), &rev2); err2 != nil {
			return review.Review{}, Errorf(5, "failed to parse LLM response as JSON: %v (pre-sanitize: %v)", err2, err)
		}
		rev = rev2
		verbose("Sanitized invalid JSON escape sequences")
	}

	// 10. Validate. Build context lookup maps in a single pass; both
	// maps are keyed by basename, matching the identifier the prompt
	// exposes to the LLM (see prompt.BuildSegments).
	// Use review.NormalizeContextPath so the map keys match exactly
	// what schema.Validate and review.ReconstructQuotes will compute
	// from Evidence.Path, regardless of the host OS or whether the
	// LLM emits back- or forward-slash paths.
	contextLineCounts := make(map[string]int, len(contexts))
	contextLinesByBase := make(map[string][]string, len(contexts))
	for _, c := range contexts {
		base := review.NormalizeContextPath(c.FilePath)
		if _, dup := contextLinesByBase[base]; dup {
			// Unconditional stderr: two context files with the same
			// basename make the LLM's citations ambiguous and will
			// silently resolve to whichever file we store last.
			fmt.Fprintf(os.Stderr, "plancritic: warning: multiple context files share basename %q — citations may be ambiguous\n", base)
		}
		contextLineCounts[base] = len(c.Lines)
		contextLinesByBase[base] = c.Lines
	}
	validationErrs := schema.Validate(&rev, len(p.Lines), contextLineCounts)
	if shape.Coverage && rev.Coverage == nil {
		// Validate cannot know a spec was supplied; a missing matrix must
		// go through repair rather than be mistaken for "no requirements".
		validationErrs = append(validationErrs, schema.ValidationError{Path: "coverage", Message: "required when a specification is provided"})
	}
	if len(validationErrs) > 0 {
		// 10a. Mechanical defects (empty or duplicate IDs, inverted or
		// overlong line ranges) are fixed locally; they need no model.
		if fixes := schema.AutoFix(&rev, len(p.Lines), contextLineCounts); len(fixes) > 0 {
			for _, fx := range fixes {
				verbose("Auto-fixed %s", fx)
			}
			validationErrs = schema.Validate(&rev, len(p.Lines), contextLineCounts)
		}
	}
	if len(validationErrs) > 0 {
		// 10b. Whatever remains needs the model. Only the offending items
		// are resent when every error is attributable to one.
		verbose("Validation failed (%d errors), attempting repair...", len(validationErrs))
		repaired, repairUsage, err := repairReview(ctx, modelProvider, settings, rev, validationErrs, repairBounds{
			PlanName:          filepath.Base(p.FilePath),
			PlanLines:         len(p.Lines),
			ContextLineCounts: contextLineCounts,
			Sources:           prompt.RenderSources(p, contexts),
			Shape:             shape,
		}, verbose)
		calls++
		usage = addUsage(usage, repairUsage)
		if err != nil {
			return review.Review{}, err
		}
		rev = repaired
	}
	verbose("Validation passed")

	// 10b. Reconstruct evidence quotes from cited line ranges. The LLM
	// is instructed to omit the quote field to save output tokens; any
	// quote it still emits is overwritten from the authoritative source.
	quoteSrc := review.QuoteSource{
		PlanLines:          p.Lines,
		ContextsByBasename: contextLinesByBase,
	}
	if misses := review.ReconstructQuotes(&rev, quoteSrc); misses > 0 {
		verbose("Quote reconstruction: %d evidence entries could not be resolved to a source", misses)
	}

	// 10c. Merge the local findings. They were created with quotes and
	// LINT ids, so they need none of the validation above, and they go
	// in before caching so a cached review carries them too. A local
	// candidate the model confirmed (same category, overlapping lines)
	// is dropped in favor of the model's finding and its severity.
	rev.Issues = append(rev.Issues, lint.Unsuperseded(localIssues, rev.Issues, p.Lines)...)

	// 11. Post-process
	if truncated {
		rev.Issues = append(rev.Issues, truncationNotice(truncatedAt, filepath.Base(p.FilePath), p.Lines))
	}
	review.SortIssues(rev.Issues)
	review.SortQuestions(rev.Questions)

	// Merge duplicate findings (same category, same or overlapping
	// citation with a similar title). Runs after the sort so the more
	// severe copy is the one kept.
	if before := len(rev.Issues); before > 0 {
		rev.Issues = review.DedupIssues(rev.Issues)
		if merged := before - len(rev.Issues); merged > 0 {
			verbose("Merged %d duplicate issue(s)", merged)
		}
	}

	// Strict grounding post-check
	if f.Strict {
		violations := review.CheckGrounding(&rev)
		if len(violations) > 0 {
			verbose("Grounding violations found: %d, applying downgrades", len(violations))
			review.ApplyGroundingDowngrades(&rev, violations)
			review.SortIssues(rev.Issues)
		}
	}

	// Persist the validated, sorted, grounding-checked review before the
	// output-only filters run (see finalize). Write failures are logged,
	// never fatal: the cache is an optimization.
	// A truncated review is incomplete by definition; never serve it
	// from cache.
	if resultStore != nil && !truncated {
		if err := resultStore.Put(resultKey, rev); err != nil {
			verbose("Result cache write failed: %v", err)
		}
	}

	out := finalize(rev, false, &review.Usage{
		Calls:                    calls,
		InputTokens:              usage.InputTokens,
		OutputTokens:             usage.OutputTokens,
		CacheReadInputTokens:     usage.CacheReadInputTokens,
		CacheCreationInputTokens: usage.CacheCreationInputTokens,
	})
	out.Meta.Truncated = truncated
	return out, nil
}

func addUsage(a, b llm.Usage) llm.Usage {
	return llm.Usage{
		InputTokens:              a.InputTokens + b.InputTokens,
		OutputTokens:             a.OutputTokens + b.OutputTokens,
		CacheCreationInputTokens: a.CacheCreationInputTokens + b.CacheCreationInputTokens,
		CacheReadInputTokens:     a.CacheReadInputTokens + b.CacheReadInputTokens,
	}
}

// truncationNotice is appended to the issues when the model hit its
// output cap, so the incompleteness is visible in the findings
// themselves and not only in meta.truncated. The output schema requires
// at least one evidence entry, so the notice cites a real location (the
// plan's first line, quoted verbatim) and is tagged "system" so readers
// can tell it is about the run, not about the plan text it cites.
func truncationNotice(maxTokens int, planName string, planLines []string) review.Issue {
	firstLine := ""
	if len(planLines) > 0 {
		firstLine = planLines[0]
	}
	return review.Issue{
		ID:             "ISSUE-TRUNC-OUTPUT",
		Severity:       review.SeverityWarn,
		Category:       review.CategoryAmbiguity,
		Title:          "Model output truncated",
		Description:    fmt.Sprintf("The model hit its output cap (max_tokens=%d) before finishing. The findings listed are complete in themselves; any findings it had not yet emitted are missing. This is a notice about the run, not a defect at the cited line.", maxTokens),
		Impact:         "The review may understate the number and severity of issues.",
		Recommendation: "Re-run with a higher --max-tokens.",
		Evidence: []review.Evidence{
			{Source: "plan", Path: planName, LineStart: 1, LineEnd: 1, Quote: firstLine},
		},
		Tags: []string{"system", "truncated"},
	}
}

type Error struct {
	Code int
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func Errorf(code int, format string, args ...any) error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

func verboseLogger(enabled bool) func(string, ...any) {
	logger := log.New(os.Stderr, "", 0)
	return func(msg string, args ...any) {
		if enabled {
			logger.Printf(msg, args...)
		}
	}
}

func writeDebugFile(dir, pattern string, data []byte) (string, error) {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	// os.CreateTemp already creates the file with mode 0600; no Chmod needed.
	// defer is the panic/early-return safety net; the explicit Close below
	// captures write-flush errors. Calling Close twice on *os.File is safe.
	defer func() { _ = f.Close() }()
	if _, err = f.Write(data); err != nil {
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	return f.Name(), nil
}

// ensureGeminiCache returns a cache resource name for the cacheable
// portion of segments when the underlying provider supports context
// caching and the prefix meets the provider's minimum size. Returns
// ("", nil) when caching is not applicable (non-caching provider,
// prefix too small, store unavailable). Cache creation failures are
// returned as errors so the caller can log and proceed uncached.
func ensureGeminiCache(ctx context.Context, provider llm.Provider, segments []llm.Segment, modelFlag string, ttl time.Duration, verbose func(string, ...any)) (string, error) {
	base := llm.Unwrap(provider)
	cp, ok := base.(llm.CachingProvider)
	if !ok {
		return "", nil
	}

	var prefixLen int
	for _, seg := range segments {
		if seg.CacheMark {
			prefixLen += len(seg.Text)
		}
	}
	if prefixLen < llm.GeminiMinCacheChars {
		verbose("Cache prefix too small (%d chars, need ≥%d), skipping cache", prefixLen, llm.GeminiMinCacheChars)
		return "", nil
	}

	// Effective model: --model flag wins, then any wrapped-provider
	// override, then the Gemini default. Normalizing the default here
	// keeps the cache key stable across invocations where the user
	// sometimes passes --model=<default> and sometimes omits it.
	model := modelFlag
	if override := llm.OverrideModel(provider); override != "" {
		model = override
	}
	if model == "" {
		model = llm.GeminiDefaultModel
	}

	// Hash key = model + concatenated cacheable segment bytes.
	h := sha256.New()
	h.Write([]byte(model))
	h.Write([]byte{0})
	for _, seg := range segments {
		if seg.CacheMark {
			h.Write([]byte(seg.Text))
		}
	}
	key := hex.EncodeToString(h.Sum(nil))

	storePath, err := cachestore.DefaultPath()
	if err != nil {
		return "", fmt.Errorf("cache store path: %w", err)
	}
	store, openErr := cachestore.Open(storePath)
	if store == nil {
		return "", fmt.Errorf("open cache store: %w", openErr)
	}
	if openErr != nil {
		// Corrupt file — Open recovered by returning an empty store.
		verbose("Cache store was corrupted, starting fresh: %v", openErr)
	}

	if entry, ok := store.Get(key); ok {
		verbose("Reusing Gemini cache: %s (expires %s)", entry.Name, entry.ExpiresAt.Format(time.RFC3339))
		return entry.Name, nil
	}

	verbose("Creating Gemini context cache (ttl=%s)...", ttl)
	handle, err := cp.CreateCache(ctx, segments, model, ttl)
	if err != nil {
		return "", fmt.Errorf("create cache: %w", err)
	}

	store.Put(key, cachestore.Entry{Name: handle.Name, Model: model, ExpiresAt: handle.ExpiresAt})
	if err := store.Save(); err != nil {
		verbose("Cache store save failed (cache created but not persisted): %v", err)
	}
	verbose("Created Gemini cache: %s (expires %s)", handle.Name, handle.ExpiresAt.Format(time.RFC3339))
	return handle.Name, nil
}

// estimatedCharsPerToken is a rough heuristic for converting prompt
// character count to an approximate token count across LLM providers.
const estimatedCharsPerToken = 4
