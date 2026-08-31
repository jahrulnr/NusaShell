package domain

import (
	"strings"
	"time"
)

// Provider retry policy constants.
//
// These govern the bounded retry loop around provider stream/complete
// requests. The loop itself lives in the application layer because it
// operates on UpstreamError (an application contract); the policy
// constants and the pure body-classification helpers live here so the
// retry rules are visible at the layer that owns the conversation model.
const (
	// MaxProviderAttempts is the maximum number of attempts for a single
	// provider request before the error surfaces to the turn.
	MaxProviderAttempts = 5
	// RetryBaseDelay is the initial backoff delay between retries.
	RetryBaseDelay = 250 * time.Millisecond
	// RetryMaxDelay is the cap on the exponential backoff delay.
	RetryMaxDelay = 4 * time.Second
	// RetryAfterCutoff is the maximum Retry-After the agent will honor.
	// If a provider advertises a longer reset window, the error is not
	// retried — the turn fails immediately so the user sees the error
	// and can retry manually when the rate limit clears.
	RetryAfterCutoff = 5 * time.Minute
)

// PermanentFailurePhrases are body substrings that indicate a billing/credit
// failure rather than a transient server issue. Matched case-insensitively.
// Mirrors the TS isPermanentProviderFailure phrase list.
var PermanentFailurePhrases = []string{
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

// ContextOverflowPhrases are body substrings that indicate the provider
// rejected the request because the prompt + max_output combination
// exceeded the model's context window. Used by the emergency-compaction
// safety net to force a compaction and retry instead of failing the turn.
var ContextOverflowPhrases = []string{
	"maximum context length",
	"context_length_exceeded",
	"reduce the length of the input prompt",
	"too many input tokens",
	"prompt is too long",
}

// IsPermanentProviderFailure reports whether the HTTP status + body
// indicate a billing/credit exhaustion that will not resolve on retry.
// A 503 with "insufficient balance" is permanent; a 503 with "internal
// server error" is not. Status 402 is always permanent.
func IsPermanentProviderFailure(status int, body string) bool {
	if status == 402 {
		return true
	}
	normalized := strings.ToLower(body)
	for _, phrase := range PermanentFailurePhrases {
		if strings.Contains(normalized, phrase) {
			return true
		}
	}
	return false
}
