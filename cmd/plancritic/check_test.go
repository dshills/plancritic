package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dshills/plancritic/internal/llm"
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
			got := severityThresholdOrder(tt.input)
			if got != tt.want {
				t.Errorf("severityThresholdOrder(%q) = %d, want %d", tt.input, got, tt.want)
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
			got := filterBySeverity(issues, tt.threshold)
			if len(got) != len(tt.wantIDs) {
				t.Fatalf("filterBySeverity(%q) returned %d issues, want %d", tt.threshold, len(got), len(tt.wantIDs))
			}
			for i, id := range tt.wantIDs {
				if got[i].ID != id {
					t.Errorf("filterBySeverity(%q)[%d].ID = %q, want %q", tt.threshold, i, got[i].ID, id)
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
	got := filterBySeverity(issues, "info")
	if len(got) != 2 {
		t.Errorf("expected 2 issues (invalid severity kept), got %d", len(got))
	}
	// Even with threshold "critical", invalid severity items are kept
	got2 := filterBySeverity(issues, "critical")
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
			got := filterQuestionsBySeverity(questions, tt.threshold)
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
	err := runCheck(planPath, f)
	assertExitCode(t, err, 3)
}

// --- runCheck integration tests via MockProvider ---

// validMockResponse returns a JSON response that passes schema validation.
func validMockResponse() string {
	issues := []review.Issue{
		{
			ID:       "ISSUE-0001",
			Severity: review.SeverityCritical,
			Category: review.CategoryContradiction,
			Title:    "Test issue",
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

func writeTempPlan(t *testing.T, content string) string {
	t.Helper()
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
	err := runCheck(planPath, f)
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
	err := runCheck("/nonexistent/plan.md", f)
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
	err := runCheck(planPath, f)
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
	err := runCheck(planPath, f)
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
	err := runCheck(planPath, f)
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
	err := runCheck(planPath, f)
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
	err := runCheck(planPath, f)
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
	err := runCheck(planPath, f)
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
	err := runCheck(planPath, f)
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
	err := runCheck(planPath, f)
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
	err := runCheck(planPath, f)
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
	err := runCheck(planPath, f)
	assertExitCode(t, err, 2)
}

func TestRunCheckDebugWritesPromptFile(t *testing.T) {
	planPath := writeTempPlan(t, "# Plan\n")

	// Change to temp dir so debug file goes there
	origDir, _ := os.Getwd()
	tmpDir := t.TempDir()
	os.Chdir(tmpDir)
	defer os.Chdir(origDir)

	f := &checkFlags{
		format:            "json",
		profileName:       "general",
		redactEnabled:     true,
		severityThreshold: "info",
		debug:             true,
		provider:          &llm.MockProvider{Response: validMockResponse()},
	}
	err := runCheck(planPath, f)
	assertExitCode(t, err, 0)

	debugPath := filepath.Join(tmpDir, "plancritic-debug-prompt.txt")
	if _, err := os.Stat(debugPath); os.IsNotExist(err) {
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

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	err := runCheck(planPath, f)
	assertExitCode(t, err, 0)

	// When redact is disabled, the debug prompt should contain the secret
	debugData, err := os.ReadFile(filepath.Join(dir, "plancritic-debug-prompt.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(debugData), "sk-abc123secret") {
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
	err := runCheck(planPath, f)
	assertExitCode(t, err, 0)

	data, _ := os.ReadFile(outPath)
	var rev review.Review
	json.Unmarshal(data, &rev)

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
	err := runCheck(planPath, f)
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
	err := runCheck(planPath, f)
	assertExitCode(t, err, 0)
}

// callCountMockProvider returns different responses on successive calls.
type callCountMockProvider struct {
	responses []string
	callIdx   int
}

func (m *callCountMockProvider) Name() string { return "mock" }

func (m *callCountMockProvider) Generate(_ context.Context, _ string, _ llm.Settings) (string, error) {
	if m.callIdx >= len(m.responses) {
		return "", errors.New("no more mock responses")
	}
	resp := m.responses[m.callIdx]
	m.callIdx++
	return resp, nil
}
