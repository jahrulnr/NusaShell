package provider

import (
	"errors"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"time"

	"nusashell/domain"
)

const (
	retryBaseDelay = domain.RetryBaseDelay
	retryMaxDelay  = domain.RetryMaxDelay
)

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

func contextLimitFromError(err error) (int, bool) {
	var upstream *domain.ProviderError
	if !errors.As(err, &upstream) || upstream.Err == nil {
		return 0, false
	}
	n, _, ok := ExtractContextLimit(upstream.Err.Error())
	return n, ok
}

func shouldEmergencyCompact(err error, estimatedTokens, compactionTrigger int) bool {
	if tpmDominatedRequest(err) {
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

func tpmDominatedRequest(err error) bool {
	limit, _, requested, ok := domain.ParseTPMError(errBody(err))
	return ok && requested*2 > limit
}

func errBody(err error) string {
	if err == nil {
		return ""
	}
	var upstream *domain.ProviderError
	if errors.As(err, &upstream) && upstream.Err != nil {
		return upstream.Err.Error()
	}
	return err.Error()
}

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

// Exported names for root wrappers. Unexported names stay so in-package
// tests and the original call sites keep matching.

func RetryDelay(err error, retry int) (time.Duration, bool) { return providerRetryDelay(err, retry) }
func IsRetryableError(err error) bool                       { return isRetryableProviderError(err) }
func DescribeError(err error) string                        { return describeProviderError(err) }
func IsContextOverflow(err error) bool                      { return isContextOverflowError(err) }
func ContextLimit(err error) (int, bool)                    { return contextLimitFromError(err) }
func ShouldEmergencyCompact(err error, estimatedTokens, compactionTrigger int) bool {
	return shouldEmergencyCompact(err, estimatedTokens, compactionTrigger)
}
func IsPrematureStreamEnd(err error) bool { return isPrematureStreamEnd(err) }
