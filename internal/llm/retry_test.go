package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// noSleep replaces retrySleep for the duration of a test and records the
// delays that would have been waited.
func noSleep(t *testing.T) *[]time.Duration {
	t.Helper()
	var delays []time.Duration
	orig := retrySleep
	retrySleep = func(ctx context.Context, d time.Duration) error {
		delays = append(delays, d)
		return ctx.Err()
	}
	t.Cleanup(func() { retrySleep = orig })
	return &delays
}

func TestSendWithRetryRecoversFromTransientStatuses(t *testing.T) {
	noSleep(t)
	statuses := []int{529, 503, 200}
	calls := 0
	res, err := sendWithRetry(context.Background(), "p", nil, func(context.Context) (httpResult, error) {
		st := statuses[calls]
		calls++
		return httpResult{Status: st, Body: []byte("ok")}, nil
	})
	if err != nil || res.Status != 200 {
		t.Fatalf("expected success after retries, got status=%d err=%v", res.Status, err)
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
}

func TestSendWithRetryDoesNotRetryClientErrors(t *testing.T) {
	noSleep(t)
	for _, st := range []int{400, 401, 403, 404, 413, 422} {
		calls := 0
		res, err := sendWithRetry(context.Background(), "p", nil, func(context.Context) (httpResult, error) {
			calls++
			return httpResult{Status: st, Body: []byte("nope")}, nil
		})
		if err != nil {
			t.Errorf("status %d: non-retryable statuses are returned as results, got err %v", st, err)
		}
		if res.Status != st || calls != 1 {
			t.Errorf("status %d: expected 1 call returning the status, got calls=%d status=%d", st, calls, res.Status)
		}
	}
}

func TestSendWithRetryExhaustionNamesAttempts(t *testing.T) {
	delays := noSleep(t)
	calls := 0
	_, err := sendWithRetry(context.Background(), "anthropic", nil, func(context.Context) (httpResult, error) {
		calls++
		return httpResult{Status: 529, Body: []byte(`{"error":"overloaded"}`)}, nil
	})
	if err == nil {
		t.Fatal("expected an error after exhausting retries")
	}
	if calls != retryMaxAttempts {
		t.Errorf("expected %d attempts, got %d", retryMaxAttempts, calls)
	}
	if !strings.Contains(err.Error(), "529") || !strings.Contains(err.Error(), "attempt") || !strings.Contains(err.Error(), "overloaded") {
		t.Errorf("error should name the status, attempts, and body: %v", err)
	}
	if len(*delays) != retryMaxAttempts-1 {
		t.Errorf("expected %d sleeps, got %d", retryMaxAttempts-1, len(*delays))
	}
}

func TestSendWithRetryHonorsRetryAfterAndNotifies(t *testing.T) {
	delays := noSleep(t)
	var notified []string
	calls := 0
	_, err := sendWithRetry(context.Background(), "p", func(attempt int, reason string, delay time.Duration) {
		notified = append(notified, reason)
	}, func(context.Context) (httpResult, error) {
		calls++
		if calls == 1 {
			return httpResult{Status: 429, RetryAfter: "7"}, nil
		}
		return httpResult{Status: 200}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(*delays) != 1 || (*delays)[0] != 7*time.Second {
		t.Errorf("Retry-After: 7 should wait exactly 7s, got %v", *delays)
	}
	if len(notified) != 1 || notified[0] != "HTTP 429" {
		t.Errorf("expected one notification for HTTP 429, got %v", notified)
	}
}

func TestSendWithRetryRetriesTransportErrorsButNotCancellation(t *testing.T) {
	noSleep(t)
	calls := 0
	res, err := sendWithRetry(context.Background(), "p", nil, func(context.Context) (httpResult, error) {
		calls++
		if calls == 1 {
			return httpResult{}, errors.New("connection reset by peer")
		}
		return httpResult{Status: 200}, nil
	})
	if err != nil || res.Status != 200 || calls != 2 {
		t.Errorf("a transport error should be retried once: calls=%d status=%d err=%v", calls, res.Status, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls = 0
	_, err = sendWithRetry(ctx, "p", nil, func(ctx context.Context) (httpResult, error) {
		calls++
		return httpResult{}, ctx.Err()
	})
	if err == nil || calls != 1 {
		t.Errorf("a canceled context must not be retried: calls=%d err=%v", calls, err)
	}
}

func TestBackoffDelay(t *testing.T) {
	for attempt, base := range map[int]time.Duration{1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second} {
		for i := 0; i < 20; i++ {
			d := backoffDelay(attempt, "")
			if d < base || d > base+base/4 {
				t.Errorf("attempt %d: delay %v outside [%v, %v]", attempt, d, base, base+base/4)
			}
		}
	}
	if d := backoffDelay(1, "3"); d != 3*time.Second {
		t.Errorf("Retry-After seconds: got %v", d)
	}
	if d := backoffDelay(1, "600"); d != retryMaxDelay {
		t.Errorf("Retry-After above the cap should be capped: got %v", d)
	}
	future := time.Now().Add(5 * time.Second).UTC().Format(http.TimeFormat)
	if d := backoffDelay(1, future); d < 3*time.Second || d > 5*time.Second {
		t.Errorf("Retry-After HTTP date: got %v", d)
	}
	past := time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)
	if d := backoffDelay(1, past); d != 0 {
		t.Errorf("Retry-After in the past should be 0: got %v", d)
	}
	if d := backoffDelay(10, ""); d > retryMaxDelay {
		t.Errorf("exponential backoff should be capped: got %v", d)
	}
}

func TestProvidersRetryOverloadedThenSucceed(t *testing.T) {
	noSleep(t)
	t.Run("anthropic", func(t *testing.T) {
		var n atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if n.Add(1) == 1 {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(529)
				_, _ = w.Write([]byte(`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`))
				return
			}
			_ = json.NewEncoder(w).Encode(anthropicResponse{Content: []anthropicContentBlock{{Type: "text", Text: "{}"}}, StopReason: "end_turn"})
		}))
		defer srv.Close()
		p := &AnthropicProvider{apiKey: "k", apiURL: srv.URL, client: srv.Client()}
		out, _, err := p.Generate(context.Background(), "hi", Settings{Model: "claude-sonnet-4-6"})
		if err != nil || out != "{}" || n.Load() != 2 {
			t.Errorf("expected success on second attempt: out=%q err=%v calls=%d", out, err, n.Load())
		}
	})
	t.Run("openai", func(t *testing.T) {
		var n atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if n.Add(1) == 1 {
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			_ = json.NewEncoder(w).Encode(openaiResponse{Choices: []openaiChoice{{Message: openaiMessage{Content: "{}"}, FinishReason: "stop"}}})
		}))
		defer srv.Close()
		p := &OpenAIProvider{apiKey: "k", apiURL: srv.URL, client: srv.Client()}
		if _, _, err := p.Generate(context.Background(), "hi", Settings{}); err != nil || n.Load() != 2 {
			t.Errorf("expected success on second attempt: err=%v calls=%d", err, n.Load())
		}
	})
	t.Run("gemini", func(t *testing.T) {
		var n atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if n.Add(1) == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_ = json.NewEncoder(w).Encode(geminiResponse{Candidates: []geminiCandidate{{Content: geminiContent{Parts: []geminiPart{{Text: "{}"}}}, FinishReason: "STOP"}}})
		}))
		defer srv.Close()
		p := &GeminiProvider{apiKey: "k", apiURL: srv.URL, client: srv.Client()}
		if _, _, err := p.Generate(context.Background(), "hi", Settings{}); err != nil || n.Load() != 2 {
			t.Errorf("expected success on second attempt: err=%v calls=%d", err, n.Load())
		}
	})
}

func TestProviderFeatureFallbackStillWorksWithRetries(t *testing.T) {
	// A 400 naming output_config must still trigger the feature fallback,
	// not a retry of the identical request.
	noSleep(t)
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		if b["output_config"] != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"output_config: not supported"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(anthropicResponse{Content: []anthropicContentBlock{{Type: "text", Text: "{}"}}, StopReason: "end_turn"})
	}))
	defer srv.Close()
	p := &AnthropicProvider{apiKey: "k", apiURL: srv.URL, client: srv.Client()}
	if _, _, err := p.Generate(context.Background(), "hi", Settings{Model: "claude-sonnet-4-6", OutputSchema: json.RawMessage(testSchema)}); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || bodies[1]["output_config"] != nil {
		t.Errorf("expected exactly one fallback request without output_config, got %d requests", len(bodies))
	}
}

func TestProviderClientsHaveNoFixedTimeout(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "k")
	t.Setenv("OPENAI_API_KEY", "k")
	t.Setenv("GEMINI_API_KEY", "k")
	a, _ := NewAnthropic()
	o, _ := NewOpenAI()
	g, _ := NewGemini()
	for name, c := range map[string]*http.Client{"anthropic": a.client, "openai": o.client, "gemini": g.client} {
		if c.Timeout != 0 {
			t.Errorf("%s: client must not carry a fixed timeout (it silently capped --timeout), got %v", name, c.Timeout)
		}
	}
}

func TestSendWithRetryCancellationDuringBackoffIsDetectable(t *testing.T) {
	orig := retrySleep
	ctx, cancel := context.WithCancel(context.Background())
	retrySleep = func(ctx context.Context, d time.Duration) error {
		cancel() // the caller gives up while we are waiting to retry
		return ctx.Err()
	}
	t.Cleanup(func() { retrySleep = orig })

	_, err := sendWithRetry(ctx, "p", nil, func(context.Context) (httpResult, error) {
		return httpResult{Status: 503, Body: []byte("down")}, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("cancellation during backoff should be detectable with errors.Is, got %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Errorf("error should still mention the failure being retried, got %v", err)
	}
}

func TestBackoffDelayHugeRetryAfterIsCapped(t *testing.T) {
	for _, ra := range []string{"10000000000", "9223372036854775807", "99999999999999999999999", "31"} {
		if d := backoffDelay(1, ra); d != retryMaxDelay {
			t.Errorf("Retry-After %s should be capped at %v, got %v", ra, retryMaxDelay, d)
		}
	}
}
