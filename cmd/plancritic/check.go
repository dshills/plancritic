package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/dshills/plancritic/internal/llm"
	"github.com/dshills/plancritic/internal/patch"
	"github.com/dshills/plancritic/internal/render"
	"github.com/dshills/plancritic/internal/review"
	"github.com/dshills/plancritic/internal/reviewer"
	"github.com/spf13/cobra"
)

type checkFlags struct {
	format            string
	out               string
	noQuotes          bool
	quiet             bool
	contextPaths      []string
	specPath          string
	planFlag          string
	profileName       string
	strict            bool
	providerName      string
	model             string
	effort            string
	fast              bool
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
	checklists        bool
	baseline          string
	noLint            bool
	failOn            string
	redactEnabled     bool
	noCache           bool
	noResultCache     bool
	cacheTTL          string
	verbose           bool
	debug             bool
	provider          llm.Provider // if non-nil, used instead of ResolveProvider (for testing)
}

func newCheckCmd() *cobra.Command {
	f := &checkFlags{}

	cmd := &cobra.Command{
		Use:   "check [plan-file]",
		Short: "Analyze a plan and produce a review",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Check if seed was explicitly set
			f.hasSeed = cmd.Flags().Changed("seed")
			planPath, err := resolvePlanPath(args, f.planFlag)
			if err != nil {
				return err
			}
			return runCheck(cmd.Context(), planPath, f)
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&f.format, "format", envStr("PLANCRITIC_FORMAT", "json"), "Output format: json, md, or compact (one line per finding, for agents)")
	flags.StringVar(&f.out, "out", "", "Output file path (default: stdout)")
	flags.BoolVar(&f.noQuotes, "no-quotes", envBool("PLANCRITIC_NO_QUOTES", false), "Omit evidence quotes from json/md output (line references are kept)")
	flags.BoolVar(&f.quiet, "quiet", envBool("PLANCRITIC_QUIET", false), "Print only the one-line verdict summary to stdout (pair with --out)")
	flags.StringVar(&f.planFlag, "plan", "", "Plan file path (alternative to the positional argument)")
	flags.StringSliceVar(&f.contextPaths, "context", nil, "Context file paths (may be repeated)")
	flags.StringVar(&f.specPath, "spec", envStr("PLANCRITIC_SPEC", ""), "Specification the plan implements; adds a requirement-by-requirement coverage matrix to the result")
	flags.StringVar(&f.profileName, "profile", envStr("PLANCRITIC_PROFILE", "general"), "Profile name")
	flags.BoolVar(&f.strict, "strict", envBool("PLANCRITIC_STRICT", false), "Enable strict grounding mode")
	flags.StringVar(&f.providerName, "provider", envStr("PLANCRITIC_PROVIDER", ""), "LLM provider: anthropic, openai, or gemini")
	flags.StringVar(&f.model, "model", envStr("PLANCRITIC_MODEL", ""), "Model ID (e.g., claude-opus-5-5, gpt-5.2, gemini-2.5-flash)")
	flags.StringVar(&f.effort, "effort", envStr("PLANCRITIC_EFFORT", ""), "Reasoning effort: low, medium, high, xhigh, or max (default: the model's own)")
	flags.BoolVar(&f.fast, "fast", envBool("PLANCRITIC_FAST", false), "Use the provider's cheaper, faster model tier (ignored when --model is set)")
	flags.IntVar(&f.maxTokens, "max-tokens", envInt("PLANCRITIC_MAX_TOKENS", 16384), "Max response tokens (a truncated response is salvaged and flagged rather than failed)")
	flags.IntVar(&f.maxIssues, "max-issues", envInt("PLANCRITIC_MAX_ISSUES", 50), "Max issues to return")
	flags.IntVar(&f.maxQuestions, "max-questions", envInt("PLANCRITIC_MAX_QUESTIONS", 20), "Max questions to return")
	flags.IntVar(&f.maxInputTokens, "max-input-tokens", envInt("PLANCRITIC_MAX_INPUT_TOKENS", 0), "Max estimated input tokens (0=unlimited)")
	flags.StringVar(&f.timeout, "timeout", envStr("PLANCRITIC_TIMEOUT", "5m"), "HTTP timeout for LLM requests (e.g., 5m, 10m)")
	flags.Float64Var(&f.temperature, "temperature", envFloat("PLANCRITIC_TEMPERATURE", 0.2), "Model temperature")
	flags.IntVar(&f.seed, "seed", 0, "Random seed (if supported)")
	flags.StringVar(&f.severityThreshold, "severity-threshold", envStr("PLANCRITIC_SEVERITY_THRESHOLD", "info"), "Minimum severity: info, warn, or critical")
	flags.StringVar(&f.patchOut, "patch-out", "", "Write suggested patches as unified diff (also asks the model to produce them)")
	flags.StringVar(&f.baseline, "baseline", envStr("PLANCRITIC_BASELINE", ""), "Earlier run's JSON output to compare against; adds resolved/new/persisting to the result")
	flags.BoolVar(&f.noLint, "no-lint", envBool("PLANCRITIC_NO_LINT", false), "Skip the local zero-token checks that run before the model (see 'plancritic lint')")
	flags.BoolVar(&f.checklists, "checklists", envBool("PLANCRITIC_CHECKLISTS", false), "Ask the model to grade every profile checklist item (PASS/FAIL/N/A) and include the result")
	flags.StringVar(&f.failOn, "fail-on", envStr("PLANCRITIC_FAIL_ON", ""), "Exit non-zero if verdict meets this level")
	flags.BoolVar(&f.redactEnabled, "redact", envBool("PLANCRITIC_REDACT", true), "Redact secrets before sending to model")
	flags.BoolVar(&f.noCache, "no-cache", envBool("PLANCRITIC_NO_CACHE", false), "Disable all caching: provider prompt caches and the local result cache")
	flags.BoolVar(&f.noResultCache, "no-result-cache", envBool("PLANCRITIC_NO_RESULT_CACHE", false), "Always call the model, even when an identical run is in the local result cache (set PLANCRITIC_CACHE_DIR to relocate the cache)")
	flags.StringVar(&f.cacheTTL, "cache-ttl", envStr("PLANCRITIC_CACHE_TTL", "1h"), "Lifetime of provider-side prompt caches: Anthropic uses 1h for values >= 1h (else 5m), Gemini uses the exact value")
	flags.BoolVar(&f.verbose, "verbose", false, "Print processing steps to stderr")
	flags.BoolVar(&f.debug, "debug", false, "Save prompt to debug file")

	return cmd
}

func runCheck(ctx context.Context, planPath string, f *checkFlags) error {
	switch f.format {
	case "json", "md", "compact":
	default:
		return exitError(3, "unknown format: %s (valid: json, md, compact)", f.format)
	}

	rev, err := runReview(ctx, planPath, f)
	if err != nil {
		return err
	}

	return emitReview(&rev, f, verboseLogger(f.verbose))
}

func runReview(parentCtx context.Context, planPath string, f *checkFlags) (review.Review, error) {
	rev, err := reviewer.Run(parentCtx, planPath, reviewer.Options{
		ContextPaths:      f.contextPaths,
		SpecPath:          f.specPath,
		ProfileName:       f.profileName,
		Strict:            f.strict,
		ProviderName:      f.providerName,
		Model:             f.model,
		Effort:            f.effort,
		Fast:              f.fast,
		MaxTokens:         f.maxTokens,
		MaxIssues:         f.maxIssues,
		MaxQuestions:      f.maxQuestions,
		MaxInputTokens:    f.maxInputTokens,
		Timeout:           f.timeout,
		Temperature:       f.temperature,
		Seed:              f.seed,
		HasSeed:           f.hasSeed,
		SeverityThreshold: f.severityThreshold,
		RedactEnabled:     f.redactEnabled,
		NoCache:           f.noCache,
		NoResultCache:     f.noResultCache,
		Patches:           f.patchOut != "",
		Checklists:        f.checklists,
		BaselinePath:      f.baseline,
		NoLint:            f.noLint,
		CacheTTL:          f.cacheTTL,
		Verbose:           f.verbose,
		Debug:             f.debug,
		DebugDir:          ".",
		Provider:          f.provider,
	}, version)
	if err != nil {
		var re *reviewer.Error
		if errors.As(err, &re) {
			return review.Review{}, exitError(re.Code, "%s", re.Msg)
		}
		return review.Review{}, exitError(4, "%v", err)
	}
	return rev, nil
}

type exitErr struct {
	code int
	msg  string
}

func (e *exitErr) Error() string { return e.msg }

func exitError(code int, format string, args ...any) error {
	return &exitErr{code: code, msg: fmt.Sprintf(format, args...)}
}

func verboseLogger(enabled bool) func(string, ...any) {
	logger := log.New(os.Stderr, "", 0)
	return func(msg string, args ...any) {
		if enabled {
			logger.Printf(msg, args...)
		}
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

// resolvePlanPath accepts the plan either as the positional argument or
// via --plan (agents and wrappers tend to reach for the flag), and
// rejects both or neither.
func resolvePlanPath(args []string, planFlag string) (string, error) {
	switch {
	case len(args) == 1 && planFlag != "":
		if args[0] == planFlag {
			return planFlag, nil
		}
		return "", exitError(3, "plan given twice: positional %q and --plan %q", args[0], planFlag)
	case len(args) == 1:
		return args[0], nil
	case planFlag != "":
		return planFlag, nil
	}
	return "", exitError(3, "plan file required: pass it as the argument or with --plan")
}

// emitReview writes the review in the requested format, the patch file,
// and applies --fail-on. It is shared by check and lint.
func emitReview(rev *review.Review, f *checkFlags, verbose func(string, ...any)) error {
	// 12. Output
	if f.noQuotes {
		review.StripQuotes(rev)
	}
	var output string
	switch f.format {
	case "json":
		data, err := json.MarshalIndent(rev, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal output: %w", err)
		}
		output = string(data) + "\n"
	case "md":
		output = render.Markdown(rev)
	case "compact":
		output = render.Compact(rev)
	}

	if f.out != "" {
		verbose("Writing output to %s", f.out)
		if err := os.WriteFile(f.out, []byte(output), 0644); err != nil {
			return fmt.Errorf("failed to write output: %w", err)
		}
		if f.quiet {
			fmt.Println(render.CompactHeader(rev))
		}
	} else if f.quiet {
		fmt.Println(render.CompactHeader(rev))
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

// toExitError maps a reviewer error onto the CLI exit code it carries,
// defaulting to 4 (provider/model error) for anything else.
func toExitError(err error) error {
	var re *reviewer.Error
	if errors.As(err, &re) {
		return exitError(re.Code, "%s", re.Msg)
	}
	return exitError(4, "%v", err)
}
