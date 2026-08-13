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
