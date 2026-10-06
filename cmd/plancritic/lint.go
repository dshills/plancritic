package main

import (
	"github.com/dshills/plancritic/internal/reviewer"
	"github.com/spf13/cobra"
)

// newLintCmd runs only the local, deterministic checks: no provider,
// no tokens, no cache. The output has the same shape as check so the
// same consumers work, and the same output flags apply.
func newLintCmd() *cobra.Command {
	f := &checkFlags{}

	cmd := &cobra.Command{
		Use:   "lint [plan-file]",
		Short: "Run the local zero-token checks on a plan (no model call)",
		Long: `Run only the deterministic checks over a plan: the profile's vague
phrases and contradiction pairs, unresolved placeholders (TODO/TBD),
empty sections, duplicate headings, references to undefined phases, and
phases without acceptance criteria. Every finding is INFO and tagged
"local". No provider is contacted. The same checks run inside "check",
where the model is told not to repeat them.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			planPath, err := resolvePlanPath(args, f.planFlag)
			if err != nil {
				return err
			}
			return runLint(planPath, f)
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&f.planFlag, "plan", "", "Plan file path (alternative to the positional argument)")
	flags.StringVar(&f.profileName, "profile", envStr("PLANCRITIC_PROFILE", "general"), "Profile name (supplies the phrase and contradiction heuristics)")
	flags.StringVar(&f.format, "format", envStr("PLANCRITIC_FORMAT", "json"), "Output format: json, md, or compact")
	flags.StringVar(&f.out, "out", "", "Output file path (default: stdout)")
	flags.BoolVar(&f.noQuotes, "no-quotes", envBool("PLANCRITIC_NO_QUOTES", false), "Omit evidence quotes from json/md output")
	flags.BoolVar(&f.quiet, "quiet", envBool("PLANCRITIC_QUIET", false), "Print only the one-line verdict summary to stdout")
	flags.StringVar(&f.severityThreshold, "severity-threshold", envStr("PLANCRITIC_SEVERITY_THRESHOLD", "info"), "Minimum severity: info, warn, or critical")
	flags.IntVar(&f.maxIssues, "max-issues", envInt("PLANCRITIC_MAX_ISSUES", 50), "Max issues to return")
	flags.StringVar(&f.failOn, "fail-on", envStr("PLANCRITIC_FAIL_ON", ""), "Exit non-zero if verdict meets this level")
	flags.BoolVar(&f.redactEnabled, "redact", envBool("PLANCRITIC_REDACT", true), "Redact secrets in quoted evidence")
	flags.BoolVar(&f.verbose, "verbose", false, "Print processing steps to stderr")

	return cmd
}

func runLint(planPath string, f *checkFlags) error {
	switch f.format {
	case "json", "md", "compact":
	default:
		return exitError(3, "unknown format: %s (valid: json, md, compact)", f.format)
	}
	rev, err := reviewer.Lint(planPath, reviewer.LintOptions{
		ProfileName:       f.profileName,
		SeverityThreshold: f.severityThreshold,
		MaxIssues:         f.maxIssues,
		RedactEnabled:     f.redactEnabled,
	}, version)
	if err != nil {
		return toExitError(err)
	}
	return emitReview(&rev, f, verboseLogger(f.verbose))
}
