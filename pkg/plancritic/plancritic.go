package plancritic

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dshills/plancritic/internal/llm"
	"github.com/dshills/plancritic/internal/profile"
	"github.com/dshills/plancritic/internal/render"
	"github.com/dshills/plancritic/internal/review"
	"github.com/dshills/plancritic/internal/reviewer"
)

type Review = review.Review
type Input = review.Input
type ContextFile = review.ContextFile
type Summary = review.Summary
type Issue = review.Issue
type Question = review.Question
type Patch = review.Patch
type Checklist = review.Checklist
type Evidence = review.Evidence
type Meta = review.Meta
type Severity = review.Severity
type Verdict = review.Verdict
type ModelInfo = llm.ModelInfo

type Error = reviewer.Error

type ContextDocument struct {
	Name string `json:"name"`
	Text string `json:"text"`
}

type CheckOptions struct {
	Version           string
	PlanPath          string
	PlanName          string
	PlanText          string
	ContextPaths      []string
	ContextDocuments  []ContextDocument
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
	RedactEnabled     bool
	NoCache           bool
	CacheTTL          string
	Verbose           bool
	Debug             bool
	DebugDir          string
}

type CheckResult struct {
	Review    *Review
	PatchDiff string
}

type ModelsResponse struct {
	Provider string      `json:"provider"`
	Models   []ModelInfo `json:"models"`
}

func DefaultCheckOptions() CheckOptions {
	return CheckOptions{
		Version:           "api",
		ProfileName:       "general",
		MaxTokens:         4096,
		MaxIssues:         50,
		MaxQuestions:      20,
		Timeout:           "5m",
		Temperature:       0.2,
		SeverityThreshold: "info",
		RedactEnabled:     true,
		CacheTTL:          "1h",
	}
}

func Check(ctx context.Context, opts CheckOptions) (*CheckResult, error) {
	if opts.Version == "" {
		opts.Version = "api"
	}
	planPath, contextPaths, cleanup, err := materializeInputs(opts)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	rev, err := reviewer.Run(ctx, planPath, reviewer.Options{
		ContextPaths:      contextPaths,
		ProfileName:       opts.ProfileName,
		Strict:            opts.Strict,
		ProviderName:      opts.ProviderName,
		Model:             opts.Model,
		MaxTokens:         opts.MaxTokens,
		MaxIssues:         opts.MaxIssues,
		MaxQuestions:      opts.MaxQuestions,
		MaxInputTokens:    opts.MaxInputTokens,
		Timeout:           opts.Timeout,
		Temperature:       opts.Temperature,
		Seed:              opts.Seed,
		HasSeed:           opts.HasSeed,
		SeverityThreshold: opts.SeverityThreshold,
		RedactEnabled:     opts.RedactEnabled,
		NoCache:           opts.NoCache,
		CacheTTL:          opts.CacheTTL,
		Verbose:           opts.Verbose,
		Debug:             opts.Debug,
		DebugDir:          opts.DebugDir,
	}, opts.Version)
	if err != nil {
		return nil, err
	}
	return &CheckResult{Review: &rev, PatchDiff: PatchDiff(rev.Patches)}, nil
}

func RenderReview(review *Review, format string) ([]byte, error) {
	switch format {
	case "", "json":
		return json.MarshalIndent(review, "", "  ")
	case "md":
		return []byte(render.Markdown(review)), nil
	default:
		return nil, fmt.Errorf("unknown format: %s", format)
	}
}

func PatchDiff(patches []Patch) string {
	if len(patches) == 0 {
		return ""
	}
	var b strings.Builder
	for _, patch := range patches {
		b.WriteString(patch.DiffUnified)
		if !strings.HasSuffix(patch.DiffUnified, "\n") {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func FilterReviewBySeverity(input *Review, threshold string) *Review {
	if input == nil {
		return nil
	}
	filtered := cloneReview(input)
	filtered.Issues = review.FilterBySeverity(filtered.Issues, threshold)
	filtered.Questions = review.FilterQuestionsBySeverity(filtered.Questions, threshold)
	filtered.Summary = review.ComputeSummary(filtered.Issues)
	return filtered
}

func VerdictMeetsThreshold(verdict Verdict, failOn string) (bool, error) {
	verdictLevel := map[Verdict]int{
		review.VerdictExecutable:         0,
		review.VerdictWithClarifications: 1,
		review.VerdictNotExecutable:      2,
	}
	failOnLevel := map[string]int{
		"executable":     0,
		"clarifications": 1,
		"not_executable": 2,
		"not-executable": 2,
		"critical":       2,
	}
	vl, ok := verdictLevel[verdict]
	if !ok {
		return false, nil
	}
	tl, ok := failOnLevel[strings.ToLower(failOn)]
	if !ok {
		return false, fmt.Errorf("unknown fail_on value: %q", failOn)
	}
	return vl >= tl, nil
}

func ListModels(ctx context.Context, provider string) (ModelsResponse, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	models, err := llm.ListModels(ctx, provider)
	if err != nil {
		return ModelsResponse{}, err
	}
	return ModelsResponse{Provider: provider, Models: models}, nil
}

func IsSupportedProvider(provider string) bool {
	return llm.IsSupportedProvider(provider)
}

func ProviderForModel(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(model, "anthropic:"), strings.HasPrefix(model, "claude"):
		return "anthropic"
	case strings.HasPrefix(model, "openai:"), strings.HasPrefix(model, "gpt"):
		return "openai"
	case strings.HasPrefix(model, "gemini:"), strings.HasPrefix(model, "gemini"):
		return "gemini"
	default:
		return ""
	}
}

func DefaultModelForProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai":
		return "gpt-5.2"
	case "gemini", "google":
		return "gemini-2.5-flash"
	default:
		return "claude-sonnet-4-6"
	}
}

func ProfileNames() []string {
	names, err := profile.List()
	if err != nil {
		return nil
	}
	return names
}

func materializeInputs(opts CheckOptions) (string, []string, func(), error) {
	if opts.PlanPath == "" && strings.TrimSpace(opts.PlanText) == "" {
		return "", nil, func() {}, fmt.Errorf("plan_path or plan_text is required")
	}
	if opts.PlanPath != "" && opts.PlanText != "" {
		return "", nil, func() {}, fmt.Errorf("plan_path and plan_text are mutually exclusive")
	}

	var cleanupPaths []string
	cleanup := func() {
		for _, path := range cleanupPaths {
			_ = os.Remove(path)
		}
	}

	planPath := opts.PlanPath
	if opts.PlanText != "" {
		path, err := writeTempText(opts.PlanName, opts.PlanText)
		if err != nil {
			cleanup()
			return "", nil, func() {}, err
		}
		cleanupPaths = append(cleanupPaths, path)
		planPath = path
	}

	contextPaths := append([]string(nil), opts.ContextPaths...)
	for _, doc := range opts.ContextDocuments {
		path, err := writeTempText(doc.Name, doc.Text)
		if err != nil {
			cleanup()
			return "", nil, func() {}, err
		}
		cleanupPaths = append(cleanupPaths, path)
		contextPaths = append(contextPaths, path)
	}
	return planPath, contextPaths, cleanup, nil
}

func writeTempText(name, text string) (string, error) {
	if name == "" {
		name = "PLAN.md"
	}
	ext := filepath.Ext(name)
	if ext == "" {
		ext = ".md"
	}
	sum := sha256.Sum256([]byte(name))
	pattern := fmt.Sprintf("plancritic-%x-*%s", sum[:4], ext)
	file, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", err
	}
	defer file.Close()
	if _, err := file.WriteString(text); err != nil {
		_ = os.Remove(file.Name())
		return "", err
	}
	return file.Name(), nil
}

func cloneReview(input *Review) *Review {
	clone := *input
	clone.Questions = append([]Question(nil), input.Questions...)
	clone.Issues = append([]Issue(nil), input.Issues...)
	clone.Patches = append([]Patch(nil), input.Patches...)
	clone.Checklists = append([]Checklist(nil), input.Checklists...)
	return &clone
}
