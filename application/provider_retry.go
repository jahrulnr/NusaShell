package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"time"

	"nusashell/domain"
)

const (
	maxProviderAttempts = domain.MaxProviderAttempts
	retryBaseDelay      = domain.RetryBaseDelay
	retryMaxDelay       = domain.RetryMaxDelay
)

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

// waitForRetry uses the injected sleeper when one is configured and falls
// back to the production cancellation-aware wait. Keeping that choice here
// lets every agent use the same retry loop without making tests sleep or
// requiring every lightweight App fixture to wire a dependency.
func (a *App) waitForRetry(ctx context.Context, delay time.Duration) error {
	sleeper := sleepForRetry
	if a != nil && a.retrySleeper != nil {
		sleeper = a.retrySleeper
	}
	return sleeper(ctx, delay)
}

func providerRetryDelay(err error, retry int) (time.Duration, bool) {
	if !isRetryableProviderError(err) {
		return 0, false
	}
	var upstream *domain.ProviderError
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
	return domain.CanAutoRetry(err)
}

// describeProviderError renders a provider error for the retry log so
// operators can tell a 429 rate limit from a mid-stream EOF without digging
// through wrapped layers. Non-ProviderError values pass through as their plain
// Error() string.
func describeProviderError(err error) string {
	if err == nil {
		return ""
	}
	var upstream *domain.ProviderError
	if !errors.As(err, &upstream) {
		return err.Error()
	}
	parts := []string{upstream.Error()}
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

// isContextOverflowError reports whether the provider rejected the request
// because the prompt + max_output combination exceeded the model's context
// window. Providers return HTTP 400 with body containing phrases like
// "maximum context length", "context_length_exceeded", or "reduce the length
// of the input prompt". Used by the emergency-compaction safety net to force
// a compaction and retry instead of failing the turn.
func isContextOverflowError(err error) bool {
	var upstream *domain.ProviderError
	if !errors.As(err, &upstream) {
		return false
	}
	if upstream.StatusCode != 400 {
		return false
	}
	body := ""
	if upstream.Err != nil {
		body = strings.ToLower(upstream.Err.Error())
	}
	for _, phrase := range contextOverflowPhrases {
		if strings.Contains(body, phrase) {
			return true
		}
	}
	return false
}

var contextOverflowPhrases = domain.ContextOverflowPhrases

// contextLimitFromError extracts the explicit context-window limit from a
// provider 400 overflow error, if one is present. Used to force emergency
// compaction when our local estimate is below the trigger but the provider
// already told us the actual limit.
func contextLimitFromError(err error) (int, bool) {
	var upstream *domain.ProviderError
	if !errors.As(err, &upstream) || upstream.Err == nil {
		return 0, false
	}
	n, _, ok := domain.ExtractContextLimit(upstream.Err.Error())
	return n, ok
}

// shouldEmergencyCompact reports whether a provider error should trigger
// destructive emergency compaction. The body must match an explicit overflow
// phrase (not a generic field name like "input_tokens"). Normally the local
// token estimate must already exceed the compaction trigger, but if the
// provider states a concrete context limit we trust the error even when the
// heuristic estimate is low — different tokenizers can count more tokens than
// our chars/4 estimate.
func shouldEmergencyCompact(err error, estimatedTokens, compactionTrigger int) bool {
	// A TPM rejection where the request dominates the per-minute budget
	// (structural: needs more than the whole budget; or dominant: more
	// than half of it) cannot be fixed by waiting — the only recovery is
	// shrinking the request via emergency compaction. Dominant requests
	// also cover every structural case (requested > limit implies
	// requested > limit/2), so one predicate gates both.
	if isTPMDominatedRequest(err) {
		return true
	}
	if !isContextOverflowError(err) {
		return false
	}
	if estimatedTokens > compactionTrigger {
		return true
	}
	_, ok := contextLimitFromError(err)
	return ok
}

// isPrematureStreamEnd reports whether the provider returned a 2xx response
// that started streaming but ended without completing the turn — no [DONE]
// sentinel and no finish_reason. The stream's clean EOF is wrapped as a
// network error with io.ErrUnexpectedEOF as the cause (see
// infrastructure/ai/openai/stream.go and infrastructure/ai/compat/stream.go).
//
// This is distinct from a hard connection error (ECONNRESET, timeout): the
// provider accepted the request and began generating, then the SSE channel
// closed cleanly mid-stream. The partial content is valid, so the turn can
// be continued with a nudge instead of failing or restarting from scratch.
func isPrematureStreamEnd(err error) bool {
	if err == nil {
		return false
	}
	var upstream *domain.ProviderError
	if !errors.As(err, &upstream) {
		return false
	}
	return upstream.Kind == domain.KindConnect && errors.Is(err, io.ErrUnexpectedEOF)
}
