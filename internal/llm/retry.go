package llm

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Transient provider failures (rate limits, overload, 5xx, dropped
// connections) are routine in agent loops, and failing the run on the
// first one throws away an already-built prompt and forces the agent to
// start over. Every provider sends its request through sendWithRetry.
const (
	// retryMaxAttempts counts the first try: one request plus three retries.
	retryMaxAttempts = 4
	retryBaseDelay   = 1 * time.Second
	retryMaxDelay    = 30 * time.Second
)

// httpResult is one provider response. A non-200 status is data, not an
// error, so callers can inspect the body (e.g. for a rejected feature).
type httpResult struct {
	Status     int
	Body       []byte
	RetryAfter string
}

// RetryNotify is called before each retry. attempt is the 1-based
// number of the request that just failed.
type RetryNotify func(attempt int, reason string, delay time.Duration)

// retrySleep waits for d or until ctx is done. Tests replace it.
var retrySleep = func(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// retryableStatus reports whether a response status is worth retrying.
// 4xx validation errors are not: the same request will fail the same way.
func retryableStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooManyRequests,
		http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout,
		529: // Anthropic "overloaded"
		return true
	}
	return false
}

// sendWithRetry calls send until it returns a non-retryable result or
// the attempts are exhausted. Transport errors are retried unless the
// context is done. A retryable status that persists through the last
// attempt becomes an error naming the attempt count; any other non-200
// status is returned as a result for the caller to interpret.
func sendWithRetry(ctx context.Context, provider string, notify RetryNotify, send func(context.Context) (httpResult, error)) (httpResult, error) {
	var lastErr error
	for attempt := 1; ; attempt++ {
		res, err := send(ctx)
		if err == nil && !retryableStatus(res.Status) {
			return res, nil
		}

		var reason string
		if err != nil {
			if ctx.Err() != nil {
				return httpResult{}, err
			}
			lastErr = err
			reason = err.Error()
		} else {
			lastErr = fmt.Errorf("%s: API returned %d after %d attempt(s): %s", provider, res.Status, attempt, strings.TrimSpace(string(res.Body)))
			reason = fmt.Sprintf("HTTP %d", res.Status)
		}
		if attempt >= retryMaxAttempts {
			return httpResult{}, lastErr
		}

		delay := backoffDelay(attempt, res.RetryAfter)
		if notify != nil {
			notify(attempt, reason, delay)
		}
		if err := retrySleep(ctx, delay); err != nil {
			// Surface the context error (detectable with errors.Is) and
			// keep the provider failure that was being retried for context.
			return httpResult{}, fmt.Errorf("%s: %w while retrying after: %v", provider, err, lastErr)
		}
	}
}

// backoffDelay returns how long to wait after the given 1-based failed
// attempt. A Retry-After header (seconds or HTTP date) wins when
// present; otherwise exponential backoff from retryBaseDelay with up to
// 25% jitter. The result never exceeds retryMaxDelay.
func backoffDelay(attempt int, retryAfter string) time.Duration {
	if ra := strings.TrimSpace(retryAfter); ra != "" {
		// A syntactically valid decimal that does not fit in 64 bits
		// (ErrRange) is simply "longer than the cap". Compare before
		// converting so no value can overflow time.Duration into a
		// negative (immediate) wait, on 32- or 64-bit platforms alike.
		if secs, err := strconv.ParseUint(ra, 10, 64); err == nil || errors.Is(err, strconv.ErrRange) {
			if err != nil || secs >= uint64(retryMaxDelay/time.Second) {
				return retryMaxDelay
			}
			return time.Duration(secs) * time.Second
		}
		if at, err := http.ParseTime(ra); err == nil {
			return min(max(time.Until(at), 0), retryMaxDelay)
		}
	}
	if attempt < 1 {
		attempt = 1
	}
	d := retryBaseDelay << (attempt - 1)
	if d > retryMaxDelay {
		d = retryMaxDelay
	}
	jitter := time.Duration(rand.Int64N(int64(d/4) + 1))
	return min(d+jitter, retryMaxDelay)
}
