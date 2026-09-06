package provider

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"nusashell/domain"
	clock "nusashell/pkg/time"
)

// DefaultRateLimitWindow is assumed when a 429 response carries no
// Retry-After header. TokenRouter uses 1 minute; other gateways are similar.
const DefaultRateLimitWindow = domain.DefaultRateLimitWindow

// RateLimiter remembers per-provider 429 windows so subsequent requests can
// wait client-side instead of hammering a gateway that omitted Retry-After.
type RateLimiter struct {
	mu      sync.Mutex
	windows map[string]time.Time
}

// NewRateLimiter returns an empty limiter.
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{windows: make(map[string]time.Time)}
}

func (r *RateLimiter) init() {
	if r.windows == nil {
		r.windows = make(map[string]time.Time)
	}
}

// Mark records that a provider rejected a request with a 429. If nextAllowed
// is zero, defaults to now + DefaultRateLimitWindow.
func (r *RateLimiter) Mark(providerID string, nextAllowed time.Time) {
	if r == nil || providerID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	if nextAllowed.IsZero() {
		nextAllowed = clock.NewTime().Time().Add(DefaultRateLimitWindow)
	}
	r.windows[providerID] = nextAllowed
}

// Wait returns how long the caller should wait before sending another request
// to this provider, or 0 if not rate-limited (or the window already cleared).
func (r *RateLimiter) Wait(providerID string) time.Duration {
	if r == nil || providerID == "" {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	next, ok := r.windows[providerID]
	if !ok {
		return 0
	}
	wait := clock.NewTime().Until(next)
	if wait <= 0 {
		delete(r.windows, providerID)
		return 0
	}
	return wait
}

func FriendlyRateLimitError(providerLabel string, upstream *domain.ProviderError, wait time.Duration) error {
	if providerLabel == "" {
		providerLabel = "provider"
	}
	body := ""
	if upstream != nil && upstream.Err != nil {
		body = upstream.Err.Error()
	}
	if limit, used, requested, ok := domain.ParseTPMError(body); ok && requested*2 > limit {
		return fmt.Errorf("%s is rate-limited on tokens per minute: this request needs %d tokens (limit %d/min, %d already used). The conversation will be compacted to fit, or reduce the input/output tokens.", providerLabel, requested, limit, used)
	}
	if wait <= 0 {
		wait = DefaultRateLimitWindow
	}
	secs := int64(wait.Seconds())
	return fmt.Errorf("%s is rate-limited (max ~5 requests/min). Wait ~%ds and try again.", providerLabel, secs)
}

// Decorate converts a 429 upstream error into a friendly user-facing error
// and records the provider rate-limit window. Returns the original error
// unchanged for non-429 failures. providerName is the human label used in
// the message (empty falls back to "provider").
func (r *RateLimiter) Decorate(providerID, providerName string, err error) error {
	if err == nil {
		return nil
	}
	var upstream *domain.ProviderError
	if !errors.As(err, &upstream) || upstream.StatusCode != 429 {
		return err
	}
	next := clock.NewTime().Time()
	if upstream.RetryAfter > 0 {
		next = next.Add(upstream.RetryAfter)
	} else {
		next = next.Add(DefaultRateLimitWindow)
	}
	r.Mark(providerID, next)
	wait := r.Wait(providerID)
	return FriendlyRateLimitError(providerName, upstream, wait)
}
