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

	// 2. Load context files
	var contexts []*pctx.File
	for _, cp := range f.ContextPaths {
		verbose("Loading context: %s", cp)
		cf, err := pctx.Load(cp)
		if err != nil {
			return review.Review{}, Errorf(3, "failed to load context %s: %v", cp, err)
		}
		contexts = append(contexts, cf)
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
	shape := schema.OutputShape{Patches: f.Patches, Checklists: f.Checklists}
	promptOpts := prompt.BuildOpts{
		Plan:         p,
		Contexts:     contexts,
		Profile:      prof,
		Strict:       f.Strict,
		StepIDs:      stepIDs,
		MaxIssues:    maxIssues,
		MaxQuestions: maxQuestions,
		Shape:        shape,
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
	finalize := func(rev review.Review, cached bool) review.Review {
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
		modelName := f.Model
		if modelName == "" {
			modelName = "(default)"
		}
		rev.Meta = review.Meta{
			Model:       modelProvider.Name() + "/" + modelName,
			Temperature: f.Temperature,
			Cached:      cached,
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
				llm.EffectiveModel(modelProvider, f.Model),
				strconv.FormatFloat(f.Temperature, 'g', -1, 64),
				seedKey,
				strconv.Itoa(f.MaxTokens),
				promptText,
			)
			if cached, ok := resultStore.Get(resultKey); ok {
				verbose("Result cache hit (%s), skipping LLM call", resultKey[:12])
				return finalize(cached, true), nil
			}
			verbose("Result cache miss (%s)", resultKey[:12])
		}
	}

	// 9. Call LLM
	verbose("Calling LLM (timeout: %s)...", timeout)
	settings := llm.Settings{
		Model:       f.Model,
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
		if name, err := ensureGeminiCache(ctx, modelProvider, promptSegments, f.Model, f.CacheTTL, verbose); err != nil {
			verbose("Cache orchestration error (falling back to uncached): %v", err)
		} else if name != "" {
			settings.CachedContentName = name
		}
	}

	var result string
	var usage llm.Usage
	if sp, ok := modelProvider.(llm.SegmentedProvider); ok {
		result, usage, err = sp.GenerateSegments(ctx, promptSegments, settings)
	} else {
		result, usage, err = modelProvider.Generate(ctx, promptText, settings)
	}
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
		repaired, err := repairReview(ctx, modelProvider, settings, rev, validationErrs, repairBounds{
			PlanName:          filepath.Base(p.FilePath),
			PlanLines:         len(p.Lines),
			ContextLineCounts: contextLineCounts,
			Sources:           prompt.RenderSources(p, contexts),
			Shape:             shape,
		}, verbose)
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

	// 11. Post-process
	if truncated {
		rev.Issues = append(rev.Issues, truncationNotice(truncatedAt, filepath.Base(p.FilePath), p.Lines))
	}
	review.SortIssues(rev.Issues)
	review.SortQuestions(rev.Questions)

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

	out := finalize(rev, false)
	out.Meta.Truncated = truncated
	return out, nil
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
func ensureGeminiCache(ctx context.Context, provider llm.Provider, segments []llm.Segment, modelFlag, ttlStr string, verbose func(string, ...any)) (string, error) {
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

	ttl, err := time.ParseDuration(ttlStr)
	if err != nil {
		return "", fmt.Errorf("invalid --cache-ttl %q: %w", ttlStr, err)
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
