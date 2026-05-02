package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dshills/plancritic/internal/llm"
	"github.com/dshills/plancritic/internal/review"
	"github.com/dshills/plancritic/internal/reviewer"
)

func TestServeIndexRendersForm(t *testing.T) {
	srv := &webServer{
		base: reviewer.Options{
			ProfileName:       "general",
			ProviderName:      "openai",
			Model:             "gpt-test",
			SeverityThreshold: "warn",
			RedactEnabled:     true,
			MaxIssues:         10,
			MaxQuestions:      5,
		},
		runner: reviewer.Run,
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	for _, want := range []string{"PlanCritic", `href="/favicon.svg"`, `hx-post="/check"`, `data-review-form`, `data-check-button`, "pending-status", "Checking...", `fetch("/models?provider="`, `id="model_options"`, `name="plan_path"`, `data-open-plan`, `name="plan"`, "gpt-test"} {
		if !strings.Contains(body, want) {
			t.Fatalf("index body missing %q", want)
		}
	}
}

func TestServeFavicon(t *testing.T) {
	srv := &webServer{runner: reviewer.Run}
	req := httptest.NewRequest(http.MethodGet, "/favicon.svg", nil)
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != "image/svg+xml" {
		t.Fatalf("Content-Type = %q, want image/svg+xml", got)
	}
	if !strings.Contains(rec.Body.String(), "<svg") {
		t.Fatalf("favicon body missing svg: %q", rec.Body.String())
	}
}

func TestDefaultLocalPlanPathPrefersRootPlan(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "specs"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "specs", "PLAN.md"), []byte("# Specs Plan\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "PLAN.md"), []byte("# Root Plan\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := defaultLocalPlanPath(dir); got != "PLAN.md" {
		t.Fatalf("defaultLocalPlanPath = %q, want PLAN.md", got)
	}
}

func TestDefaultLocalPlanPathFallsBackToSpecsPlan(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "specs"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "specs", "PLAN.md"), []byte("# Specs Plan\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := defaultLocalPlanPath(dir); got != filepath.Join("specs", "PLAN.md") {
		t.Fatalf("defaultLocalPlanPath = %q, want specs/PLAN.md", got)
	}
}

func TestServeModelsListsProviderModels(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q, want Bearer test-key", got)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-5.2"},{"id":"text-embedding-3-large"}]}`))
	}))
	defer upstream.Close()
	originalURL := llm.OpenAIModelsAPIURLForTest()
	llm.SetOpenAIModelsAPIURL(upstream.URL)
	t.Cleanup(func() { llm.SetOpenAIModelsAPIURL(originalURL) })
	t.Setenv("OPENAI_API_KEY", "test-key")

	srv := &webServer{base: reviewer.Options{ProviderName: "openai"}, runner: reviewer.Run}
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/models?provider=openai", nil)
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var payload modelsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Provider != "openai" || len(payload.Models) != 1 || payload.Models[0].ID != "gpt-5.2" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestServeCheckRunsReviewAndRendersResult(t *testing.T) {
	var gotPlan string
	var gotFlags reviewer.Options
	srv := &webServer{
		base: reviewer.Options{
			Timeout:           "5m",
			ProfileName:       "general",
			ProviderName:      "openai",
			Model:             "gpt-test",
			SeverityThreshold: "info",
			RedactEnabled:     true,
			MaxIssues:         50,
			MaxQuestions:      20,
		},
		runner: func(_ context.Context, planPath string, f reviewer.Options, _ string) (review.Review, error) {
			gotPlan = planPath
			gotFlags = f
			return review.Review{
				Tool: "plancritic",
				Input: review.Input{
					PlanFile: "plan.md",
					Profile:  f.ProfileName,
					Strict:   f.Strict,
				},
				Summary: review.Summary{
					Verdict:       review.VerdictNotExecutable,
					Score:         71,
					CriticalCount: 1,
					WarnCount:     1,
				},
				Issues: []review.Issue{
					{
						ID:             "ISSUE-0001",
						Severity:       review.SeverityCritical,
						Category:       review.CategoryOrderingDependency,
						Title:          "Missing migration order",
						Description:    "The plan references migration work without ordering it before the rollout.",
						Impact:         "The deployment can apply code before data is ready.",
						Recommendation: "Move migration steps before rollout steps.",
						Evidence:       []review.Evidence{{Source: "plan", Path: "plan-plan.md", LineStart: 2, LineEnd: 2, Quote: "Do the migration"}},
					},
				},
				Questions: []review.Question{
					{
						ID:        "Q-0001",
						Severity:  review.SeverityWarn,
						Question:  "Which database?",
						WhyNeeded: "The implementation depends on the target database.",
						Evidence:  []review.Evidence{{Source: "plan", Path: "plan.md", LineStart: 2, LineEnd: 2, Quote: "Do the migration"}},
					},
				},
				Meta: review.Meta{Model: "mock/gpt-test"},
			}, nil
		},
	}

	nonce := issueNonce(t, srv)
	body, contentType := multipartBody(t, map[string]string{
		"profile":       "go-backend",
		"form_nonce":    nonce,
		"provider":      "openai",
		"model":         "gpt-test",
		"severity":      "warn",
		"strict":        "on",
		"redact":        "on",
		"max_issues":    "12",
		"max_questions": "7",
	}, map[string]string{
		"plan": "# Plan\nDo the migration\n",
	})

	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/check", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Origin", "http://127.0.0.1")
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if gotPlan == "" {
		t.Fatal("runner was not called")
	}
	if gotFlags.ProfileName != "go-backend" || !gotFlags.Strict || gotFlags.MaxIssues != 12 || gotFlags.MaxQuestions != 7 {
		t.Fatalf("unexpected flags: %+v", gotFlags)
	}
	for _, want := range []string{"Completed in", "NOT_EXECUTABLE", "Missing migration order", "Which database?", "Do the migration", `data-modal-target="modal-issue-ISSUE-0001-`, `class="line-badge CRITICAL"`, "Move migration steps before rollout steps.", "The implementation depends on the target database."} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("result body missing %q", want)
		}
	}
}

func TestServeCheckUsesLocalPlanPath(t *testing.T) {
	dir := t.TempDir()
	planPath := filepath.Join(dir, "PLAN.md")
	if err := os.WriteFile(planPath, []byte("# Plan\nUse local path\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var gotPlan string
	srv := &webServer{
		localRoot: dir,
		base: reviewer.Options{
			ProfileName:       "general",
			SeverityThreshold: "info",
			RedactEnabled:     true,
		},
		runner: func(_ context.Context, planPath string, f reviewer.Options, _ string) (review.Review, error) {
			gotPlan = planPath
			return review.Review{
				Input:   review.Input{Profile: f.ProfileName},
				Summary: review.Summary{Verdict: review.VerdictExecutable, Score: 100},
				Meta:    review.Meta{Model: "mock/default"},
			}, nil
		},
	}

	nonce := issueNonce(t, srv)
	body, contentType := multipartBody(t, map[string]string{
		"profile":    "general",
		"form_nonce": nonce,
		"plan_path":  "PLAN.md",
	}, nil)
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/check", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Origin", "http://127.0.0.1")
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	wantPlan, err := filepath.EvalSymlinks(planPath)
	if err != nil {
		t.Fatal(err)
	}
	if gotPlan != wantPlan {
		t.Fatalf("plan path = %q, want %q", gotPlan, wantPlan)
	}
}

func TestServeCheckPrefersUploadedPlanOverDefaultLocalPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "PLAN.md"), []byte("# Plan\nUse local path\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var gotPlanContent string
	srv := &webServer{
		localRoot: dir,
		base: reviewer.Options{
			ProfileName:       "general",
			SeverityThreshold: "info",
			RedactEnabled:     true,
		},
		runner: func(_ context.Context, planPath string, f reviewer.Options, _ string) (review.Review, error) {
			content, err := os.ReadFile(planPath)
			if err != nil {
				t.Fatal(err)
			}
			gotPlanContent = string(content)
			return review.Review{
				Input:   review.Input{Profile: f.ProfileName},
				Summary: review.Summary{Verdict: review.VerdictExecutable, Score: 100},
				Meta:    review.Meta{Model: "mock/default"},
			}, nil
		},
	}

	nonce := issueNonce(t, srv)
	body, contentType := multipartBody(t, map[string]string{
		"profile":    "general",
		"form_nonce": nonce,
		"plan_path":  "PLAN.md",
	}, map[string]string{
		"plan": "# Uploaded\nUse uploaded file\n",
	})
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/check", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Origin", "http://127.0.0.1")
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(gotPlanContent, "Use uploaded file") {
		t.Fatalf("runner used wrong plan content: %q", gotPlanContent)
	}
}

func TestServeCheckIssuesReplacementNonceForOpenPlan(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "PLAN.md"), []byte("# Plan\n"), 0600); err != nil {
		t.Fatal(err)
	}
	opened := false
	srv := &webServer{
		localRoot: dir,
		base: reviewer.Options{
			ProfileName:       "general",
			SeverityThreshold: "info",
			RedactEnabled:     true,
		},
		runner: func(context.Context, string, reviewer.Options, string) (review.Review, error) {
			return review.Review{
				Input:   review.Input{Profile: "general"},
				Summary: review.Summary{Verdict: review.VerdictExecutable, Score: 100},
				Meta:    review.Meta{Model: "mock/default"},
			}, nil
		},
		openEditor: func(string, string) error {
			opened = true
			return nil
		},
	}
	nonce := issueNonce(t, srv)
	checkBody, checkContentType := multipartBody(t, map[string]string{
		"profile":    "general",
		"form_nonce": nonce,
		"plan_path":  "PLAN.md",
	}, nil)
	checkReq := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/check", checkBody)
	checkReq.Header.Set("Content-Type", checkContentType)
	checkReq.Header.Set("Origin", "http://127.0.0.1")
	checkRec := httptest.NewRecorder()
	srv.routes().ServeHTTP(checkRec, checkReq)
	if checkRec.Code != http.StatusOK {
		t.Fatalf("check status = %d, want %d: %s", checkRec.Code, http.StatusOK, checkRec.Body.String())
	}
	nextNonce := extractFormNonce(t, checkRec.Body.String())
	if nextNonce == nonce {
		t.Fatal("check response reused consumed nonce")
	}

	form := url.Values{"form_nonce": {nextNonce}, "plan_path": {"PLAN.md"}, "editor": {"default"}}
	openReq := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/open-plan", strings.NewReader(form.Encode()))
	openReq.RemoteAddr = "127.0.0.1:12345"
	openReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	openReq.Header.Set("Origin", "http://127.0.0.1")
	openRec := httptest.NewRecorder()
	srv.routes().ServeHTTP(openRec, openReq)
	if openRec.Code != http.StatusOK {
		t.Fatalf("open status = %d, want %d: %s", openRec.Code, http.StatusOK, openRec.Body.String())
	}
	if !opened {
		t.Fatal("openEditor was not called")
	}
}

func TestServeOpenPlanOpensEditor(t *testing.T) {
	dir := t.TempDir()
	planPath := filepath.Join(dir, "PLAN.md")
	if err := os.WriteFile(planPath, []byte("# Plan\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var gotEditor, gotPath string
	srv := &webServer{
		localRoot: dir,
		runner:    reviewer.Run,
		openEditor: func(editor, path string) error {
			gotEditor = editor
			gotPath = path
			return nil
		},
	}
	nonce := issueNonce(t, srv)
	form := url.Values{
		"form_nonce": {nonce},
		"plan_path":  {"PLAN.md"},
		"editor":     {"vscode"},
	}
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/open-plan", strings.NewReader(form.Encode()))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://127.0.0.1")
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	wantPlan, err := filepath.EvalSymlinks(planPath)
	if err != nil {
		t.Fatal(err)
	}
	if gotEditor != "vscode" || gotPath != wantPlan {
		t.Fatalf("open editor got editor=%q path=%q, want vscode %q", gotEditor, gotPath, wantPlan)
	}
	if strings.Contains(rec.Body.String(), wantPlan) {
		t.Fatalf("response leaked resolved plan path: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"status":"opened"`) {
		t.Fatalf("response missing opened status: %q", rec.Body.String())
	}
}

func TestServeOpenPlanRejectsPathOutsideWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	planPath := filepath.Join(outside, "PLAN.md")
	if err := os.WriteFile(planPath, []byte("# Plan\n"), 0600); err != nil {
		t.Fatal(err)
	}
	srv := &webServer{localRoot: root, runner: reviewer.Run, openEditor: func(string, string) error {
		t.Fatal("openEditor should not be called")
		return nil
	}}
	nonce := issueNonce(t, srv)
	form := url.Values{
		"form_nonce": {nonce},
		"plan_path":  {planPath},
		"editor":     {"default"},
	}
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/open-plan", strings.NewReader(form.Encode()))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://127.0.0.1")
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if strings.Contains(rec.Body.String(), planPath) {
		t.Fatalf("response leaked rejected plan path: %s", rec.Body.String())
	}
}

func TestServeOpenPlanRejectsNonLoopbackClient(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "PLAN.md"), []byte("# Plan\n"), 0600); err != nil {
		t.Fatal(err)
	}
	srv := &webServer{localRoot: dir, runner: reviewer.Run, openEditor: func(string, string) error {
		t.Fatal("openEditor should not be called")
		return nil
	}}
	nonce := issueNonce(t, srv)
	form := url.Values{
		"form_nonce": {nonce},
		"plan_path":  {"PLAN.md"},
		"editor":     {"default"},
	}
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/open-plan", strings.NewReader(form.Encode()))
	req.RemoteAddr = "192.0.2.10:54321"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://127.0.0.1")
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestServeCheckRequiresPlanUpload(t *testing.T) {
	srv := &webServer{
		base:   reviewer.Options{ProfileName: "general"},
		runner: reviewer.Run,
	}

	nonce := issueNonce(t, srv)
	body, contentType := multipartBody(t, map[string]string{"profile": "general", "form_nonce": nonce}, nil)
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/check", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Origin", "http://127.0.0.1")
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "Missing plan file or local plan path") {
		t.Fatalf("expected missing plan error, got %q", rec.Body.String())
	}
}

func TestServeCheckRejectsMissingFormNonce(t *testing.T) {
	srv := &webServer{
		base:   reviewer.Options{ProfileName: "general"},
		runner: reviewer.Run,
	}

	body, contentType := multipartBody(t, map[string]string{"profile": "general"}, map[string]string{
		"plan": "# Plan\n",
	})
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/check", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Origin", "http://127.0.0.1")
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if !strings.Contains(rec.Body.String(), "form expired") {
		t.Fatalf("expected form nonce error, got %q", rec.Body.String())
	}
}

func TestServeCheckAcceptsIssuedNonceWithoutCookie(t *testing.T) {
	var called bool
	srv := &webServer{
		base: reviewer.Options{
			Timeout:           "5m",
			ProfileName:       "general",
			SeverityThreshold: "info",
			RedactEnabled:     true,
		},
		runner: func(_ context.Context, _ string, _ reviewer.Options, _ string) (review.Review, error) {
			called = true
			return review.Review{
				Input:   review.Input{Profile: "general"},
				Summary: review.Summary{Verdict: review.VerdictExecutable, Score: 100},
				Meta:    review.Meta{Model: "mock/default"},
			}, nil
		},
	}
	nonce := issueNonce(t, srv)
	body, contentType := multipartBody(t, map[string]string{"profile": "general", "form_nonce": nonce}, map[string]string{
		"plan": "# Plan\n",
	})
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/check", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Origin", "http://127.0.0.1")
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !called {
		t.Fatal("runner was not called")
	}
}

func TestServeCheckRejectsCrossOriginPost(t *testing.T) {
	srv := &webServer{
		base:   reviewer.Options{ProfileName: "general"},
		runner: reviewer.Run,
	}

	nonce := issueNonce(t, srv)
	body, contentType := multipartBody(t, map[string]string{"profile": "general", "form_nonce": nonce}, map[string]string{
		"plan": "# Plan\n",
	})
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/check", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Origin", "http://evil.example")
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if !strings.Contains(rec.Body.String(), "Cross-origin") {
		t.Fatalf("expected cross-origin error, got %q", rec.Body.String())
	}
}

func multipartBody(t *testing.T, fields map[string]string, files map[string]string) (io.Reader, string) {
	t.Helper()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	for key, value := range fields {
		if err := w.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	for field, content := range files {
		part, err := w.CreateFormFile(field, field+".md")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &b, w.FormDataContentType()
}

func issueNonce(t *testing.T, srv *webServer) string {
	t.Helper()
	nonce, err := srv.issueFormNonce()
	if err != nil {
		t.Fatal(err)
	}
	return nonce
}

func extractFormNonce(t *testing.T, body string) string {
	t.Helper()
	marker := `id="form_nonce"`
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("body missing form nonce: %s", body)
	}
	rest := body[i:]
	valueMarker := `value="`
	j := strings.Index(rest, valueMarker)
	if j < 0 {
		t.Fatalf("form nonce missing value: %s", body)
	}
	rest = rest[j+len(valueMarker):]
	k := strings.Index(rest, `"`)
	if k < 0 {
		t.Fatalf("form nonce value not terminated: %s", body)
	}
	return rest[:k]
}
