package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

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
	model             string
	maxTokens         int
	temperature       float64
	seed              int
	hasSeed           bool
	severityThreshold string
	patchOut          string
	failOn            string
	redactEnabled     bool
	offline           bool
	verbose           bool
	debug             bool
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
	flags.StringVar(&f.format, "format", "json", "Output format: json or md")
	flags.StringVar(&f.out, "out", "", "Output file path (default: stdout)")
	flags.StringSliceVar(&f.contextPaths, "context", nil, "Context file paths (may be repeated)")
	flags.StringVar(&f.profileName, "profile", "general", "Profile name")
	flags.BoolVar(&f.strict, "strict", false, "Enable strict grounding mode")
	flags.StringVar(&f.model, "model", "", "Model ID (e.g., claude-sonnet-4-20250514, gpt-4o)")
	flags.IntVar(&f.maxTokens, "max-tokens", 4096, "Max response tokens")
	flags.Float64Var(&f.temperature, "temperature", 0.2, "Model temperature")
	flags.IntVar(&f.seed, "seed", 0, "Random seed (if supported)")
	flags.StringVar(&f.severityThreshold, "severity-threshold", "info", "Minimum severity: info, warn, or critical")
	flags.StringVar(&f.patchOut, "patch-out", "", "Write suggested patches as unified diff")
	flags.StringVar(&f.failOn, "fail-on", "", "Exit non-zero if verdict meets this level")
	flags.BoolVar(&f.redactEnabled, "redact", true, "Redact secrets before sending to model")
	flags.BoolVar(&f.offline, "offline", false, "Fail if no model provider is configured")
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
		for i := range p.Lines {
			p.Lines[i] = redact.Redact(p.Lines[i])
		}
		for _, cf := range contexts {
			cf.Raw = redact.Redact(cf.Raw)
			for j := range cf.Lines {
				cf.Lines[j] = redact.Redact(cf.Lines[j])
			}
		}
	}

	// 4. Load profile
	verbose("Loading profile: %s", f.profileName)
	prof, err := profile.LoadBuiltin(f.profileName)
	if err != nil {
		return exitError(3, "failed to load profile: %v", err)
	}

	// 5. Resolve LLM provider
	verbose("Resolving LLM provider")
	provider, err := llm.ResolveProvider(f.model)
	if err != nil {
		if f.offline {
			return exitError(4, "no model provider configured (--offline): %v", err)
		}
		return exitError(4, "model provider error: %v", err)
	}
	verbose("Using provider: %s", provider.Name())

	// 6. Build prompt
	promptText := prompt.Build(prompt.BuildOpts{
		Plan:         p,
		Contexts:     contexts,
		Profile:      prof,
		Strict:       f.strict,
		StepIDs:      stepIDs,
		MaxIssues:    review.DefaultMaxIssues,
		MaxQuestions:  review.DefaultMaxQuestions,
	})

	// 7. Debug output
	if f.debug {
		debugPath := "plancritic-debug-prompt.txt"
		verbose("Writing debug prompt to %s", debugPath)
		if err := os.WriteFile(debugPath, []byte(promptText), 0600); err != nil {
			verbose("Warning: failed to write debug prompt: %v", err)
		}
	}

	// 8. Call LLM
	verbose("Calling LLM...")
	settings := llm.Settings{
		Model:       f.model,
		Temperature: f.temperature,
		MaxTokens:   f.maxTokens,
	}
	if f.hasSeed {
		settings.Seed = &f.seed
	}

	result, err := provider.Generate(context.Background(), promptText, settings)
	if err != nil {
		return exitError(4, "LLM call failed: %v", err)
	}
	verbose("Received LLM response (%d bytes)", len(result))

	// 9. Parse JSON
	var rev review.Review
	if err := json.Unmarshal([]byte(result), &rev); err != nil {
		return exitError(5, "failed to parse LLM response as JSON: %v", err)
	}

	// 10. Validate
	validationErrs := schema.Validate(&rev, len(p.Lines))
	if len(validationErrs) > 0 {
		verbose("Validation failed (%d errors), attempting repair...", len(validationErrs))

		repairPrompt := prompt.BuildRepair(result, validationErrs)
		repairResult, err := provider.Generate(context.Background(), repairPrompt, settings)
		if err != nil {
			return exitError(4, "repair LLM call failed: %v", err)
		}

		var rev2 review.Review
		if err := json.Unmarshal([]byte(repairResult), &rev2); err != nil {
			return exitError(5, "repair response is not valid JSON: %v", err)
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
	// Override score and summary with deterministic computation
	rev.Summary = review.ComputeSummary(rev.Issues)
	review.SortIssues(rev.Issues)
	review.SortQuestions(rev.Questions)
	review.Truncate(&rev, review.DefaultMaxIssues, review.DefaultMaxQuestions)

	// Strict grounding post-check
	if f.strict {
		violations := review.CheckGrounding(&rev)
		if len(violations) > 0 {
			verbose("Grounding violations found: %d, applying downgrades", len(violations))
			review.ApplyGroundingDowngrades(&rev, violations)
			// Recompute after downgrades
			rev.Summary = review.ComputeSummary(rev.Issues)
			review.SortIssues(rev.Issues)
		}
	}

	// Apply severity threshold filter
	rev.Issues = filterBySeverity(rev.Issues, f.severityThreshold)
	rev.Questions = filterQuestionsBySeverity(rev.Questions, f.severityThreshold)
	// Recompute summary after filtering
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
		if verdictMeetsThreshold(rev.Summary.Verdict, f.failOn) {
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

func filterBySeverity(issues []review.Issue, threshold string) []review.Issue {
	minOrder := severityThresholdOrder(threshold)
	var result []review.Issue
	for _, iss := range issues {
		if iss.Severity.Valid() && severityOrder(iss.Severity) <= minOrder {
			result = append(result, iss)
		}
	}
	return result
}

func filterQuestionsBySeverity(questions []review.Question, threshold string) []review.Question {
	minOrder := severityThresholdOrder(threshold)
	var result []review.Question
	for _, q := range questions {
		if q.Severity.Valid() && severityOrder(q.Severity) <= minOrder {
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

func verdictMeetsThreshold(verdict review.Verdict, failOn string) bool {
	verdictLevel := map[review.Verdict]int{
		review.VerdictExecutable:         0,
		review.VerdictWithClarifications: 1,
		review.VerdictNotExecutable:      2,
	}
	thresholdLevel := map[string]int{
		"executable":         0,
		"clarifications":     1,
		"not_executable":     2,
		"not-executable":     2,
		"critical":           2,
	}

	vl, vlOk := verdictLevel[verdict]
	if !vlOk {
		return false
	}
	tl, ok := thresholdLevel[strings.ToLower(failOn)]
	if !ok {
		tl = 2
	}
	return vl >= tl
}
