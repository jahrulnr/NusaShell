package application

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"
)

const (
	maxProviderAttempts = 3
	retryBaseDelay      = 250 * time.Millisecond
	retryMaxDelay       = 4 * time.Second
	// retryAfterCutoff is the maximum Retry-After the agent will honor. If a
	// provider advertises a longer reset window (e.g. OpenRouter proxying an
	// upstream with an 81-hour rate-limit reset), the error is not retried —
	// the turn fails immediately so the user sees the error and can retry
	// manually when the rate limit clears.
	retryAfterCutoff = 5 * time.Minute
)

// UpstreamError carries retry metadata from a provider adapter without making
// the application layer depend on a concrete HTTP client.
type UpstreamError struct {
	// Kind classifies the failure mode so operators can distinguish an HTTP
	// status rejection from a connect failure, an SSE transport error, or an
	// idle stream stall — without parsing the error message.
	Kind       UpstreamErrorKind
	StatusCode int
	RetryAfter time.Duration
	Temporary  bool
	Err        error
}

// UpstreamErrorKind discriminates provider failure modes for logging and
// retry decisions. Mirrors the TS AgentProviderHttpError.kind field.
type UpstreamErrorKind string

const (
	// KindHTTPStatus: provider returned a non-2xx HTTP status (429, 5xx, …).
	KindHTTPStatus UpstreamErrorKind = "http_status"
	// KindConnect: transport-level failure before/during the request (DNS,
	// TLS, TCP reset, context deadline at connect time).
	KindConnect UpstreamErrorKind = "connect"
	// KindSSETransport: the SSE stream opened (2xx) but failed mid-stream —
	// either a read error (mid-frame cut) or a missing terminator.
	KindSSETransport UpstreamErrorKind = "sse_transport"
	// KindIdleTimeout: the stream stalled for the configured idle window
	// with no data chunks, indicating a hung provider.
	KindIdleTimeout UpstreamErrorKind = "idle_timeout"
)

func (e *UpstreamError) Error() string {
	if e == nil || e.Err == nil {
		return "provider request failed"
	}
	return e.Err.Error()
}

func (e *UpstreamError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// RetrySleeper makes the backoff wait deterministic in tests while keeping
// the production retry loop cancellation-aware.
type RetrySleeper func(context.Context, time.Duration) error

func sleepForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func providerRetryDelay(err error, retry int) (time.Duration, bool) {
	if !isRetryableProviderError(err) {
		return 0, false
	}
	var upstream *UpstreamError
	_ = errors.As(err, &upstream)

	// If the provider advertises a Retry-After longer than the cutoff, do not
	// retry — the rate-limit window is too long to wait inside a turn. Fail
	// fast so the user sees the error and can retry manually later.
	if upstream != nil && upstream.RetryAfter > retryAfterCutoff {
		return 0, false
	}

	delay := retryBaseDelay
	for i := 1; i < retry && delay < retryMaxDelay; i++ {
		delay *= 2
	}
	if delay > retryMaxDelay {
		delay = retryMaxDelay
	}
	if upstream != nil && upstream.RetryAfter > delay {
		delay = upstream.RetryAfter
	}
	// A small positive jitter keeps concurrent local turns from retrying in
	// lockstep while never retrying earlier than a provider's Retry-After.
	return delay + time.Duration(rand.Int63n(int64(delay/4)+1)), true
}

func isRetryableProviderError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var upstream *UpstreamError
	if !errors.As(err, &upstream) || !isRetryableUpstream(upstream) {
		return false
	}
	return true
}

func isRetryableUpstream(err *UpstreamError) bool {
	if err == nil {
		return false
	}
	body := ""
	if err.Err != nil {
		body = err.Err.Error()
	}
	if err.Temporary {
		// Even temporary errors can be permanent provider failures (e.g.
		// 503 with "insufficient balance" body). Check the message before
		// declaring retryability.
		if isPermanentProviderFailure(err.StatusCode, body) {
			return false
		}
		return true
	}
	if isPermanentProviderFailure(err.StatusCode, body) {
		return false
	}
	switch err.StatusCode {
	case 408, 409, 425:
		return true
	case 429:
		// Only retry 429 if the provider advertised a Retry-After window
		// that fits within our cutoff. Without Retry-After, the rate-limit
		// window is unknown and our exponential backoff (250ms–4s) is far
		// shorter than typical rate-limit windows (1–60 minutes). Retrying
		// just spams the provider with requests that all hit the same
		// rate-limit window, making the limit worse. Fail fast so the user
		// sees the error and can retry manually when the window clears.
		return err.RetryAfter > 0 && err.RetryAfter <= retryAfterCutoff
	default:
		return err.StatusCode >= 500 && err.StatusCode <= 599
	}
}

// permanentFailurePhrases are body substrings that indicate a billing/credit
// failure rather than a transient server issue. Matched case-insensitively.
// Mirrors the TS isPermanentProviderFailure phrase list.
var permanentFailurePhrases = []string{
	"insufficient balance",
	"no resource package",
	"please recharge",
	"payment required",
	"out of credits",
	"credit balance",
	"billing",
	"top up",
	"top-up",
	"topup",
	"account suspended",
	`"code":"1113"`,
	`"code":1113`,
}

// isPermanentProviderFailure reports whether the HTTP status + body indicate
// a billing/credit exhaustion that will not resolve on retry. A 503 with
// "insufficient balance" is permanent; a 503 with "internal server error" is
// not. Status 402 is always permanent.
func isPermanentProviderFailure(status int, body string) bool {
	if status == 402 {
		return true
	}
	normalized := strings.ToLower(body)
	for _, phrase := range permanentFailurePhrases {
		if strings.Contains(normalized, phrase) {
			return true
		}
	}
	return false
}

// describeProviderError renders a provider error for the retry log so
// operators can tell a 429 rate limit from a mid-stream EOF without digging
// through wrapped layers. Non-UpstreamError values pass through as their plain
// Error() string.
func describeProviderError(err error) string {
	if err == nil {
		return ""
	}
	var upstream *UpstreamError
	if !errors.As(err, &upstream) {
		return err.Error()
	}
	parts := []string{upstream.Err.Error()}
	if upstream.Kind != "" {
		parts = append(parts, fmt.Sprintf("kind=%s", upstream.Kind))
	}
	if upstream.StatusCode != 0 {
		parts = append(parts, fmt.Sprintf("status=%d", upstream.StatusCode))
	}
	if upstream.RetryAfter > 0 {
		parts = append(parts, fmt.Sprintf("retry_after=%s", upstream.RetryAfter.Round(time.Second)))
	}
	return strings.Join(parts, " ")
}
