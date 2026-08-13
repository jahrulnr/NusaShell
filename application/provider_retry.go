package application

import (
	"context"
	"errors"
	"math/rand"
	"time"
)

const (
	maxProviderAttempts = 3
	retryBaseDelay      = 250 * time.Millisecond
	retryMaxDelay       = 4 * time.Second
)

// UpstreamError carries retry metadata from a provider adapter without making
// the application layer depend on a concrete HTTP client.
type UpstreamError struct {
	StatusCode int
	RetryAfter time.Duration
	Temporary  bool
	Err        error
}

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
	if err.Temporary {
		return true
	}
	switch err.StatusCode {
	case 408, 409, 425, 429:
		return true
	default:
		return err.StatusCode >= 500 && err.StatusCode <= 599
	}
}
