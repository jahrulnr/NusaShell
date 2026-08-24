package application

// Provider rate-limit window tracker.
//
// Some gateways (TokenRouter) enforce short request windows (e.g. 5 requests
// per minute) WITHOUT sending a Retry-After header. The agent retry policy
// deliberately does not retry 429s without Retry-After because the exponential
// backoff is far shorter than the window and would make the limit worse.
// Instead we remember, per provider, when the window is expected to clear and
// gate further requests client-side, and surface a human-readable message.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"nusashell/domain"
)

// DefaultRateLimitWindow is assumed when a 429 response carries no
// Retry-After header. TokenRouter uses 1 minute; other gateways are similar.
const DefaultRateLimitWindow = time.Minute

func (a *App) initRateLimitWindows() {
	a.rlMu.Lock()
	defer a.rlMu.Unlock()
	if a.rlWindows == nil {
		a.rlWindows = make(map[string]time.Time)
	}
}

// MarkProviderRateLimited records that a provider rejected a request with a
// 429. If nextAllowed is zero, defaults to now + DefaultRateLimitWindow.
func (a *App) MarkProviderRateLimited(providerID string, nextAllowed time.Time) {
	if providerID == "" {
		return
	}
	a.initRateLimitWindows()
	a.rlMu.Lock()
	defer a.rlMu.Unlock()
	if nextAllowed.IsZero() {
		nextAllowed = time.Now().Add(DefaultRateLimitWindow)
	}
	a.rlWindows[providerID] = nextAllowed
}

// ProviderRateLimitWait returns how long the caller should wait before
// sending another request to this provider, or 0 if not rate-limited (or the
// window already cleared).
func (a *App) ProviderRateLimitWait(providerID string) time.Duration {
	if providerID == "" {
		return 0
	}
	a.initRateLimitWindows()
	a.rlMu.Lock()
	defer a.rlMu.Unlock()
	next, ok := a.rlWindows[providerID]
	if !ok {
		return 0
	}
	wait := time.Until(next)
	if wait <= 0 {
		delete(a.rlWindows, providerID)
		return 0
	}
	return wait
}

// friendlyRateLimitError renders a 429 into a human-readable message that
// tells the user how long to wait, instead of a raw provider JSON blob.
func (a *App) friendlyRateLimitError(providerID string, upstream *UpstreamError, wait time.Duration) error {
	providerLabel := a.providerNameByID(providerID)
	if wait <= 0 {
		wait = DefaultRateLimitWindow
	}
	secs := int64(wait.Seconds())
	return fmt.Errorf("%s is rate-limited (max ~5 requests/min). Wait ~%ds and try again.", providerLabel, secs)
}

var _ = context.Background
var _ = domain.ProviderChat

// decorateRateLimitError converts a 429 upstream error into a friendly
// user-facing error and records the provider rate-limit window. Returns the
// original error unchanged for non-429 failures.
func (a *App) decorateRateLimitError(providerID string, err error) error {
	if err == nil {
		return nil
	}
	var upstream *UpstreamError
	if !errors.As(err, &upstream) || upstream.StatusCode != 429 {
		return err
	}
	// Record the window: prefer the provider's Retry-After, else our default.
	next := time.Now()
	if upstream.RetryAfter > 0 {
		next = next.Add(upstream.RetryAfter)
	} else {
		next = next.Add(DefaultRateLimitWindow)
	}
	a.MarkProviderRateLimited(providerID, next)
	wait := a.ProviderRateLimitWait(providerID)
	return a.friendlyRateLimitError(providerID, upstream, wait)
}
