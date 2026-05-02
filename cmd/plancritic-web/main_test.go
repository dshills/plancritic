package main

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	for _, want := range []string{"PlanCritic", `href="/favicon.svg"`, `hx-post="/check"`, `data-review-form`, `data-check-button`, "pending-status", "Checking...", `name="plan"`, "gpt-test"} {
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
						Evidence:       []review.Evidence{{Source: "plan", Path: "plan.md", LineStart: 2, LineEnd: 2, Quote: "Do the migration"}},
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
	if !strings.Contains(rec.Body.String(), "Missing plan file") {
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
