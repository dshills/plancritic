package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	"github.com/dshills/plancritic/internal/patch"
	"github.com/dshills/plancritic/internal/plan"
	"github.com/dshills/plancritic/internal/profile"
	"github.com/dshills/plancritic/internal/prompt"
	"github.com/dshills/plancritic/internal/redact"
	"github.com/dshills/plancritic/internal/render"
	"github.com/dshills/plancritic/internal/review"
	"github.com/dshills/plancritic/internal/schema"
	"github.com/spf13/cobra"
)

type checkFlags struct {
	format            string
	out               string
	contextPaths      []string
	profileName       string
	strict            bool
	providerName      string
	model             string
	maxTokens         int
	maxIssues         int
	maxQuestions      int
	maxInputTokens    int
	timeout           string
	temperature       float64
	seed              int
	hasSeed           bool
	severityThreshold string
	patchOut          string
	failOn            string
	redactEnabled     bool
	noCache           bool
	cacheTTL          string
	verbose           bool
	debug             bool
	provider          llm.Provider // if non-nil, used instead of ResolveProvider (for testing)
}

func newCheckCmd() *cobra.Command {
	f := &checkFlags{}

	cmd := &cobra.Command{
		Use:   "check <plan-file>",
		Short: "Analyze a plan and produce a review",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Check if seed was explicitly set
			f.hasSeed = cmd.Flags().Changed("seed")
			return runCheck(args[0], f)
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&f.format, "format", envStr("PLANCRITIC_FORMAT", "json"), "Output format: json or md")
	flags.StringVar(&f.out, "out", "", "Output file path (default: stdout)")
	flags.StringSliceVar(&f.contextPaths, "context", nil, "Context file paths (may be repeated)")
	flags.StringVar(&f.profileName, "profile", envStr("PLANCRITIC_PROFILE", "general"), "Profile name")
	flags.BoolVar(&f.strict, "strict", envBool("PLANCRITIC_STRICT", false), "Enable strict grounding mode")
	flags.StringVar(&f.providerName, "provider", envStr("PLANCRITIC_PROVIDER", ""), "LLM provider: anthropic, openai, or gemini")
	flags.StringVar(&f.model, "model", envStr("PLANCRITIC_MODEL", ""), "Model ID (e.g., claude-sonnet-4-6, gpt-5.2)")
	flags.IntVar(&f.maxTokens, "max-tokens", envInt("PLANCRITIC_MAX_TOKENS", 4096), "Max response tokens")
	flags.IntVar(&f.maxIssues, "max-issues", envInt("PLANCRITIC_MAX_ISSUES", 50), "Max issues to return")
	flags.IntVar(&f.maxQuestions, "max-questions", envInt("PLANCRITIC_MAX_QUESTIONS", 20), "Max questions to return")
	flags.IntVar(&f.maxInputTokens, "max-input-tokens", envInt("PLANCRITIC_MAX_INPUT_TOKENS", 0), "Max estimated input tokens (0=unlimited)")
	flags.StringVar(&f.timeout, "timeout", envStr("PLANCRITIC_TIMEOUT", "5m"), "HTTP timeout for LLM requests (e.g., 5m, 10m)")
	flags.Float64Var(&f.temperature, "temperature", envFloat("PLANCRITIC_TEMPERATURE", 0.2), "Model temperature")
	flags.IntVar(&f.seed, "seed", 0, "Random seed (if supported)")
	flags.StringVar(&f.severityThreshold, "severity-threshold", envStr("PLANCRITIC_SEVERITY_THRESHOLD", "info"), "Minimum severity: info, warn, or critical")
	flags.StringVar(&f.patchOut, "patch-out", "", "Write suggested patches as unified diff")
	flags.StringVar(&f.failOn, "fail-on", envStr("PLANCRITIC_FAIL_ON", ""), "Exit non-zero if verdict meets this level")
	flags.BoolVar(&f.redactEnabled, "redact", envBool("PLANCRITIC_REDACT", true), "Redact secrets before sending to model")
	flags.BoolVar(&f.noCache, "no-cache", envBool("PLANCRITIC_NO_CACHE", false), "Disable prompt caching (Anthropic cache_control markers / Gemini context cache)")
	flags.StringVar(&f.cacheTTL, "cache-ttl", envStr("PLANCRITIC_CACHE_TTL", "1h"), "TTL for provider-side context caches (Gemini only)")
	flags.BoolVar(&f.verbose, "verbose", false, "Print processing steps to stderr")
	flags.BoolVar(&f.debug, "debug", false, "Save prompt to debug file")

	return cmd
}

func runCheck(planPath string, f *checkFlags) error {
	logger := log.New(os.Stderr, "", 0)
	verbose := func(msg string, args ...any) {
		if f.verbose {
			logger.Printf(msg, args...)
		}
	}

	// 1. Load plan
	verbose("Loading plan: %s", planPath)
	p, err := plan.Load(planPath)
	if err != nil {
		return exitError(3, "failed to load plan: %v", err)
	}

	stepIDs := plan.InferStepIDs(p)
	verbose("Inferred %d plan steps", len(stepIDs))

	// 2. Load context files
	var contexts []*pctx.File
	for _, cp := range f.contextPaths {
		verbose("Loading context: %s", cp)
		cf, err := pctx.Load(cp)
		if err != nil {
			return exitError(3, "failed to load context %s: %v", cp, err)
		}
		contexts = append(contexts, cf)
	}

	// 3. Redact
	if f.redactEnabled {
		verbose("Redacting secrets")
		p.Raw = redact.Redact(p.Raw)
		p.Lines = strings.Split(p.Raw, "\n")
		for _, cf := range contexts {
			cf.Raw = redact.Redact(cf.Raw)
			cf.Lines = strings.Split(cf.Raw, "\n")
		}
	}

	// 4. Load profile
	verbose("Loading profile: %s", f.profileName)
	prof, err := profile.LoadBuiltin(f.profileName)
	if err != nil {
		return exitError(3, "failed to load profile: %v", err)
	}

	// 5. Validate format early
	if f.format != "json" && f.format != "md" {
		return exitError(3, "unknown format: %s", f.format)
	}

	// 6. Resolve LLM provider
	verbose("Resolving LLM provider")
	provider := f.provider
	if provider == nil {
		var err error
		provider, err = llm.ResolveProvider(f.providerName, f.model)
		if err != nil {
			return exitError(4, "model provider error: %v", err)
		}
	}
	verbose("Using provider: %s", provider.Name())

	// 6b. Parse timeout
	timeoutStr := f.timeout
	if timeoutStr == "" {
		timeoutStr = "5m"
	}
	timeout, err := time.ParseDuration(timeoutStr)
	if err != nil {
		return exitError(3, "invalid --timeout value %q: %v", f.timeout, err)
	}

	// 7. Build prompt
	maxIssues := f.maxIssues
	if maxIssues <= 0 {
		maxIssues = review.DefaultMaxIssues
	}
	maxQuestions := f.maxQuestions
	if maxQuestions <= 0 {
		maxQuestions = review.DefaultMaxQuestions
	}
	promptOpts := prompt.BuildOpts{
		Plan:         p,
		Contexts:     contexts,
		Profile:      prof,
		Strict:       f.strict,
		StepIDs:      stepIDs,
		MaxIssues:    maxIssues,
		MaxQuestions: maxQuestions,
	}
	promptSegments := prompt.BuildSegments(promptOpts)
	if f.noCache {
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
	if f.maxInputTokens > 0 && estimatedTokens > f.maxInputTokens {
		return exitError(3, "estimated prompt size ~%d tokens exceeds --max-input-tokens=%d (plan: %d lines, context files: %d). Reduce context, lower --max-issues/--max-questions, or raise the limit",
			estimatedTokens, f.maxInputTokens, len(p.Lines), len(contexts))
	}

	// 8. Debug output
	if f.debug {
		debugPath := "plancritic-debug-prompt.txt"
		verbose("Writing debug prompt to %s", debugPath)
		if err := os.WriteFile(debugPath, []byte(promptText), 0600); err != nil {
			verbose("Warning: failed to write debug prompt: %v", err)
		}
	}

	// 9. Call LLM
	verbose("Calling LLM (timeout: %s)...", timeout)
	settings := llm.Settings{
		Model:       f.model,
		Temperature: f.temperature,
		MaxTokens:   f.maxTokens,
	}
	if f.hasSeed {
		settings.Seed = &f.seed
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if !f.noCache {
		if name, err := ensureGeminiCache(ctx, provider, promptSegments, f.model, f.cacheTTL, verbose); err != nil {
			verbose("Cache orchestration error (falling back to uncached): %v", err)
		} else if name != "" {
			settings.CachedContentName = name
		}
	}

	var result string
	var usage llm.Usage
	if sp, ok := provider.(llm.SegmentedProvider); ok {
		result, usage, err = sp.GenerateSegments(ctx, promptSegments, settings)
	} else {
		result, usage, err = provider.Generate(ctx, promptText, settings)
	}
	if err != nil {
		return exitError(4, "LLM call failed: %v", err)
	}
	verbose("Received LLM response (%d bytes)", len(result))
	if usage.CacheReadInputTokens > 0 || usage.CacheCreationInputTokens > 0 {
		verbose("Token usage: input=%d (cache read=%d, cache write=%d), output=%d",
			usage.InputTokens, usage.CacheReadInputTokens, usage.CacheCreationInputTokens, usage.OutputTokens)
	} else if usage.InputTokens > 0 {
		verbose("Token usage: input=%d, output=%d", usage.InputTokens, usage.OutputTokens)
	}

	if f.debug {
		debugRespPath := "plancritic-debug-response.txt"
		verbose("Writing debug response to %s", debugRespPath)
		if err := os.WriteFile(debugRespPath, []byte(result), 0600); err != nil {
			verbose("Warning: failed to write debug response: %v", err)
		}
	}

	// 9. Parse JSON
	result = llm.ExtractJSON(result)
	var rev review.Review
	if err := json.Unmarshal([]byte(result), &rev); err != nil {
		// Try sanitizing invalid escape sequences (common with Gemini)
		sanitized := llm.SanitizeJSON(result)
		if err2 := json.Unmarshal([]byte(sanitized), &rev); err2 != nil {
			return exitError(5, "failed to parse LLM response as JSON: %v (pre-sanitize: %v)", err2, err)
		}
		verbose("Sanitized invalid JSON escape sequences")
		result = sanitized
	}

	// 10. Validate
	validationErrs := schema.Validate(&rev, len(p.Lines))
	if len(validationErrs) > 0 {
		verbose("Validation failed (%d errors), attempting repair...", len(validationErrs))

		repairPrompt := prompt.BuildRepair(result, validationErrs)
		repairResult, repairUsage, err := provider.Generate(ctx, repairPrompt, settings)
		if err != nil {
			return exitError(4, "repair LLM call failed: %v", err)
		}
		if repairUsage.InputTokens > 0 {
			verbose("Repair token usage: input=%d, output=%d", repairUsage.InputTokens, repairUsage.OutputTokens)
		}
		repairResult = llm.ExtractJSON(repairResult)

		var rev2 review.Review
		if err := json.Unmarshal([]byte(repairResult), &rev2); err != nil {
			sanitized := llm.SanitizeJSON(repairResult)
			if err2 := json.Unmarshal([]byte(sanitized), &rev2); err2 != nil {
				return exitError(5, "repair response is not valid JSON: %v (pre-sanitize: %v)", err2, err)
			}
		}

		validationErrs2 := schema.Validate(&rev2, len(p.Lines))
		if len(validationErrs2) > 0 {
			fmt.Fprintln(os.Stderr, "Schema validation errors after repair:")
			for _, e := range validationErrs2 {
				fmt.Fprintf(os.Stderr, "  %s\n", e)
			}
			return exitError(5, "LLM output failed schema validation after repair")
		}

		rev = rev2
	}
	verbose("Validation passed")

	// 11. Post-process
	review.SortIssues(rev.Issues)
	review.SortQuestions(rev.Questions)
	review.Truncate(&rev, maxIssues, maxQuestions)

	// Strict grounding post-check
	if f.strict {
		violations := review.CheckGrounding(&rev)
		if len(violations) > 0 {
			verbose("Grounding violations found: %d, applying downgrades", len(violations))
			review.ApplyGroundingDowngrades(&rev, violations)
			review.SortIssues(rev.Issues)
		}
	}

	// Apply severity threshold filter
	rev.Issues = filterBySeverity(rev.Issues, f.severityThreshold)
	rev.Questions = filterQuestionsBySeverity(rev.Questions, f.severityThreshold)
	// Compute deterministic summary from final issue list
	rev.Summary = review.ComputeSummary(rev.Issues)

	// Fill metadata
	rev.Tool = "plancritic"
	rev.Version = version
	rev.Input = review.Input{
		PlanFile: filepath.Base(planPath),
		PlanHash: p.Hash,
		Profile:  f.profileName,
		Strict:   f.strict,
	}
	for _, cf := range contexts {
		rev.Input.ContextFiles = append(rev.Input.ContextFiles, review.ContextFile{
			Path: filepath.Base(cf.FilePath),
			Hash: cf.Hash,
		})
	}
	modelName := f.model
	if modelName == "" {
		modelName = "(default)"
	}
	rev.Meta = review.Meta{
		Model:       provider.Name() + "/" + modelName,
		Temperature: f.temperature,
	}

	// 12. Output
	var output string
	switch f.format {
	case "json":
		data, err := json.MarshalIndent(rev, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal output: %w", err)
		}
		output = string(data) + "\n"
	case "md":
		output = render.Markdown(&rev)
	default:
		return exitError(3, "unknown format: %s", f.format)
	}

	if f.out != "" {
		verbose("Writing output to %s", f.out)
		if err := os.WriteFile(f.out, []byte(output), 0644); err != nil {
			return fmt.Errorf("failed to write output: %w", err)
		}
	} else {
		fmt.Print(output)
	}

	// 13. Patch output
	if f.patchOut != "" {
		verbose("Writing patches to %s", f.patchOut)
		if err := patch.WritePatchFile(rev.Patches, f.patchOut); err != nil {
			return fmt.Errorf("failed to write patches: %w", err)
		}
	}

	// 14. Exit code based on --fail-on
	if f.failOn != "" {
		meets, err := verdictMeetsThreshold(rev.Summary.Verdict, f.failOn)
		if err != nil {
			return exitError(3, "%v", err)
		}
		if meets {
			return exitError(2, "verdict %s meets fail threshold %s", rev.Summary.Verdict, f.failOn)
		}
	}

	return nil
}

type exitErr struct {
	code int
	msg  string
}

func (e *exitErr) Error() string { return e.msg }

func exitError(code int, format string, args ...any) error {
	return &exitErr{code: code, msg: fmt.Sprintf(format, args...)}
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

func filterBySeverity(issues []review.Issue, threshold string) []review.Issue {
	minOrder := severityThresholdOrder(threshold)
	var result []review.Issue
	for _, iss := range issues {
		if !iss.Severity.Valid() || severityOrder(iss.Severity) <= minOrder {
			result = append(result, iss)
		}
	}
	return result
}

func filterQuestionsBySeverity(questions []review.Question, threshold string) []review.Question {
	minOrder := severityThresholdOrder(threshold)
	var result []review.Question
	for _, q := range questions {
		if !q.Severity.Valid() || severityOrder(q.Severity) <= minOrder {
			result = append(result, q)
		}
	}
	return result
}

func severityOrder(s review.Severity) int {
	switch s {
	case review.SeverityCritical:
		return 0
	case review.SeverityWarn:
		return 1
	default:
		return 2
	}
}

func severityThresholdOrder(threshold string) int {
	switch strings.ToLower(threshold) {
	case "critical":
		return 0
	case "warn":
		return 1
	default:
		return 2 // info shows everything
	}
}

// envStr returns the value of the environment variable key, or fallback if unset/empty.
func envStr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// envBool returns the boolean value of the environment variable key, or fallback if unset/invalid.
func envBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

// envInt returns the integer value of the environment variable key, or fallback if unset/invalid.
func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

// envFloat returns the float64 value of the environment variable key, or fallback if unset/invalid.
func envFloat(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return f
}

// estimatedCharsPerToken is a rough heuristic for converting prompt
// character count to an approximate token count across LLM providers.
const estimatedCharsPerToken = 4

var validFailOnValues = map[string]int{
	"executable":     0,
	"clarifications": 1,
	"not_executable": 2,
	"not-executable": 2,
	"critical":       2,
}

func verdictMeetsThreshold(verdict review.Verdict, failOn string) (bool, error) {
	verdictLevel := map[review.Verdict]int{
		review.VerdictExecutable:         0,
		review.VerdictWithClarifications: 1,
		review.VerdictNotExecutable:      2,
	}

	vl, vlOk := verdictLevel[verdict]
	if !vlOk {
		return false, nil
	}
	tl, ok := validFailOnValues[strings.ToLower(failOn)]
	if !ok {
		return false, fmt.Errorf("unknown --fail-on value: %q (valid: executable, clarifications, not_executable, critical)", failOn)
	}
	return vl >= tl, nil
}
