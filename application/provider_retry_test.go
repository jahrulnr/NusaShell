package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"nusashell/domain"
)

func TestProviderRetryDelayHonorsRetryAfter(t *testing.T) {
	delay, retryable := providerRetryDelay(&domain.ProviderError{
		StatusCode: 429,
		RetryAfter: 3 * time.Second,
		Err:        errors.New("rate limited"),
	}, 1)
	if !retryable {
		t.Fatal("rate limit must be retryable")
	}
	if delay < 3*time.Second {
		t.Fatalf("retry delay = %s, want at least Retry-After", delay)
	}
}

func TestIsRetryableProviderError(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		retryable bool
	}{
		{name: "request timeout", err: &domain.ProviderError{StatusCode: 408}, retryable: true},
		{name: "conflict", err: &domain.ProviderError{StatusCode: 409}, retryable: true},
		{name: "too early", err: &domain.ProviderError{StatusCode: 425}, retryable: true},
		{name: "rate limited with Retry-After", err: &domain.ProviderError{StatusCode: 429, RetryAfter: 3 * time.Second}, retryable: true},
		{name: "rate limited without Retry-After", err: &domain.ProviderError{StatusCode: 429}, retryable: false},
		{name: "server error", err: &domain.ProviderError{StatusCode: 503}, retryable: true},
		{name: "temporary transport error", err: &domain.ProviderError{Temporary: true}, retryable: true},
		{name: "invalid request", err: &domain.ProviderError{StatusCode: 400}, retryable: false},
		{name: "unauthorized", err: &domain.ProviderError{StatusCode: 401}, retryable: false},
		{name: "forbidden", err: &domain.ProviderError{StatusCode: 403}, retryable: false},
		{name: "not found", err: &domain.ProviderError{StatusCode: 404}, retryable: false},
		{name: "cancelled", err: &domain.ProviderError{Temporary: true, Err: context.Canceled}, retryable: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRetryableProviderError(tt.err); got != tt.retryable {
				t.Fatalf("isRetryableProviderError(%v) = %t, want %t", tt.err, got, tt.retryable)
			}
		})
	}
}

// TestProviderRetryDelayRejectsLongRetryAfter verifies that a 429 with a
// Retry-After exceeding the cutoff (e.g. OpenRouter proxying an upstream with
// an 81-hour rate-limit reset) is NOT retried — the turn should fail fast so
// the user sees the error instead of waiting hours inside a retry sleep.
func TestProviderRetryDelayRejectsLongRetryAfter(t *testing.T) {
	delay, retryable := providerRetryDelay(&domain.ProviderError{
		StatusCode: 429,
		RetryAfter: 81 * time.Hour,
		Err:        errors.New("rate limited (reset after 81h 38m 41s)"),
	}, 1)
	if retryable {
		t.Fatalf("expected retryable=false for RetryAfter=81h, got delay=%s", delay)
	}
	if delay != 0 {
		t.Fatalf("expected delay=0 for non-retryable, got %s", delay)
	}
}

// TestProviderRetryDelayAcceptsShortRetryAfter verifies that a 429 with a
// Retry-After within the cutoff is still retried with the provider's delay.
func TestProviderRetryDelayAcceptsShortRetryAfter(t *testing.T) {
	delay, retryable := providerRetryDelay(&domain.ProviderError{
		StatusCode: 429,
		RetryAfter: 30 * time.Second,
		Err:        errors.New("rate limited"),
	}, 1)
	if !retryable {
		t.Fatal("expected retryable=true for RetryAfter=30s (within cutoff)")
	}
	if delay < 30*time.Second {
		t.Fatalf("retry delay = %s, want at least Retry-After (30s)", delay)
	}
}

// TestProviderRetryDelayAtCutoffBoundary verifies the boundary behavior:
// exactly at the cutoff is retryable, just above is not.
func TestProviderRetryDelayAtCutoffBoundary(t *testing.T) {
	// Exactly at cutoff — retryable
	_, retryable := providerRetryDelay(&domain.ProviderError{
		StatusCode: 429,
		RetryAfter: domain.RetryAfterCutoff,
		Err:        errors.New("rate limited"),
	}, 1)
	if !retryable {
		t.Fatalf("expected retryable=true at cutoff (%s)", domain.RetryAfterCutoff)
	}

	// Just above cutoff — not retryable
	_, retryable = providerRetryDelay(&domain.ProviderError{
		StatusCode: 429,
		RetryAfter: domain.RetryAfterCutoff + 1*time.Second,
		Err:        errors.New("rate limited"),
	}, 1)
	if retryable {
		t.Fatalf("expected retryable=false just above cutoff (%s+1s)", domain.RetryAfterCutoff)
	}
}

// TestDescribeProviderError verifies that the retry log helper surfaces
// ProviderError metadata (status code, Retry-After) alongside the underlying
// message, and falls back to the plain error string for non-Provider errors.
// This is what lets operators tell a 429 rate limit from a mid-stream EOF in
// the retry log line.
func TestDescribeProviderError(t *testing.T) {
	t.Run("non-upstream passthrough", func(t *testing.T) {
		src := errors.New("boom")
		if got := describeProviderError(src); got != "boom" {
			t.Fatalf("describeProviderError(non-upstream) = %q, want %q", got, "boom")
		}
	})

	t.Run("bare temporary EOF", func(t *testing.T) {
		err := &domain.ProviderError{Kind: domain.KindSSETransport, Temporary: true, Err: io.ErrUnexpectedEOF}
		got := describeProviderError(err)
		if !strings.Contains(got, "unexpected EOF") {
			t.Fatalf("describeProviderError must include underlying message, got %q", got)
		}
		if !strings.Contains(got, "kind=sse_transport") {
			t.Fatalf("describeProviderError must include kind, got %q", got)
		}
		if strings.Contains(got, "status=") || strings.Contains(got, "retry_after=") {
			t.Fatalf("bare temporary error must not invent metadata, got %q", got)
		}
	})

	t.Run("rate limit with status and retry-after", func(t *testing.T) {
		err := &domain.ProviderError{
			Kind:       domain.KindHTTPStatus,
			StatusCode: 429,
			RetryAfter: 30 * time.Second,
			Err:        errors.New("rate limited"),
		}
		got := describeProviderError(err)
		for _, want := range []string{"rate limited", "kind=http_status", "status=429", "retry_after=30s"} {
			if !strings.Contains(got, want) {
				t.Fatalf("describeProviderError missing %q in %q", want, got)
			}
		}
	})

	t.Run("server error with status only", func(t *testing.T) {
		err := &domain.ProviderError{
			Kind:       domain.KindHTTPStatus,
			StatusCode: 503,
			Err:        errors.New("upstream down"),
		}
		got := describeProviderError(err)
		if !strings.Contains(got, "status=503") {
			t.Fatalf("describeProviderError missing status=503 in %q", got)
		}
		if strings.Contains(got, "retry_after=") {
			t.Fatalf("describeProviderError must not add retry_after when absent, got %q", got)
		}
	})
}

// TestIsPermanentProviderFailure verifies that billing/credit errors are
// classified as permanent failures and NOT retried, even when the HTTP status
// is in the transient set (e.g. 503 with "out of credits" body). This prevents
// 3x wasted retry attempts on an account that has exhausted its balance.
func TestIsPermanentProviderFailure(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{name: "402 payment required", status: 402, body: "Payment Required", want: true},
		{name: "503 insufficient balance", status: 503, body: "insufficient balance", want: true},
		{name: "503 out of credits", status: 503, body: "out of credits", want: true},
		{name: "429 please recharge", status: 429, body: "please recharge your account", want: true},
		{name: "503 top up", status: 503, body: "please top up your balance", want: true},
		{name: "503 top-up hyphenated", status: 503, body: "top-up required", want: true},
		{name: "503 topup no hyphen", status: 503, body: "topup now", want: true},
		{name: "503 account suspended", status: 503, body: "account suspended", want: true},
		{name: "503 code 1113 string", status: 503, body: `{"code":"1113"}`, want: true},
		{name: "503 code 1113 number", status: 503, body: `{"code":1113}`, want: true},
		{name: "503 no resource package", status: 503, body: "no resource package", want: true},
		{name: "503 credit balance", status: 503, body: "credit balance is zero", want: true},
		{name: "503 billing", status: 503, body: "billing issue", want: true},
		{name: "503 generic server error", status: 503, body: "internal server error", want: false},
		{name: "429 generic rate limit", status: 429, body: "rate limit exceeded", want: false},
		{name: "502 bad gateway", status: 502, body: "bad gateway", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := domain.IsPermanentProviderFailure(tt.status, tt.body); got != tt.want {
				t.Fatalf("domain.IsPermanentProviderFailure(%d, %q) = %t, want %t", tt.status, tt.body, got, tt.want)
			}
		})
	}
}

// TestIsRetryableProviderErrorRejectsPermanentFailure verifies that a 503 with
// a billing body is NOT retryable, even though 503 is in the transient status
// set. This is the integration point between isPermanentProviderFailure and
// domain.CanAutoRetry.
func TestShouldEmergencyCompact(t *testing.T) {
	overflow := &domain.ProviderError{
		Kind:       domain.KindHTTPStatus,
		StatusCode: 400,
		Err:        errors.New(`provider returned HTTP 400: Requested token count exceeds the model's maximum context length of 262144 tokens. You requested a total of 267042 tokens.`),
	}
	notOverflow := &domain.ProviderError{
		Kind:       domain.KindHTTPStatus,
		StatusCode: 400,
		Err:        errors.New("provider returned HTTP 400: unsupported parameter"),
	}

	if !shouldEmergencyCompact(overflow, 200_000, 150_000) {
		t.Error("expected emergency compact when estimate exceeds trigger")
	}
	// Even with a low heuristic estimate, an explicit context limit forces compaction.
	if !shouldEmergencyCompact(overflow, 100_000, 150_000) {
		t.Error("expected emergency compact with explicit context limit despite low estimate")
	}
	if shouldEmergencyCompact(notOverflow, 200_000, 150_000) {
		t.Error("unexpected emergency compact for non-overflow 400")
	}
}

func TestContextLimitFromError(t *testing.T) {
	overflow := &domain.ProviderError{
		Kind:       domain.KindHTTPStatus,
		StatusCode: 400,
		Err:        errors.New(`provider returned HTTP 400: Requested token count exceeds the model's maximum context length of 262144 tokens.`),
	}
	if got, ok := contextLimitFromError(overflow); !ok || got != 262144 {
		t.Fatalf("contextLimitFromError = (%d, %t), want (262144, true)", got, ok)
	}
	if _, ok := contextLimitFromError(errors.New("plain error")); ok {
		t.Fatal("contextLimitFromError should not match non-ProviderError")
	}
}

func TestIsRetryableProviderErrorRejectsPermanentFailure(t *testing.T) {
	err := &domain.ProviderError{
		Kind:       domain.KindHTTPStatus,
		StatusCode: 503,
		Err:        errors.New("provider returned HTTP 503: insufficient balance"),
	}
	if isRetryableProviderError(err) {
		t.Fatal("503 with billing body must NOT be retryable")
	}
}

// The observed OpenAI TPM rejection body (delivered either as an in-stream
// SSE error event on the Responses API or as an HTTP 429 on Chat
// Completions). Sanitized org id.
const tpmOverflowBody = "openai: stream error: Request too large for gpt-5.6-luna in organization org-test on tokens per min (TPM): Limit 200000, Requested 333331. The input or output tokens must be reduced in order to run successfully. Visit https://platform.openai.com/account/rate-limits to learn more."

// TestIsTPMOverflowSemantics: the modern parser distinguishes three TPM
// rejection classes. Structural (requested > limit) and dominant (requested
// > half of limit) both require shrinking the request — the compact-then-
// retry path handles them via isTPMDominatedRequest, which covers structural
// as a subset. Modest requests (<= half the budget) are genuine congestion:
// waiting for the window to drain is the right fix.
func TestIsTPMDominatedRequestApp(t *testing.T) {
	structural := &domain.ProviderError{Kind: domain.KindSSETransport, Temporary: true, Err: errors.New(tpmOverflowBody)}
	if !isTPMDominatedRequest(structural) {
		t.Fatal("requested > limit must be dominated (structural subset)")
	}
	// Wrapping layers must not hide the signal.
	if !isTPMDominatedRequest(fmt.Errorf("stream round failed: %w", structural)) {
		t.Fatal("wrapped dominant TPM must still be detected")
	}
	dominant := &domain.ProviderError{Kind: domain.KindHTTPStatus, StatusCode: 429, RetryAfter: 30 * time.Second,
		Err: errors.New("Request too large for gpt-5.6-luna on tokens per min (TPM): Limit 500000, Used 271036, Requested 355391.")}
	if !isTPMDominatedRequest(dominant) {
		t.Fatal("requested > half the budget must be dominated")
	}
	modest := &domain.ProviderError{Kind: domain.KindHTTPStatus, StatusCode: 429, RetryAfter: 30 * time.Second,
		Err: errors.New("Request too large on tokens per min (TPM): Limit 500000, Used 271036, Requested 40000.")}
	if isTPMDominatedRequest(modest) {
		t.Fatal("requested <= half the budget is congestion, not dominated")
	}
	if isTPMDominatedRequest(errors.New("boom")) {
		t.Fatal("unrelated error must not match")
	}
	if isTPMDominatedRequest(nil) {
		t.Fatal("nil error must not match")
	}
}

// TestProviderRetryDelayRejectsStructuralTPM verifies that a structural TPM
// rejection is never retried, even when it arrives as an HTTP 429 with a
// Retry-After that would normally qualify. Retrying resends the same
// oversized request, which fails again in every window.
func TestProviderRetryDelayRejectsStructuralTPM(t *testing.T) {
	err := &domain.ProviderError{Kind: domain.KindHTTPStatus, StatusCode: 429, RetryAfter: 30 * time.Second, Err: errors.New(tpmOverflowBody)}
	delay, retryable := providerRetryDelay(err, 1)
	if retryable {
		t.Fatalf("structural TPM must not be retryable, got delay=%s", delay)
	}
	// The transient variant with the same status stays retryable.
	transient := &domain.ProviderError{Kind: domain.KindHTTPStatus, StatusCode: 429, RetryAfter: 30 * time.Second,
		Err: errors.New("Request too large on tokens per min (TPM): Limit 200000, Requested 150000.")}
	if _, retryable := providerRetryDelay(transient, 1); !retryable {
		t.Fatal("transient TPM with Retry-After must stay retryable")
	}
}

// TestShouldEmergencyCompactTPM verifies that TPM rejections where the
// request dominates the per-minute budget trigger emergency compaction even
// when the local token estimate is far below the compaction trigger — the
// provider's own numbers are the proof (image-heavy transcripts are
// routinely undercounted by the chars/4 estimate). Structural (requested >
// limit) and dominant (requested > half of limit) both qualify; a modest
// request is congestion and must not force compaction.
func TestShouldEmergencyCompactTPM(t *testing.T) {
	structural := &domain.ProviderError{Kind: domain.KindSSETransport, Temporary: true, Err: errors.New(tpmOverflowBody)}
	if !shouldEmergencyCompact(structural, 50_000, 150_000) {
		t.Fatal("structural TPM must force emergency compaction despite low estimate")
	}
	dominant := &domain.ProviderError{Kind: domain.KindHTTPStatus, StatusCode: 429, RetryAfter: 30 * time.Second,
		Err: errors.New("Request too large on tokens per min (TPM): Limit 500000, Used 100000, Requested 300000.")}
	if !shouldEmergencyCompact(dominant, 50_000, 150_000) {
		t.Fatal("dominant TPM (request > half the budget) must force emergency compaction")
	}
	modest := &domain.ProviderError{Kind: domain.KindHTTPStatus, StatusCode: 429, RetryAfter: 30 * time.Second,
		Err: errors.New("Request too large on tokens per min (TPM): Limit 500000, Used 400000, Requested 40000.")}
	if shouldEmergencyCompact(modest, 50_000, 150_000) {
		t.Fatal("modest TPM (congestion) must not force compaction")
	}
}
