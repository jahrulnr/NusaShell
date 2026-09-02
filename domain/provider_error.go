package domain

import (
	"context"
	"errors"
	"io"
	"net"
	"regexp"
	"strconv"
	"syscall"
	"time"
)

// ProviderErrorKind identifies the boundary at which a provider request
// failed. It is deliberately provider-neutral so the same retry decision can
// be used by conversations, background reviews, and automation runs.
type ProviderErrorKind string

const (
	KindHTTPStatus   ProviderErrorKind = "http_status"
	KindConnect      ProviderErrorKind = "connect"
	KindSSETransport ProviderErrorKind = "sse_transport"
	KindIdleTimeout  ProviderErrorKind = "idle_timeout"
)

// ProviderError carries the retry metadata produced by an AI provider
// adapter. The error lives in domain because retryability is a product rule,
// not a property of one application runner.
type ProviderError struct {
	Kind       ProviderErrorKind
	StatusCode int
	RetryAfter time.Duration
	Temporary  bool
	Err        error
}

func (e *ProviderError) Error() string {
	if e == nil || e.Err == nil {
		return "provider request failed"
	}
	return e.Err.Error()
}

func (e *ProviderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// CanAutoRetry reports whether an agent may transparently retry this
// provider failure. It is intentionally conservative: a retry must have a
// reasonable chance of succeeding without user changes. In particular, an
// unknown 429 is hard-failed because a short exponential backoff cannot
// safely guess the provider's rate-limit window.
func (e *ProviderError) CanAutoRetry() bool {
	if e == nil || errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
		return false
	}
	body := e.Error()
	if IsStructuralTPMFailure(body) || IsPermanentProviderFailure(e.StatusCode, body) {
		return false
	}
	if e.RetryAfter > RetryAfterCutoff {
		return false
	}
	if e.StatusCode != 0 {
		switch e.StatusCode {
		case 408, 409, 425:
			return true
		case 429:
			return e.RetryAfter > 0
		default:
			// An HTTP error is authoritative. A provider may mark a 400 as
			// temporary while constructing its generic error, but retrying
			// the same invalid request cannot help. Learnable request repairs
			// are handled explicitly by the application before it reaches
			// this policy.
			return e.StatusCode >= 500 && e.StatusCode <= 599
		}
	}
	if e.Kind == KindIdleTimeout || e.Temporary {
		return true
	}
	return isTransientTransportError(e.Err)
}

// CanAutoRetry is the shared retryability decision for all agent runners.
// It follows wrapped errors to the domain provider error and returns false
// for errors that carry no explicit retry policy.
func CanAutoRetry(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var decider interface{ CanAutoRetry() bool }
	if errors.As(err, &decider) {
		return decider.CanAutoRetry()
	}
	// A few adapter boundaries can return the standard library transport
	// error directly (before a provider-specific wrapper is available). Keep
	// those failures eligible too, while deliberately rejecting plain text
	// that merely mentions a network problem.
	return isTransientTransportError(err)
}

// isTransientTransportError recognizes transport failures that mean the
// request may succeed when the connection is recreated. It is intentionally
// based on typed errors and OS sentinels, not substring matching: an HTTP 400
// body can contain words such as "connection" without becoming retryable.
func isTransientTransportError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary()) {
		return true
	}
	for _, target := range []error{
		syscall.ECONNRESET,
		syscall.ECONNABORTED,
		syscall.ECONNREFUSED,
		syscall.EPIPE,
		syscall.ETIMEDOUT,
		syscall.ENETRESET,
		syscall.ENETDOWN,
		syscall.ENETUNREACH,
		syscall.EHOSTUNREACH,
	} {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

// ParseTPMLimitRequested extracts the per-minute token budget and the token
// count rejected by a provider's tokens-per-minute error.
func ParseTPMLimitRequested(body string) (limit, requested int, ok bool) {
	m := tpmLimitRe.FindStringSubmatch(body)
	if len(m) < 3 {
		return 0, 0, false
	}
	limit, err1 := strconv.Atoi(m[1])
	requested, err2 := strconv.Atoi(m[2])
	if err1 != nil || err2 != nil || limit <= 0 {
		return 0, 0, false
	}
	return limit, requested, true
}

// IsStructuralTPMFailure distinguishes an oversized single request from a
// transient request-count/window limit. Waiting cannot make a request whose
// token count exceeds the whole per-minute budget succeed.
func IsStructuralTPMFailure(body string) bool {
	limit, requested, ok := ParseTPMLimitRequested(body)
	return ok && requested > limit
}

// tpmLimitRe matches OpenAI's tokens-per-minute rejection body, delivered as
// an HTTP 429 or as an in-stream SSE error event.
var tpmLimitRe = regexp.MustCompile(`(?i)tokens per min(?:ute)?[^:]*:\s*Limit\s+(\d+),\s*Requested\s+(\d+)`)
