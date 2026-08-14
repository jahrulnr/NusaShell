package application

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestProviderRetryDelayHonorsRetryAfter(t *testing.T) {
	delay, retryable := providerRetryDelay(&UpstreamError{
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
		{name: "request timeout", err: &UpstreamError{StatusCode: 408}, retryable: true},
		{name: "conflict", err: &UpstreamError{StatusCode: 409}, retryable: true},
		{name: "too early", err: &UpstreamError{StatusCode: 425}, retryable: true},
		{name: "rate limited", err: &UpstreamError{StatusCode: 429}, retryable: true},
		{name: "server error", err: &UpstreamError{StatusCode: 503}, retryable: true},
		{name: "temporary transport error", err: &UpstreamError{Temporary: true}, retryable: true},
		{name: "invalid request", err: &UpstreamError{StatusCode: 400}, retryable: false},
		{name: "unauthorized", err: &UpstreamError{StatusCode: 401}, retryable: false},
		{name: "forbidden", err: &UpstreamError{StatusCode: 403}, retryable: false},
		{name: "not found", err: &UpstreamError{StatusCode: 404}, retryable: false},
		{name: "cancelled", err: &UpstreamError{Temporary: true, Err: context.Canceled}, retryable: false},
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
	delay, retryable := providerRetryDelay(&UpstreamError{
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
	delay, retryable := providerRetryDelay(&UpstreamError{
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
	_, retryable := providerRetryDelay(&UpstreamError{
		StatusCode: 429,
		RetryAfter: retryAfterCutoff,
		Err:        errors.New("rate limited"),
	}, 1)
	if !retryable {
		t.Fatalf("expected retryable=true at cutoff (%s)", retryAfterCutoff)
	}

	// Just above cutoff — not retryable
	_, retryable = providerRetryDelay(&UpstreamError{
		StatusCode: 429,
		RetryAfter: retryAfterCutoff + 1*time.Second,
		Err:        errors.New("rate limited"),
	}, 1)
	if retryable {
		t.Fatalf("expected retryable=false just above cutoff (%s+1s)", retryAfterCutoff)
	}
}
