package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dshills/plancritic/internal/llm"
	"github.com/dshills/plancritic/internal/resultcache"
	"github.com/dshills/plancritic/internal/review"
)

// --- Pure function tests ---

func TestSeverityThresholdOrder(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"critical", 0},
		{"CRITICAL", 0},
		{"warn", 1},
		{"WARN", 1},
		{"info", 2},
		{"INFO", 2},
		{"", 2},
		{"unknown", 2},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := review.ThresholdOrder(tt.input)
			if got != tt.want {
				t.Errorf("review.ThresholdOrder(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestFilterBySeverity(t *testing.T) {
	issues := []review.Issue{
		{ID: "C1", Severity: review.SeverityCritical, Category: review.CategoryContradiction},
		{ID: "W1", Severity: review.SeverityWarn, Category: review.CategoryAmbiguity},
		{ID: "I1", Severity: review.SeverityInfo, Category: review.CategoryTestGap},
	}

	tests := []struct {
		threshold string
		wantIDs   []string
	}{
		{"critical", []string{"C1"}},
		{"warn", []string{"C1", "W1"}},
		{"info", []string{"C1", "W1", "I1"}},
		{"", []string{"C1", "W1", "I1"}},
	}
	for _, tt := range tests {
		t.Run(tt.threshold, func(t *testing.T) {
			got := review.FilterBySeverity(issues, tt.threshold)
			if len(got) != len(tt.wantIDs) {
				t.Fatalf("review.FilterBySeverity(%q) returned %d issues, want %d", tt.threshold, len(got), len(tt.wantIDs))
			}
			for i, id := range tt.wantIDs {
				if got[i].ID != id {
					t.Errorf("review.FilterBySeverity(%q)[%d].ID = %q, want %q", tt.threshold, i, got[i].ID, id)
				}
			}
		})
	}
}

func TestFilterBySeverityKeepsInvalid(t *testing.T) {
	issues := []review.Issue{
		{ID: "C1", Severity: review.SeverityCritical, Category: review.CategoryContradiction},
		{ID: "BAD", Severity: review.Severity("BOGUS"), Category: review.CategoryAmbiguity},
	}
	got := review.FilterBySeverity(issues, "info")
	if len(got) != 2 {
		t.Errorf("expected 2 issues (invalid severity kept), got %d", len(got))
	}
	// Even with threshold "critical", invalid severity items are kept
	got2 := review.FilterBySeverity(issues, "critical")
	if len(got2) != 2 {
		t.Errorf("expected 2 issues with critical threshold (invalid kept), got %d", len(got2))
	}
}

func TestFilterQuestionsBySeverity(t *testing.T) {
	questions := []review.Question{
		{ID: "Q1", Severity: review.SeverityCritical},
		{ID: "Q2", Severity: review.SeverityWarn},
		{ID: "Q3", Severity: review.SeverityInfo},
	}

	tests := []struct {
		threshold string
		wantIDs   []string
	}{
		{"critical", []string{"Q1"}},
		{"warn", []string{"Q1", "Q2"}},
		{"info", []string{"Q1", "Q2", "Q3"}},
	}
	for _, tt := range tests {
		t.Run(tt.threshold, func(t *testing.T) {
			got := review.FilterQuestionsBySeverity(questions, tt.threshold)
			if len(got) != len(tt.wantIDs) {
				t.Fatalf("got %d questions, want %d", len(got), len(tt.wantIDs))
			}
			for i, id := range tt.wantIDs {
				if got[i].ID != id {
					t.Errorf("[%d].ID = %q, want %q", i, got[i].ID, id)
				}
			}
		})
	}
}

func TestVerdictMeetsThreshold(t *testing.T) {
	tests := []struct {
		verdict review.Verdict
		failOn  string
		want    bool
		wantErr bool
	}{
		// executable verdict never meets any meaningful threshold
		{review.VerdictExecutable, "executable", true, false},
		{review.VerdictExecutable, "clarifications", false, false},
		{review.VerdictExecutable, "not_executable", false, false},

		// clarifications verdict
		{review.VerdictWithClarifications, "executable", true, false},
		{review.VerdictWithClarifications, "clarifications", true, false},
		{review.VerdictWithClarifications, "not_executable", false, false},
		{review.VerdictWithClarifications, "not-executable", false, false},

		// not_executable verdict
		{review.VerdictNotExecutable, "executable", true, false},
		{review.VerdictNotExecutable, "clarifications", true, false},
		{review.VerdictNotExecutable, "not_executable", true, false},
		{review.VerdictNotExecutable, "critical", true, false},

		// unknown verdict always returns false (no error)
		{review.Verdict("BOGUS"), "executable", false, false},

		// unknown failOn returns error
		{review.VerdictNotExecutable, "bogus_threshold", false, true},
	}
	for _, tt := range tests {
		name := string(tt.verdict) + "/" + tt.failOn
		t.Run(name, func(t *testing.T) {
			got, err := verdictMeetsThreshold(tt.verdict, tt.failOn)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error for unrecognized failOn value")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("verdictMeetsThreshold(%q, %q) = %v, want %v", tt.verdict, tt.failOn, got, tt.want)
			}
		})
	}
}

func TestRunCheckFailOnUnrecognized(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n")
	f := &checkFlags{
		format:            "json",
		profileName:       "general",
		redactEnabled:     true,
		severityThreshold: "info",
		failOn:            "bogus_value",
		provider:          &llm.MockProvider{Response: validMockResponse()},
	}
	err := runCheck(context.Background(), planPath, f)
	assertExitCode(t, err, 3)
}

// --- runCheck integration tests via MockProvider ---

// validMockResponse returns a JSON response that passes schema validation.
func validMockResponse() string {
	issues := []review.Issue{
		{
			ID:          "ISSUE-0001",
			Severity:    review.SeverityCritical,
			Category:    review.CategoryContradiction,
			Title:       "Test issue",
			Description: "A test issue",
			Evidence: []review.Evidence{
				{Source: "plan", Path: "plan.md", LineStart: 1, LineEnd: 1, Quote: "test"},
			},
			Impact:         "high",
			Recommendation: "fix it",
			Blocking:       true,
		},
	}

	rev := review.Review{
		Tool:    "plancritic",
		Version: "1.0",
		Summary: review.ComputeSummary(issues),
		Issues:  issues,
		Questions: []review.Question{
			{
				ID:        "Q-0001",
				Severity:  review.SeverityWarn,
				Question:  "What?",
				WhyNeeded: "Because",
				Evidence: []review.Evidence{
					{Source: "plan", Path: "plan.md", LineStart: 1, LineEnd: 1, Quote: "test"},
				},
			},
		},
	}

	data, _ := json.Marshal(rev)
	return string(data)
}

// writeTempPlan writes a plan file into a fresh temp dir and points the
// result cache at a fresh temp dir too. Every runCheck test goes through
// this helper, so no test can be served a cached review from another
// test (or from the developer's real cache).
func writeTempPlan(t *testing.T, content string) string {
	t.Helper()
	t.Setenv(resultcache.EnvDir, t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertExitCode(t *testing.T, err error, wantCode int) {
	t.Helper()
	if wantCode == 0 {
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		return
	}
	if err == nil {
		t.Fatalf("expected exit code %d, got nil error", wantCode)
	}
	var ee *exitErr
	if !errors.As(err, &ee) {
		t.Fatalf("expected *exitErr, got %T: %v", err, err)
	}
	if ee.code != wantCode {
		t.Errorf("exit code = %d, want %d (msg: %s)", ee.code, wantCode, ee.msg)
	}
}

func TestRunCheckHappyPath(t *testing.T) {
	planPath := writeTempPlan(t, "# Step 1\nDo something\n")
	f := &checkFlags{
		format:            "json",
		profileName:       "general",
		redactEnabled:     true,
		severityThreshold: "info",
		temperature:       0.2,
		maxTokens:         4096,
		provider:          &llm.MockProvider{Response: validMockResponse()},
	}
	err := runCheck(context.Background(), planPath, f)
	assertExitCode(t, err, 0)
}

func TestRunCheckMissingPlanFile(t *testing.T) {
	f := &checkFlags{
		format:            "json",
		profileName:       "general",
		redactEnabled:     true,
		severityThreshold: "info",
		provider:          &llm.MockProvider{Response: "{}"},
	}
	err := runCheck(context.Background(), "/nonexistent/plan.md", f)
	assertExitCode(t, err, 3)
}

func TestRunCheckBadContextPath(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n")
	f := &checkFlags{
		format:            "json",
		profileName:       "general",
		redactEnabled:     true,
		severityThreshold: "info",
		contextPaths:      []string{"/nonexistent/context.md"},
		provider:          &llm.MockProvider{Response: "{}"},
	}
	err := runCheck(context.Background(), planPath, f)
	assertExitCode(t, err, 3)
}

func TestRunCheckUnknownProfile(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n")
	f := &checkFlags{
		format:            "json",
		profileName:       "nonexistent-profile-xyz",
		redactEnabled:     true,
		severityThreshold: "info",
		provider:          &llm.MockProvider{Response: "{}"},
	}
	err := runCheck(context.Background(), planPath, f)
	assertExitCode(t, err, 3)
}

func TestRunCheckLLMError(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n")
	f := &checkFlags{
		format:            "json",
		profileName:       "general",
		redactEnabled:     true,
		severityThreshold: "info",
		provider:          &llm.MockProvider{Err: errors.New("model exploded")},
	}
	err := runCheck(context.Background(), planPath, f)
	assertExitCode(t, err, 4)
}

func TestRunCheckLLMReturnsNonJSON(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n")
	f := &checkFlags{
		format:            "json",
		profileName:       "general",
		redactEnabled:     true,
		severityThreshold: "info",
		provider:          &llm.MockProvider{Response: "this is not json at all"},
	}
	err := runCheck(context.Background(), planPath, f)
	assertExitCode(t, err, 5)
}

func TestRunCheckSchemaValidationFailsRepairSucceeds(t *testing.T) {
	// First response: issue with invalid severity (structural error)
	badResp := `{"summary":{"verdict":"EXECUTABLE_AS_IS"},"issues":[{"id":"I1","severity":"BOGUS","category":"CONTRADICTION","title":"t","description":"d","evidence":[{"source":"plan","path":"p","line_start":1,"line_end":1,"quote":"q"}]}],"questions":[]}`

	mock := &callCountMockProvider{
		responses: []string{badResp, validMockResponse()},
	}

	planPath := writeTempPlan(t, "# Plan\n")
	f := &checkFlags{
		format:            "json",
		profileName:       "general",
		redactEnabled:     true,
		severityThreshold: "info",
		provider:          mock,
	}
	err := runCheck(context.Background(), planPath, f)
	// The first response has invalid severity, so validation fails.
	// The second response (validMockResponse) should pass.
	assertExitCode(t, err, 0)
}

func TestRunCheckSchemaValidationFailsBothAttempts(t *testing.T) {
	// Both responses have structural errors (invalid severity)
	badResp := `{"summary":{"verdict":"EXECUTABLE_AS_IS"},"issues":[{"id":"I1","severity":"BOGUS","category":"CONTRADICTION","title":"t","description":"d","evidence":[{"source":"plan","path":"p","line_start":1,"line_end":1,"quote":"q"}]}],"questions":[]}`

	mock := &callCountMockProvider{
		responses: []string{badResp, badResp},
	}

	planPath := writeTempPlan(t, "# Plan\n")
	f := &checkFlags{
		format:            "json",
		profileName:       "general",
		redactEnabled:     true,
		severityThreshold: "info",
		provider:          mock,
	}
	err := runCheck(context.Background(), planPath, f)
	assertExitCode(t, err, 5)
}

func TestRunCheckFormatMarkdown(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n")
	dir := t.TempDir()
	outPath := filepath.Join(dir, "out.md")

	f := &checkFlags{
		format:            "md",
		out:               outPath,
		profileName:       "general",
		redactEnabled:     true,
		severityThreshold: "info",
		provider:          &llm.MockProvider{Response: validMockResponse()},
	}
	err := runCheck(context.Background(), planPath, f)
	assertExitCode(t, err, 0)

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "#") {
		t.Error("expected markdown output with headers")
	}
}

func TestRunCheckFormatUnknown(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n")
	f := &checkFlags{
		format:            "xml",
		profileName:       "general",
		redactEnabled:     true,
		severityThreshold: "info",
		provider:          &llm.MockProvider{Response: validMockResponse()},
	}
	err := runCheck(context.Background(), planPath, f)
	assertExitCode(t, err, 3)
}

func TestRunCheckOutFile(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n")
	dir := t.TempDir()
	outPath := filepath.Join(dir, "result.json")

	f := &checkFlags{
		format:            "json",
		out:               outPath,
		profileName:       "general",
		redactEnabled:     true,
		severityThreshold: "info",
		provider:          &llm.MockProvider{Response: validMockResponse()},
	}
	err := runCheck(context.Background(), planPath, f)
	assertExitCode(t, err, 0)

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty output file")
	}

	var rev review.Review
	if err := json.Unmarshal(data, &rev); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
}

func TestRunCheckFailOn(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n")
	f := &checkFlags{
		format:            "json",
		profileName:       "general",
		redactEnabled:     true,
		severityThreshold: "info",
		failOn:            "executable",
		provider:          &llm.MockProvider{Response: validMockResponse()},
	}
	err := runCheck(context.Background(), planPath, f)
	assertExitCode(t, err, 2)
}

func TestRunCheckDebugWritesPromptFile(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n")

	// Change to temp dir so debug file goes there
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	f := &checkFlags{
		format:            "json",
		profileName:       "general",
		redactEnabled:     true,
		severityThreshold: "info",
		debug:             true,
		provider:          &llm.MockProvider{Response: validMockResponse()},
	}
	err := runCheck(context.Background(), planPath, f)
	assertExitCode(t, err, 0)

	matches, err := filepath.Glob(filepath.Join(tmpDir, "plancritic-debug-prompt-*.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Error("expected debug prompt file to be created")
	}
}

func TestRunCheckRedactDisabled(t *testing.T) {
	// Plan with a secret pattern
	planPath := writeTempPlan(t, "# Plan\nAPI_KEY=sk-abc123secret\n")
	dir := t.TempDir()
	outPath := filepath.Join(dir, "result.json")

	f := &checkFlags{
		format:            "json",
		out:               outPath,
		profileName:       "general",
		redactEnabled:     false,
		severityThreshold: "info",
		debug:             true,
		provider:          &llm.MockProvider{Response: validMockResponse()},
	}

	t.Chdir(dir)

	err := runCheck(context.Background(), planPath, f)
	assertExitCode(t, err, 0)

	// When redact is disabled, the debug prompt should contain the secret
	matches, err := filepath.Glob(filepath.Join(dir, "plancritic-debug-prompt-*.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("expected debug prompt file")
	}
	found := false
	for _, match := range matches {
		debugData, err := os.ReadFile(match)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(debugData), "sk-abc123secret") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected secret to pass through when redact is disabled")
	}
}

func TestRunCheckSeverityThresholdCritical(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n")
	dir := t.TempDir()
	outPath := filepath.Join(dir, "result.json")

	f := &checkFlags{
		format:            "json",
		out:               outPath,
		profileName:       "general",
		redactEnabled:     true,
		severityThreshold: "critical",
		provider:          &llm.MockProvider{Response: validMockResponse()},
	}
	err := runCheck(context.Background(), planPath, f)
	assertExitCode(t, err, 0)

	data, err2 := os.ReadFile(outPath)
	if err2 != nil {
		t.Fatal(err2)
	}
	var rev review.Review
	if err3 := json.Unmarshal(data, &rev); err3 != nil {
		t.Fatal(err3)
	}

	// validMockResponse has 1 CRITICAL issue and 1 WARN question
	// With threshold "critical", only CRITICAL items should remain
	for _, iss := range rev.Issues {
		if iss.Severity != review.SeverityCritical {
			t.Errorf("expected only CRITICAL issues, got %s", iss.Severity)
		}
	}
	if len(rev.Questions) != 0 {
		t.Errorf("expected 0 questions with critical threshold, got %d", len(rev.Questions))
	}
}

func TestRunCheckStrict(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\nDo something\n")
	f := &checkFlags{
		format:            "json",
		profileName:       "general",
		redactEnabled:     true,
		severityThreshold: "info",
		strict:            true,
		provider:          &llm.MockProvider{Response: validMockResponse()},
	}
	err := runCheck(context.Background(), planPath, f)
	assertExitCode(t, err, 0)
}

func TestRunCheckWithContext(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\nDo something\n")
	dir := t.TempDir()
	ctxPath := writeTempFile(t, dir, "context.md", "# Context\nSome context info\n")

	f := &checkFlags{
		format:            "json",
		profileName:       "general",
		redactEnabled:     true,
		severityThreshold: "info",
		contextPaths:      []string{ctxPath},
		provider:          &llm.MockProvider{Response: validMockResponse()},
	}
	err := runCheck(context.Background(), planPath, f)
	assertExitCode(t, err, 0)
}

// --- env helper tests ---

func TestEnvStr(t *testing.T) {
	t.Setenv("PLANCRITIC_TEST_STR", "hello")
	if got := envStr("PLANCRITIC_TEST_STR", "default"); got != "hello" {
		t.Errorf("envStr = %q, want %q", got, "hello")
	}
	if got := envStr("PLANCRITIC_TEST_UNSET", "default"); got != "default" {
		t.Errorf("envStr = %q, want %q", got, "default")
	}
}

func TestEnvBool(t *testing.T) {
	t.Setenv("PLANCRITIC_TEST_BOOL", "true")
	if got := envBool("PLANCRITIC_TEST_BOOL", false); !got {
		t.Error("envBool = false, want true")
	}
	t.Setenv("PLANCRITIC_TEST_BOOL", "false")
	if got := envBool("PLANCRITIC_TEST_BOOL", true); got {
		t.Error("envBool = true, want false")
	}
	if got := envBool("PLANCRITIC_TEST_UNSET", true); !got {
		t.Error("envBool fallback = false, want true")
	}
	t.Setenv("PLANCRITIC_TEST_BOOL", "invalid")
	if got := envBool("PLANCRITIC_TEST_BOOL", true); !got {
		t.Error("envBool invalid = false, want fallback true")
	}
}

func TestEnvInt(t *testing.T) {
	t.Setenv("PLANCRITIC_TEST_INT", "8192")
	if got := envInt("PLANCRITIC_TEST_INT", 4096); got != 8192 {
		t.Errorf("envInt = %d, want 8192", got)
	}
	if got := envInt("PLANCRITIC_TEST_UNSET", 4096); got != 4096 {
		t.Errorf("envInt fallback = %d, want 4096", got)
	}
	t.Setenv("PLANCRITIC_TEST_INT", "bad")
	if got := envInt("PLANCRITIC_TEST_INT", 4096); got != 4096 {
		t.Errorf("envInt invalid = %d, want fallback 4096", got)
	}
}

func TestEnvFloat(t *testing.T) {
	t.Setenv("PLANCRITIC_TEST_FLOAT", "0.5")
	if got := envFloat("PLANCRITIC_TEST_FLOAT", 0.2); got != 0.5 {
		t.Errorf("envFloat = %f, want 0.5", got)
	}
	if got := envFloat("PLANCRITIC_TEST_UNSET", 0.2); got != 0.2 {
		t.Errorf("envFloat fallback = %f, want 0.2", got)
	}
	t.Setenv("PLANCRITIC_TEST_FLOAT", "bad")
	if got := envFloat("PLANCRITIC_TEST_FLOAT", 0.2); got != 0.2 {
		t.Errorf("envFloat invalid = %f, want fallback 0.2", got)
	}
}

// callCountMockProvider returns different responses on successive calls.
type callCountMockProvider struct {
	responses []string
	callIdx   int
	prompts   []string       // every prompt received, in order
	settings  []llm.Settings // settings for each call, in order
}

func (m *callCountMockProvider) Name() string { return "mock" }

func (m *callCountMockProvider) Generate(_ context.Context, prompt string, s llm.Settings) (string, llm.Usage, error) {
	m.prompts = append(m.prompts, prompt)
	m.settings = append(m.settings, s)
	if m.callIdx >= len(m.responses) {
		return "", llm.Usage{}, errors.New("no more mock responses")
	}
	resp := m.responses[m.callIdx]
	m.callIdx++
	return resp, llm.Usage{}, nil
}

// --- result cache ---

func cacheTestFlags(t *testing.T, mock *llm.MockProvider) *checkFlags {
	t.Helper()
	return &checkFlags{
		format:            "json",
		out:               filepath.Join(t.TempDir(), "out.json"),
		profileName:       "general",
		redactEnabled:     true,
		severityThreshold: "info",
		provider:          mock,
	}
}

func readReview(t *testing.T, path string) review.Review {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var rev review.Review
	if err := json.Unmarshal(data, &rev); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	return rev
}

func TestRunCheckResultCacheHitSkipsProvider(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n1. Do the thing\n")
	mock := &llm.MockProvider{Response: validMockResponse()}

	f1 := cacheTestFlags(t, mock)
	if err := runCheck(context.Background(), planPath, f1); err != nil {
		t.Fatal(err)
	}
	if mock.Calls != 1 {
		t.Fatalf("first run should call the provider once, got %d", mock.Calls)
	}
	first := readReview(t, f1.out)
	if first.Meta.Cached {
		t.Error("first run must not be marked cached")
	}

	f2 := cacheTestFlags(t, mock)
	if err := runCheck(context.Background(), planPath, f2); err != nil {
		t.Fatal(err)
	}
	if mock.Calls != 1 {
		t.Fatalf("second identical run should be served from cache, provider calls = %d", mock.Calls)
	}
	second := readReview(t, f2.out)
	if !second.Meta.Cached {
		t.Error("second run should be marked cached")
	}
	if second.Summary != first.Summary {
		t.Errorf("cached summary differs: %+v vs %+v", second.Summary, first.Summary)
	}
	if len(second.Issues) != len(first.Issues) || second.Issues[0].Evidence[0].Quote != first.Issues[0].Evidence[0].Quote {
		t.Error("cached issues should match the original run, including reconstructed quotes")
	}
	if second.Input.PlanHash != first.Input.PlanHash || second.Tool != "plancritic" {
		t.Error("cached run should carry freshly computed metadata")
	}
}

func TestRunCheckResultCacheMissWhenPlanChanges(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n1. Step A\n")
	mock := &llm.MockProvider{Response: validMockResponse()}

	if err := runCheck(context.Background(), planPath, cacheTestFlags(t, mock)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planPath, []byte("# Plan\n1. Step A revised\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runCheck(context.Background(), planPath, cacheTestFlags(t, mock)); err != nil {
		t.Fatal(err)
	}
	if mock.Calls != 2 {
		t.Errorf("edited plan should miss the cache, provider calls = %d", mock.Calls)
	}
}

func TestRunCheckResultCacheMissWhenSettingsChange(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n1. Step A\n")
	mock := &llm.MockProvider{Response: validMockResponse()}

	if err := runCheck(context.Background(), planPath, cacheTestFlags(t, mock)); err != nil {
		t.Fatal(err)
	}

	strict := cacheTestFlags(t, mock)
	strict.strict = true
	if err := runCheck(context.Background(), planPath, strict); err != nil {
		t.Fatal(err)
	}
	if mock.Calls != 2 {
		t.Errorf("--strict changes the prompt and must miss, provider calls = %d", mock.Calls)
	}

	temp := cacheTestFlags(t, mock)
	temp.temperature = 0.9
	if err := runCheck(context.Background(), planPath, temp); err != nil {
		t.Fatal(err)
	}
	if mock.Calls != 3 {
		t.Errorf("--temperature changes sampling and must miss, provider calls = %d", mock.Calls)
	}
}

func TestRunCheckResultCacheHitHonorsOutputFlags(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n1. Step A\n")
	mock := &llm.MockProvider{Response: validMockResponse()}

	if err := runCheck(context.Background(), planPath, cacheTestFlags(t, mock)); err != nil {
		t.Fatal(err)
	}

	// Output-only flags (format, quotes) must still hit.
	f := cacheTestFlags(t, mock)
	f.format = "compact"
	f.noQuotes = true
	if err := runCheck(context.Background(), planPath, f); err != nil {
		t.Fatal(err)
	}
	if mock.Calls != 1 {
		t.Fatalf("format and --no-quotes are output-only and should still hit, provider calls = %d", mock.Calls)
	}
	if data, _ := os.ReadFile(f.out); !strings.Contains(string(data), "VERDICT NOT_EXECUTABLE") || !strings.Contains(string(data), " cached") {
		t.Errorf("cached result should render in the requested format: %s", data)
	}

	// The severity threshold is told to the model, so it is part of the
	// prompt and a different threshold is a different review.
	g := cacheTestFlags(t, mock)
	g.severityThreshold = "critical"
	if err := runCheck(context.Background(), planPath, g); err != nil {
		t.Fatal(err)
	}
	if mock.Calls != 2 {
		t.Fatalf("a different severity threshold must miss the cache, provider calls = %d", mock.Calls)
	}
	rev := readReview(t, g.out)
	if len(rev.Questions) != 0 || len(rev.Issues) != 1 {
		t.Errorf("post-hoc filter should still apply: %d issues, %d questions", len(rev.Issues), len(rev.Questions))
	}
}

func TestRunCheckResultCacheDisabledByFlags(t *testing.T) {
	for _, tc := range []struct {
		name  string
		apply func(*checkFlags)
	}{
		{"no-result-cache", func(f *checkFlags) { f.noResultCache = true }},
		{"no-cache", func(f *checkFlags) { f.noCache = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			planPath := writeTempPlan(t, "# Plan\n1. Step "+tc.name+"\n")
			mock := &llm.MockProvider{Response: validMockResponse()}
			for i := 0; i < 2; i++ {
				f := cacheTestFlags(t, mock)
				tc.apply(f)
				if err := runCheck(context.Background(), planPath, f); err != nil {
					t.Fatal(err)
				}
			}
			if mock.Calls != 2 {
				t.Errorf("cache disabled: expected 2 provider calls, got %d", mock.Calls)
			}
		})
	}
}

func TestRunCheckProviderErrorIsNotCached(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n1. Step A\n")
	mock := &llm.MockProvider{Response: "not json at all"}

	if err := runCheck(context.Background(), planPath, cacheTestFlags(t, mock)); err == nil {
		t.Fatal("expected a schema error for a non-JSON response")
	}
	mock.Response = validMockResponse()
	if err := runCheck(context.Background(), planPath, cacheTestFlags(t, mock)); err != nil {
		t.Fatalf("second run should succeed: %v", err)
	}
	if mock.Calls != 2 {
		t.Errorf("a failed run must not populate the cache, provider calls = %d", mock.Calls)
	}
}

// --- local auto-fix and delta repair ---

func TestRunCheckAutoFixAvoidsRepairCall(t *testing.T) {
	// No trailing newline: the plan is exactly 2 lines.
	planPath := writeTempPlan(t, "# Plan\n1. Step A")
	// Two issues with the same ID; the second cites past the end of the
	// 2-line plan and has an inverted range. All of it is mechanical.
	resp := `{"summary":{"verdict":"EXECUTABLE_AS_IS"},"issues":[
	  {"id":"ISSUE-0001","severity":"WARN","category":"AMBIGUITY","title":"a","description":"d","evidence":[{"source":"plan","path":"plan.md","line_start":1,"line_end":1}]},
	  {"id":"ISSUE-0001","severity":"WARN","category":"AMBIGUITY","title":"b","description":"d","evidence":[{"source":"plan","path":"plan.md","line_start":99,"line_end":2}]}
	],"questions":[]}`
	mock := &llm.MockProvider{Response: resp}
	f := cacheTestFlags(t, mock)
	if err := runCheck(context.Background(), planPath, f); err != nil {
		t.Fatal(err)
	}
	if mock.Calls != 1 {
		t.Fatalf("mechanical defects should be fixed locally without a repair call, got %d calls", mock.Calls)
	}
	rev := readReview(t, f.out)
	if len(rev.Issues) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(rev.Issues))
	}
	ids := map[string]bool{rev.Issues[0].ID: true, rev.Issues[1].ID: true}
	if len(ids) != 2 {
		t.Errorf("duplicate IDs should have been renumbered: %v", ids)
	}
	for _, iss := range rev.Issues {
		if iss.Title == "b" {
			ev := iss.Evidence[0]
			if ev.LineStart != 2 || ev.LineEnd != 2 {
				t.Errorf("range should be swapped then clamped to 2-2, got %d-%d", ev.LineStart, ev.LineEnd)
			}
			if ev.Quote != "1. Step A" {
				t.Errorf("quote should be reconstructed from the fixed range, got %q", ev.Quote)
			}
		}
	}
}

func TestRunCheckDeltaRepairMergesOnlyOffendingItems(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n1. Step A\n")
	first := `{"summary":{"verdict":"EXECUTABLE_AS_IS"},"issues":[
	  {"id":"ISSUE-0001","severity":"WARN","category":"AMBIGUITY","title":"Keep me","description":"d","evidence":[{"source":"plan","path":"plan.md","line_start":1,"line_end":1}]},
	  {"id":"ISSUE-0002","severity":"BOGUS","category":"AMBIGUITY","title":"Broken","description":"d","evidence":[{"source":"plan","path":"plan.md","line_start":2,"line_end":2}]}
	],"questions":[]}`
	repair := `{"summary":{"verdict":"EXECUTABLE_AS_IS"},"issues":[
	  {"id":"ISSUE-0002","severity":"INFO","category":"AMBIGUITY","title":"Fixed","description":"d","evidence":[{"source":"plan","path":"plan.md","line_start":2,"line_end":2}]}
	],"questions":[],"patches":[],"checklists":[]}`
	mock := &callCountMockProvider{responses: []string{first, repair}}
	f := cacheTestFlags(t, nil)
	f.provider = mock
	if err := runCheck(context.Background(), planPath, f); err != nil {
		t.Fatal(err)
	}
	if mock.callIdx != 2 {
		t.Fatalf("expected exactly one repair call, got %d calls total", mock.callIdx)
	}
	repairPrompt := mock.prompts[1]
	if strings.Contains(repairPrompt, "Keep me") {
		t.Error("delta repair must not resend the valid issue")
	}
	if !strings.Contains(repairPrompt, "Broken") || !strings.Contains(repairPrompt, "issues[1].severity") {
		t.Errorf("delta repair should resend the offending issue with its error:\n%s", repairPrompt)
	}

	rev := readReview(t, f.out)
	if len(rev.Issues) != 2 {
		t.Fatalf("expected 2 issues after merge, got %d", len(rev.Issues))
	}
	titles := map[string]bool{rev.Issues[0].Title: true, rev.Issues[1].Title: true}
	if !titles["Keep me"] || !titles["Fixed"] {
		t.Errorf("merged issues should be the kept original and the repaired one, got %v", titles)
	}
}

func TestRunCheckDeltaRepairShortResponseIsSchemaError(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n1. Step A\n")
	first := `{"summary":{"verdict":"EXECUTABLE_AS_IS"},"issues":[
	  {"id":"ISSUE-0001","severity":"BOGUS","category":"AMBIGUITY","title":"Broken","description":"d","evidence":[{"source":"plan","path":"plan.md","line_start":1,"line_end":1}]}
	],"questions":[]}`
	repair := `{"summary":{"verdict":"EXECUTABLE_AS_IS"},"issues":[],"questions":[],"patches":[],"checklists":[]}`
	mock := &callCountMockProvider{responses: []string{first, repair}}
	f := cacheTestFlags(t, nil)
	f.provider = mock
	err := runCheck(context.Background(), planPath, f)
	assertExitCode(t, err, 5)
}

func TestRunCheckInvalidVerdictIsFixedLocally(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n1. Step A")
	// The model's verdict is never used (it is recomputed from the
	// issues), so an invalid one must not cost a repair round trip.
	first := `{"summary":{"verdict":"MAYBE"},"issues":[],"questions":[]}`
	mock := &callCountMockProvider{responses: []string{first}}
	f := cacheTestFlags(t, nil)
	f.provider = mock
	if err := runCheck(context.Background(), planPath, f); err != nil {
		t.Fatal(err)
	}
	if mock.callIdx != 1 {
		t.Errorf("expected no repair call, got %d calls", mock.callIdx)
	}
	if rev := readReview(t, f.out); rev.Summary.Verdict != review.VerdictExecutable {
		t.Errorf("verdict should be recomputed from the (empty) issues, got %s", rev.Summary.Verdict)
	}
}

func TestRunCheckDeltaRepairSendsSourcesForEvidenceErrors(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n1. Step A")
	// Issue with no evidence at all: the model must pick a citation, so
	// the repair prompt has to carry the plan text.
	first := `{"summary":{"verdict":"EXECUTABLE_AS_IS"},"issues":[
	  {"id":"ISSUE-0001","severity":"WARN","category":"AMBIGUITY","title":"No evidence","description":"d","evidence":[]}
	],"questions":[]}`
	repair := `{"summary":{"verdict":"EXECUTABLE_AS_IS"},"issues":[
	  {"id":"ISSUE-0001","severity":"WARN","category":"AMBIGUITY","title":"No evidence","description":"d","evidence":[{"source":"plan","path":"plan.md","line_start":2,"line_end":2}]}
	],"questions":[],"patches":[],"checklists":[]}`
	mock := &callCountMockProvider{responses: []string{first, repair}}
	f := cacheTestFlags(t, nil)
	f.provider = mock
	if err := runCheck(context.Background(), planPath, f); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mock.prompts[1], "2|1. Step A") {
		t.Error("evidence repair should include the line-numbered plan")
	}
	// A severity-only error needs no sources (see the merge test above).
	severityOnly := `{"summary":{"verdict":"EXECUTABLE_AS_IS"},"issues":[
	  {"id":"ISSUE-0001","severity":"BOGUS","category":"AMBIGUITY","title":"x","description":"d","evidence":[{"source":"plan","path":"plan.md","line_start":1,"line_end":1}]}
	],"questions":[]}`
	mock2 := &callCountMockProvider{responses: []string{severityOnly, repair}}
	f2 := cacheTestFlags(t, nil)
	f2.provider = mock2
	planPath2 := writeTempPlan(t, "# Plan\n1. Step A")
	if err := runCheck(context.Background(), planPath2, f2); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(mock2.prompts[1], "## Sources") {
		t.Error("structural repair should not resend the plan text")
	}
}

func TestRunCheckVerdictAndIDsFixedTogetherWithoutRepair(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n1. Step A")
	// Distinct citations so the two issues are not merged as duplicates.
	first := `{"summary":{"verdict":"MAYBE"},"issues":[
	  {"id":"ISSUE-0001","severity":"WARN","category":"AMBIGUITY","title":"a","description":"d","evidence":[{"source":"plan","path":"plan.md","line_start":1,"line_end":1}]},
	  {"id":"ISSUE-0001","severity":"WARN","category":"AMBIGUITY","title":"b","description":"d","evidence":[{"source":"plan","path":"plan.md","line_start":2,"line_end":2}]}
	],"questions":[]}`
	mock := &callCountMockProvider{responses: []string{first}}
	f := cacheTestFlags(t, nil)
	f.provider = mock
	if err := runCheck(context.Background(), planPath, f); err != nil {
		t.Fatal(err)
	}
	if mock.callIdx != 1 {
		t.Errorf("all defects were mechanical; expected no repair call, got %d calls", mock.callIdx)
	}
	rev := readReview(t, f.out)
	if len(rev.Issues) != 2 || rev.Issues[0].ID == rev.Issues[1].ID {
		t.Errorf("expected two issues with distinct IDs, got %+v", rev.Issues)
	}
}

func TestRunCheckPatchesAndChecklistsAreOptIn(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n1. Step A")

	plain := &callCountMockProvider{responses: []string{validMockResponse()}}
	f := cacheTestFlags(t, nil)
	f.provider = plain
	if err := runCheck(context.Background(), planPath, f); err != nil {
		t.Fatal(err)
	}
	prompt, schema := plain.prompts[0], string(plain.settings[0].OutputSchema)
	for _, absent := range []string{`"patches"`, `"checklists"`, `Include "patches"`} {
		if strings.Contains(prompt, absent) {
			t.Errorf("default prompt should not contain %s", absent)
		}
	}
	if strings.Contains(schema, `"patches"`) || strings.Contains(schema, `"checklists"`) {
		t.Error("default structured-output schema should not request patches or checklists")
	}

	shaped := &callCountMockProvider{responses: []string{validMockResponse()}}
	f2 := cacheTestFlags(t, nil)
	f2.provider = shaped
	f2.patchOut = filepath.Join(t.TempDir(), "fixes.diff")
	f2.checklists = true
	planPath2 := writeTempPlan(t, "# Plan\n1. Step A")
	if err := runCheck(context.Background(), planPath2, f2); err != nil {
		t.Fatal(err)
	}
	prompt, schema = shaped.prompts[0], string(shaped.settings[0].OutputSchema)
	for _, present := range []string{`"diff_unified"`, `Include "patches"`, `"checklists" as PASS`} {
		if !strings.Contains(prompt, present) {
			t.Errorf("shaped prompt should contain %s", present)
		}
	}
	if !strings.Contains(schema, `"patches"`) || !strings.Contains(schema, `"checklists"`) {
		t.Error("shaped structured-output schema should request patches and checklists")
	}
}

// --- truncated output salvage ---

func TestRunCheckSalvagesTruncatedOutput(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n1. Step A")
	partial := `{"summary":{"verdict":"EXECUTABLE_AS_IS"},"questions":[],"issues":[` +
		`{"id":"ISSUE-0001","severity":"CRITICAL","category":"CONTRADICTION","title":"Complete one","description":"d","evidence":[{"source":"plan","path":"plan.md","line_start":1,"line_end":1}],"impact":"i","recommendation":"r","blocking":true},` +
		`{"id":"ISSUE-0002","severity":"WARN","category":"AMBIGUITY","title":"Cut off mid`
	mock := &llm.MockProvider{Response: partial, Err: &llm.TruncatedError{Provider: "mock", MaxTokens: 123, Partial: partial}}

	f := cacheTestFlags(t, mock)
	if err := runCheck(context.Background(), planPath, f); err != nil {
		t.Fatalf("truncated output with a complete issue should be salvaged, got %v", err)
	}
	rev := readReview(t, f.out)
	if !rev.Meta.Truncated {
		t.Error("meta.truncated should be set")
	}
	titles := map[string]bool{}
	for _, iss := range rev.Issues {
		titles[iss.Title] = true
	}
	if !titles["Complete one"] || !titles["Model output truncated"] || titles["Cut off mid"] {
		t.Errorf("expected the complete issue plus a truncation notice, got %v", titles)
	}
	if rev.Summary.Verdict != review.VerdictNotExecutable {
		t.Errorf("verdict should still reflect the salvaged blocking critical, got %s", rev.Summary.Verdict)
	}
	for _, iss := range rev.Issues {
		if iss.Title != "Model output truncated" {
			continue
		}
		ev := iss.Evidence[0]
		if ev.Path != "plan.md" || ev.LineStart != 1 || ev.Quote != "# Plan" {
			t.Errorf("truncation notice should cite the real first line of the plan, got %+v", ev)
		}
		if !strings.Contains(strings.Join(iss.Tags, ","), "system") {
			t.Errorf("truncation notice should be tagged system, got %v", iss.Tags)
		}
	}

	// A salvaged review must not be served from cache next time.
	f2 := cacheTestFlags(t, mock)
	if err := runCheck(context.Background(), planPath, f2); err != nil {
		t.Fatal(err)
	}
	if mock.Calls != 2 {
		t.Errorf("truncated result should not be cached, provider calls = %d", mock.Calls)
	}
}

func TestRunCheckUnsalvageableTruncationIsProviderError(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n1. Step A")
	partial := `{"summary":{"verdict":"EXEC`
	mock := &llm.MockProvider{Response: partial, Err: &llm.TruncatedError{Provider: "mock", MaxTokens: 50, Partial: partial}}
	err := runCheck(context.Background(), planPath, cacheTestFlags(t, mock))
	assertExitCode(t, err, 4)
	if !strings.Contains(err.Error(), "--max-tokens") {
		t.Errorf("error should point at --max-tokens, got: %v", err)
	}
}

func TestMaxTokensDefault(t *testing.T) {
	t.Setenv("PLANCRITIC_MAX_TOKENS", "")
	cmd := newCheckCmd()
	if got := cmd.Flags().Lookup("max-tokens").DefValue; got != "16384" {
		t.Errorf("--max-tokens default = %s, want 16384", got)
	}
}

func TestRunCheckRepairRaisesSmallMaxTokens(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n1. Step A")
	first := `{"summary":{"verdict":"EXECUTABLE_AS_IS"},"issues":[
	  {"id":"ISSUE-0001","severity":"BOGUS","category":"AMBIGUITY","title":"x","description":"d","evidence":[{"source":"plan","path":"plan.md","line_start":1,"line_end":1}]}
	],"questions":[]}`
	repair := `{"summary":{"verdict":"EXECUTABLE_AS_IS"},"issues":[
	  {"id":"ISSUE-0001","severity":"WARN","category":"AMBIGUITY","title":"x","description":"d","evidence":[{"source":"plan","path":"plan.md","line_start":1,"line_end":1}]}
	],"questions":[],"patches":[],"checklists":[]}`
	mock := &callCountMockProvider{responses: []string{first, repair}}
	f := cacheTestFlags(t, nil)
	f.provider = mock
	f.maxTokens = 500
	if err := runCheck(context.Background(), planPath, f); err != nil {
		t.Fatal(err)
	}
	if got := mock.settings[0].MaxTokens; got != 500 {
		t.Errorf("original call should keep the user's cap, got %d", got)
	}
	if got := mock.settings[1].MaxTokens; got < 8192 {
		t.Errorf("repair call should get at least 8192 output tokens, got %d", got)
	}
}

// --- compact format, --no-quotes, --quiet ---

// captureStdout runs fn with os.Stdout redirected and returns what it wrote.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string)
	go func() {
		data, _ := io.ReadAll(r)
		done <- string(data)
	}()
	fn()
	_ = w.Close()
	os.Stdout = orig
	return <-done
}

func TestRunCheckCompactFormat(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n1. Step A")
	mock := &llm.MockProvider{Response: validMockResponse()}
	f := cacheTestFlags(t, mock)
	f.format = "compact"
	f.out = ""
	out := captureStdout(t, func() {
		if err := runCheck(context.Background(), planPath, f); err != nil {
			t.Error(err)
		}
	})
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected header + 1 issue + 1 question, got %d lines:\n%s", len(lines), out)
	}
	if !strings.HasPrefix(lines[0], "VERDICT NOT_EXECUTABLE score=80 critical=1") {
		t.Errorf("header = %q", lines[0])
	}
	if lines[1] != `ISSUE-0001 CRITICAL(blocking) CONTRADICTION plan.md:L1 "Test issue" -> fix it` {
		t.Errorf("issue line = %q", lines[1])
	}
	if lines[2] != `Q-0001 WARN plan.md:L1 "What?" -> Because` {
		t.Errorf("question line = %q", lines[2])
	}
	if strings.Contains(out, "# Plan") {
		t.Error("compact output must not include quoted plan text")
	}
}

func TestRunCheckNoQuotesDropsQuoteField(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n1. Step A")
	mock := &llm.MockProvider{Response: validMockResponse()}
	f := cacheTestFlags(t, mock)
	f.noQuotes = true
	if err := runCheck(context.Background(), planPath, f); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(f.out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"quote"`) {
		t.Error("--no-quotes JSON should omit the quote field entirely")
	}
	if !strings.Contains(string(data), `"line_start": 1`) {
		t.Error("line references must be kept")
	}
}

func TestRunCheckQuietPrintsHeaderOnly(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n1. Step A")
	mock := &llm.MockProvider{Response: validMockResponse()}
	f := cacheTestFlags(t, mock)
	f.quiet = true
	out := captureStdout(t, func() {
		if err := runCheck(context.Background(), planPath, f); err != nil {
			t.Error(err)
		}
	})
	if strings.Count(out, "\n") != 1 || !strings.HasPrefix(out, "VERDICT NOT_EXECUTABLE ") {
		t.Errorf("--quiet should print exactly the header line, got %q", out)
	}
	if _, err := os.Stat(f.out); err != nil {
		t.Errorf("full output should still be written to --out: %v", err)
	}
}

func TestRunCheckUnknownFormatListsValidOnes(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n")
	f := cacheTestFlags(t, &llm.MockProvider{Response: validMockResponse()})
	f.format = "yaml"
	err := runCheck(context.Background(), planPath, f)
	assertExitCode(t, err, 3)
	if !strings.Contains(err.Error(), "compact") {
		t.Errorf("error should list the valid formats, got %v", err)
	}
}

// --- fingerprints and --baseline ---

func TestRunCheckBaselineDelta(t *testing.T) {
	// Run 1: a contradiction at L2 and a test gap at L3.
	run1 := `{"summary":{"verdict":"EXECUTABLE_AS_IS"},"questions":[],"issues":[
	  {"id":"ISSUE-0001","severity":"CRITICAL","category":"CONTRADICTION","title":"Deps contradiction","description":"d","evidence":[{"source":"plan","path":"plan.md","line_start":2,"line_end":2}],"impact":"i","recommendation":"r","blocking":true},
	  {"id":"ISSUE-0002","severity":"WARN","category":"TEST_GAP","title":"No tests","description":"d","evidence":[{"source":"plan","path":"plan.md","line_start":3,"line_end":3}],"impact":"i","recommendation":"r","blocking":false}
	]}`
	planPath := writeTempPlan(t, "# Plan\n1. No external deps\n2. Ship it")
	f1 := cacheTestFlags(t, &llm.MockProvider{Response: run1})
	if err := runCheck(context.Background(), planPath, f1); err != nil {
		t.Fatal(err)
	}
	first := readReview(t, f1.out)
	if first.Issues[0].Fingerprint == "" || first.Issues[1].Fingerprint == "" || first.Delta != nil {
		t.Fatalf("run 1 should carry fingerprints and no delta: %+v", first)
	}

	// Revise: insert a line above everything (shifting line numbers), fix
	// the deps line. The test gap persists at a new line number; the
	// contradiction's cited text changed, so it is gone; a new issue appears.
	run2 := `{"summary":{"verdict":"EXECUTABLE_AS_IS"},"questions":[],"issues":[
	  {"id":"ISSUE-0001","severity":"WARN","category":"TEST_GAP","title":"Still no tests","description":"d","evidence":[{"source":"plan","path":"plan.md","line_start":4,"line_end":4}],"impact":"i","recommendation":"r","blocking":false},
	  {"id":"ISSUE-0002","severity":"INFO","category":"AMBIGUITY","title":"Vague intro","description":"d","evidence":[{"source":"plan","path":"plan.md","line_start":2,"line_end":2}],"impact":"i","recommendation":"r","blocking":false}
	]}`
	// (No profile trigger words here, or the local lint would add findings.)
	if err := os.WriteFile(planPath, []byte("# Plan\nIntro: describe the goal\n1. Uses libfoo (approved)\n2. Ship it"), 0o644); err != nil {
		t.Fatal(err)
	}
	f2 := cacheTestFlags(t, &llm.MockProvider{Response: run2})
	f2.baseline = f1.out
	if err := runCheck(context.Background(), planPath, f2); err != nil {
		t.Fatal(err)
	}
	second := readReview(t, f2.out)
	d := second.Delta
	if d == nil {
		t.Fatal("run 2 should carry a delta")
	}
	if d.BaselineFile != filepath.Base(f1.out) || d.BaselinePlanHash != first.Input.PlanHash {
		t.Errorf("delta header wrong: %+v", d)
	}
	if len(d.Persisting) != 1 || d.Persisting[0].Title != "Still no tests" {
		t.Errorf("the test gap moved lines but cites the same text; expected it persisting, got %+v", d.Persisting)
	}
	if len(d.New) != 1 || d.New[0].Title != "Vague intro" {
		t.Errorf("new = %+v", d.New)
	}
	if len(d.Resolved) != 1 || d.Resolved[0].Title != "Deps contradiction" || d.Resolved[0].ID != "ISSUE-0001" {
		t.Errorf("resolved should name the baseline finding, got %+v", d.Resolved)
	}
	if d.ScoreChange != second.Summary.Score-first.Summary.Score {
		t.Errorf("score change = %d", d.ScoreChange)
	}

	// Compact rendering of the same run carries the markers.
	f3 := cacheTestFlags(t, &llm.MockProvider{Response: run2})
	f3.baseline = f1.out
	f3.format = "compact"
	if err := runCheck(context.Background(), planPath, f3); err != nil {
		t.Fatal(err)
	}
	compact, _ := os.ReadFile(f3.out)
	for _, want := range []string{"new=1 persisting=1 resolved=1", `"Still no tests" -> r [persisting]`, `"Vague intro" -> r [new]`, `RESOLVED issue ISSUE-0001 "Deps contradiction"`} {
		if !strings.Contains(string(compact), want) {
			t.Errorf("compact output missing %q:\n%s", want, compact)
		}
	}
}

func TestRunCheckBaselineErrors(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n")
	mock := &llm.MockProvider{Response: validMockResponse()}
	f := cacheTestFlags(t, mock)
	f.baseline = filepath.Join(t.TempDir(), "missing.json")
	err := runCheck(context.Background(), planPath, f)
	assertExitCode(t, err, 3)
	if mock.Calls != 0 {
		t.Error("a bad baseline must fail before any provider call")
	}
}

func TestRunCheckDedupsDuplicateIssues(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n1. Step A\n2. Step B")
	resp := `{"summary":{"verdict":"EXECUTABLE_AS_IS"},"questions":[],"issues":[
	  {"id":"ISSUE-0001","severity":"INFO","category":"AMBIGUITY","title":"Vague step wording","description":"d","evidence":[{"source":"plan","path":"plan.md","line_start":2,"line_end":2}],"impact":"i","recommendation":"r","blocking":false},
	  {"id":"ISSUE-0002","severity":"WARN","category":"AMBIGUITY","title":"The vague step wording.","description":"d","evidence":[{"source":"plan","path":"plan.md","line_start":2,"line_end":2}],"impact":"i","recommendation":"r","blocking":false},
	  {"id":"ISSUE-0003","severity":"WARN","category":"TEST_GAP","title":"No tests","description":"d","evidence":[{"source":"plan","path":"plan.md","line_start":2,"line_end":2}],"impact":"i","recommendation":"r","blocking":false}
	]}`
	f := cacheTestFlags(t, &llm.MockProvider{Response: resp})
	if err := runCheck(context.Background(), planPath, f); err != nil {
		t.Fatal(err)
	}
	rev := readReview(t, f.out)
	if len(rev.Issues) != 2 {
		t.Fatalf("same-line AMBIGUITY issues with restated titles should merge, got %d issues", len(rev.Issues))
	}
	// Sorted by severity first, so the WARN copy is kept and absorbs the INFO one.
	kept := rev.Issues[0]
	if kept.Category == review.CategoryAmbiguity {
		if kept.Severity != review.SeverityWarn || !strings.Contains(strings.Join(kept.Tags, ","), "merged:ISSUE-0001") {
			t.Errorf("the more severe copy should be kept with a merged tag, got %+v", kept)
		}
	}
	if rev.Summary.WarnCount != 2 || rev.Summary.InfoCount != 0 {
		t.Errorf("score counts should reflect the merged list: %+v", rev.Summary)
	}
}

func TestRunCheckPromptCarriesSeverityThreshold(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n")
	mock := &callCountMockProvider{responses: []string{validMockResponse()}}
	f := cacheTestFlags(t, nil)
	f.provider = mock
	f.severityThreshold = "warn"
	if err := runCheck(context.Background(), planPath, f); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mock.prompts[0], "the caller discards INFO") {
		t.Error("prompt should tell the model the severity threshold")
	}
}

// --- --effort and --fast ---

// namedProvider gives a mock a real provider name so provider-keyed
// behavior (fast tier) can be exercised.
type namedProvider struct {
	*callCountMockProvider
	name string
}

func (n *namedProvider) Name() string { return n.name }

func TestRunCheckInvalidEffortFailsBeforeProviderCall(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n")
	mock := &llm.MockProvider{Response: validMockResponse()}
	f := cacheTestFlags(t, mock)
	f.effort = "ultra"
	err := runCheck(context.Background(), planPath, f)
	assertExitCode(t, err, 3)
	if mock.Calls != 0 || !strings.Contains(err.Error(), "xhigh") {
		t.Errorf("invalid effort should fail before any call and list valid values: calls=%d err=%v", mock.Calls, err)
	}
}

func TestRunCheckEffortReachesProviderAndCacheKey(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n")
	mock := &callCountMockProvider{responses: []string{validMockResponse(), validMockResponse()}}

	f := cacheTestFlags(t, nil)
	f.provider = mock
	f.effort = "low"
	if err := runCheck(context.Background(), planPath, f); err != nil {
		t.Fatal(err)
	}
	if mock.settings[0].Effort != "low" {
		t.Errorf("effort should reach the provider settings, got %q", mock.settings[0].Effort)
	}

	g := cacheTestFlags(t, nil)
	g.provider = mock
	g.effort = "high"
	if err := runCheck(context.Background(), planPath, g); err != nil {
		t.Fatal(err)
	}
	if mock.callIdx != 2 {
		t.Errorf("a different effort is a different review and must miss the cache, calls=%d", mock.callIdx)
	}
}

func TestRunCheckFastTier(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n")
	inner := &callCountMockProvider{responses: []string{validMockResponse(), validMockResponse()}}
	mock := &namedProvider{callCountMockProvider: inner, name: "anthropic"}

	f := cacheTestFlags(t, nil)
	f.provider = mock
	f.fast = true
	if err := runCheck(context.Background(), planPath, f); err != nil {
		t.Fatal(err)
	}
	if got := inner.settings[0].Model; got != llm.FastModel("anthropic") {
		t.Errorf("--fast should request the fast tier model, got %q", got)
	}
	if rev := readReview(t, f.out); rev.Meta.Model != "anthropic/"+llm.FastModel("anthropic") {
		t.Errorf("meta.model should name the fast tier, got %q", rev.Meta.Model)
	}

	g := cacheTestFlags(t, nil)
	g.provider = mock
	g.fast = true
	g.model = "claude-opus-5-5"
	if err := runCheck(context.Background(), planPath, g); err != nil {
		t.Fatal(err)
	}
	if got := inner.settings[1].Model; got != "claude-opus-5-5" {
		t.Errorf("an explicit --model must win over --fast, got %q", got)
	}
}

// --- meta.usage and --cache-ttl ---

func TestRunCheckReportsUsageAcrossCalls(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n1. Step A")
	// First response has an invalid severity (needs a repair call), so
	// usage must sum both calls.
	first := `{"summary":{"verdict":"EXECUTABLE_AS_IS"},"issues":[
	  {"id":"ISSUE-0001","severity":"BOGUS","category":"AMBIGUITY","title":"x","description":"d","evidence":[{"source":"plan","path":"plan.md","line_start":1,"line_end":1}]}
	],"questions":[]}`
	repair := `{"summary":{"verdict":"EXECUTABLE_AS_IS"},"issues":[
	  {"id":"ISSUE-0001","severity":"WARN","category":"AMBIGUITY","title":"x","description":"d","evidence":[{"source":"plan","path":"plan.md","line_start":1,"line_end":1}]}
	],"questions":[],"patches":[],"checklists":[]}`
	mock := &usageMockProvider{callCountMockProvider: &callCountMockProvider{responses: []string{first, repair}},
		usage: llm.Usage{InputTokens: 100, OutputTokens: 40, CacheReadInputTokens: 60, CacheCreationInputTokens: 7}}
	f := cacheTestFlags(t, nil)
	f.provider = mock
	if err := runCheck(context.Background(), planPath, f); err != nil {
		t.Fatal(err)
	}
	rev := readReview(t, f.out)
	u := rev.Meta.Usage
	if u == nil {
		t.Fatal("meta.usage should be present on a fresh review")
	}
	want := review.Usage{Calls: 2, InputTokens: 200, OutputTokens: 80, CacheReadInputTokens: 120, CacheCreationInputTokens: 14}
	if *u != want {
		t.Errorf("usage = %+v, want %+v", *u, want)
	}

	// A cache hit reports no usage (nothing was spent) and the compact
	// header carries the figures only on a fresh run.
	g := cacheTestFlags(t, nil)
	g.provider = mock
	g.format = "compact"
	if err := runCheck(context.Background(), planPath, g); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(g.out)
	if !strings.Contains(string(data), " cached") || strings.Contains(string(data), " in=") {
		t.Errorf("cached compact header should have no usage figures: %s", data)
	}
	h := cacheTestFlags(t, &llm.MockProvider{Response: validMockResponse(), Usage: llm.Usage{InputTokens: 5, OutputTokens: 6}})
	h.format = "compact"
	h.noResultCache = true
	if err := runCheck(context.Background(), planPath, h); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(h.out)
	if !strings.Contains(string(data), " in=5 out=6") {
		t.Errorf("fresh compact header should show in/out tokens: %s", data)
	}
}

// usageMockProvider reports a fixed usage on every call.
type usageMockProvider struct {
	*callCountMockProvider
	usage llm.Usage
}

func (u *usageMockProvider) Generate(ctx context.Context, prompt string, s llm.Settings) (string, llm.Usage, error) {
	out, _, err := u.callCountMockProvider.Generate(ctx, prompt, s)
	return out, u.usage, err
}

func TestRunCheckInvalidCacheTTLFailsFast(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n")
	mock := &llm.MockProvider{Response: validMockResponse()}
	f := cacheTestFlags(t, mock)
	f.cacheTTL = "soon"
	err := runCheck(context.Background(), planPath, f)
	assertExitCode(t, err, 3)
	if mock.Calls != 0 {
		t.Error("an invalid --cache-ttl must fail before any provider call")
	}
}

// --- --spec coverage and --plan alias ---

func TestResolvePlanPath(t *testing.T) {
	if p, err := resolvePlanPath([]string{"a.md"}, ""); err != nil || p != "a.md" {
		t.Errorf("positional: %q %v", p, err)
	}
	if p, err := resolvePlanPath(nil, "b.md"); err != nil || p != "b.md" {
		t.Errorf("--plan: %q %v", p, err)
	}
	if p, err := resolvePlanPath([]string{"c.md"}, "c.md"); err != nil || p != "c.md" {
		t.Errorf("same path both ways is fine: %q %v", p, err)
	}
	if _, err := resolvePlanPath([]string{"a.md"}, "b.md"); err == nil {
		t.Error("two different plans must be rejected")
	}
	_, err := resolvePlanPath(nil, "")
	assertExitCode(t, err, 3)
}

func TestRunCheckSpecCoverage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(resultcache.EnvDir, t.TempDir())
	planPath := filepath.Join(dir, "PLAN.md")
	specPath := filepath.Join(dir, "SPEC.md")
	if err := os.WriteFile(planPath, []byte("# Plan\n1. Build login\n2. Add dark mode"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(specPath, []byte("# Spec\nUsers must log in.\nAll logins must be audited."), 0o644); err != nil {
		t.Fatal(err)
	}
	resp := `{"summary":{"verdict":"EXECUTABLE_AS_IS"},"issues":[],"questions":[],"coverage":{
	  "requirements":[
	    {"id":"REQ-0001","requirement":"Users must log in","status":"COVERED","spec_evidence":[{"source":"context","path":"SPEC.md","line_start":2,"line_end":2}],"plan_evidence":[{"source":"plan","path":"PLAN.md","line_start":2,"line_end":2}],"note":""},
	    {"id":"REQ-0002","requirement":"Logins audited","status":"UNCOVERED","spec_evidence":[{"source":"context","path":"SPEC.md","line_start":3,"line_end":3}],"plan_evidence":[],"note":"no audit step"}
	  ],
	  "out_of_scope":[{"id":"SCOPE-0001","plan_step":"Dark mode","plan_evidence":[{"source":"plan","path":"PLAN.md","line_start":3,"line_end":3}],"note":"not in spec"}]
	}}`
	mock := &callCountMockProvider{responses: []string{resp}}
	f := cacheTestFlags(t, nil)
	f.provider = mock
	f.specPath = specPath
	if err := runCheck(context.Background(), planPath, f); err != nil {
		t.Fatal(err)
	}
	prompt := mock.prompts[0]
	if !strings.Contains(prompt, `path="SPEC.md" role="spec"##`) || !strings.Contains(prompt, "## Specification Coverage") {
		t.Error("spec should be sent with the spec role and coverage instructions")
	}
	if !strings.Contains(string(mock.settings[0].OutputSchema), `"coverage"`) {
		t.Error("structured-output schema should request coverage when a spec is given")
	}
	rev := readReview(t, f.out)
	c := rev.Coverage
	if c == nil {
		t.Fatal("coverage block missing")
	}
	if c.Summary != (review.CoverageSummary{Covered: 1, Partial: 0, Uncovered: 1, OutOfScope: 1}) {
		t.Errorf("summary = %+v", c.Summary)
	}
	if c.Requirements[0].SpecEvidence[0].Quote != "Users must log in." || c.Requirements[0].PlanEvidence[0].Quote != "1. Build login" {
		t.Errorf("coverage quotes should be reconstructed: %+v", c.Requirements[0])
	}
	if rev.Input.ContextFiles[0].Path != "SPEC.md" {
		t.Errorf("the spec should be recorded as an input context file: %+v", rev.Input.ContextFiles)
	}

	// A model that omits the block despite the spec is sent to repair; a
	// repair that supplies it succeeds, one that still omits it is exit 5.
	withoutCov := `{"summary":{"verdict":"EXECUTABLE_AS_IS"},"issues":[],"questions":[]}`
	repairMock := &callCountMockProvider{responses: []string{withoutCov, resp}}
	h := cacheTestFlags(t, nil)
	h.provider = repairMock
	h.specPath = specPath
	h.noResultCache = true // same inputs as the run above; must not be served from cache
	if err := runCheck(context.Background(), planPath, h); err != nil {
		t.Fatalf("a repair that adds coverage should succeed: %v", err)
	}
	if repairMock.callIdx != 2 || !strings.Contains(repairMock.prompts[1], "coverage: required when a specification is provided") {
		t.Errorf("missing coverage should trigger a repair naming the problem: calls=%d", repairMock.callIdx)
	}
	if got := readReview(t, h.out).Coverage; got == nil || got.Summary.Covered != 1 {
		t.Errorf("repaired coverage should be reported: %+v", got)
	}
	stillMissing := &callCountMockProvider{responses: []string{withoutCov, withoutCov}}
	k := cacheTestFlags(t, nil)
	k.provider = stillMissing
	k.specPath = specPath
	k.noResultCache = true
	assertExitCode(t, runCheck(context.Background(), planPath, k), 5)

	// Without --spec the model is not asked for coverage and none is reported.
	plain := &callCountMockProvider{responses: []string{validMockResponse()}}
	g := cacheTestFlags(t, nil)
	g.provider = plain
	if err := runCheck(context.Background(), planPath, g); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain.prompts[0], "Specification Coverage") || readReview(t, g.out).Coverage != nil {
		t.Error("no spec: no coverage")
	}
}

func TestRunCheckSpecMissingFailsFast(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n")
	mock := &llm.MockProvider{Response: validMockResponse()}
	f := cacheTestFlags(t, mock)
	f.specPath = filepath.Join(t.TempDir(), "missing.md")
	err := runCheck(context.Background(), planPath, f)
	assertExitCode(t, err, 3)
	if mock.Calls != 0 {
		t.Error("a missing spec must fail before any provider call")
	}
}

// --- local lint ---

func TestRunLintCommandNeedsNoProvider(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n## Phase 1: Setup\nMake it fast. TODO decide.\n## Phase 2: Build\nAcceptance criteria: builds.\nSee Phase 9.")
	f := &checkFlags{format: "json", out: filepath.Join(t.TempDir(), "lint.json"), profileName: "general", severityThreshold: "info", redactEnabled: true}
	if err := runLint(planPath, f); err != nil {
		t.Fatal(err)
	}
	rev := readReview(t, f.out)
	if rev.Meta.Model != "local/lint" || rev.Tool != "plancritic" {
		t.Errorf("lint review should be labelled local/lint: %+v", rev.Meta)
	}
	titles := map[string]bool{}
	for _, iss := range rev.Issues {
		titles[iss.Title] = true
		if iss.Severity != review.SeverityInfo || iss.Blocking || iss.Fingerprint == "" || !strings.HasPrefix(iss.ID, "ISSUE-LINT-") {
			t.Errorf("local finding should be INFO, non-blocking, fingerprinted, LINT-numbered: %+v", iss)
		}
	}
	for _, want := range []string{`Vague phrase "fast"`, "Unresolved placeholder", "Reference to undefined Phase 9", "Phase without acceptance criteria"} {
		if !titles[want] {
			t.Errorf("expected finding %q, got %v", want, titles)
		}
	}
	if rev.Summary.Verdict != review.VerdictExecutable || rev.Summary.InfoCount != len(rev.Issues) {
		t.Errorf("INFO-only findings keep the plan executable: %+v", rev.Summary)
	}

	// --fail-on and the output formats work as for check.
	g := &checkFlags{format: "compact", out: filepath.Join(t.TempDir(), "lint.txt"), profileName: "general", severityThreshold: "info", failOn: "executable"}
	assertExitCode(t, runLint(planPath, g), 2)
	data, _ := os.ReadFile(g.out)
	if !strings.HasPrefix(string(data), "VERDICT EXECUTABLE_AS_IS ") || !strings.Contains(string(data), "[local,trigger-phrase]") {
		t.Errorf("compact lint output wrong:\n%s", data)
	}
	h := &checkFlags{format: "yaml", profileName: "general"}
	assertExitCode(t, runLint(planPath, h), 3)
}

func TestRunCheckMergesLocalFindingsAndPreFlags(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n1. Make it robust\n2. TBD")
	mock := &callCountMockProvider{responses: []string{validMockResponse()}}
	f := cacheTestFlags(t, nil)
	f.provider = mock
	if err := runCheck(context.Background(), planPath, f); err != nil {
		t.Fatal(err)
	}
	prompt := mock.prompts[0]
	if !strings.Contains(prompt, "## Already Flagged Locally") || !strings.Contains(prompt, `AMBIGUITY: Vague phrase "robust" (L2)`) {
		t.Errorf("prompt should list the local findings:\n%s", prompt)
	}
	rev := readReview(t, f.out)
	var local, model int
	for _, iss := range rev.Issues {
		if strings.HasPrefix(iss.ID, "ISSUE-LINT-") {
			local++
		} else {
			model++
		}
	}
	if local != 2 || model != 1 {
		t.Errorf("expected 2 local + 1 model issue, got local=%d model=%d", local, model)
	}
	if rev.Issues[0].ID != "ISSUE-0001" {
		t.Errorf("the model's CRITICAL should sort before the local INFO findings, got %s first", rev.Issues[0].ID)
	}

	off := &callCountMockProvider{responses: []string{validMockResponse()}}
	g := cacheTestFlags(t, nil)
	g.provider = off
	g.noLint = true
	if err := runCheck(context.Background(), planPath, g); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(off.prompts[0], "Already Flagged Locally") || len(readReview(t, g.out).Issues) != 1 {
		t.Error("--no-lint should neither pre-flag nor merge local findings")
	}
}

func TestRunCheckModelFindingSupersedesLocalCandidate(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n1. Make it robust")
	// The model confirms the vague phrase on L2 at WARN with its own title.
	resp := `{"summary":{"verdict":"EXECUTABLE_AS_IS"},"questions":[],"issues":[
	  {"id":"ISSUE-0001","severity":"WARN","category":"AMBIGUITY","title":"Robustness is undefined","description":"d","evidence":[{"source":"plan","path":"plan.md","line_start":2,"line_end":2}],"impact":"i","recommendation":"r","blocking":false}
	]}`
	f := cacheTestFlags(t, &llm.MockProvider{Response: resp})
	if err := runCheck(context.Background(), planPath, f); err != nil {
		t.Fatal(err)
	}
	rev := readReview(t, f.out)
	if len(rev.Issues) != 1 || rev.Issues[0].ID != "ISSUE-0001" || rev.Issues[0].Severity != review.SeverityWarn {
		t.Errorf("the model's confirmed WARN should replace the local INFO candidate, got %+v", rev.Issues)
	}
}
