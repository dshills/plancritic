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
			return runCheck(cmd.Context(), args[0], f)
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

func runCheck(ctx context.Context, planPath string, f *checkFlags) error {
	if f.format != "json" && f.format != "md" {
		return exitError(3, "unknown format: %s", f.format)
	}

	rev, err := runReview(ctx, planPath, f)
	if err != nil {
		return err
	}

	verbose := verboseLogger(f.verbose)

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

func runReview(parentCtx context.Context, planPath string, f *checkFlags) (review.Review, error) {
	rev, err := reviewer.Run(parentCtx, planPath, reviewer.Options{
		ContextPaths:      f.contextPaths,
		ProfileName:       f.profileName,
		Strict:            f.strict,
		ProviderName:      f.providerName,
		Model:             f.model,
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
	}
	return rev, err
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
