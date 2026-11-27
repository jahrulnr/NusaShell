// Package apperror is the shared error contract: an HTTP status code plus
// a public Message safe for frontend display, wrapping an internal Cause
// that never leaves the backend. One taxonomy covers HTTP and file I/O —
// consumers route on StatusCode() via As(), not on error-string matching.
//
// Routing conventions for LLM calls: 401/403 = dead credentials, 429 =
// rate-limited (try another provider), 413 = context overflow (compact, don't
// fallback), 502/503/504 = upstream unreachable (retry or fallback), other
// 4xx = invalid request (fix the payload, don't retry as-is).
package apperror

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jahrulnr/nusashell/infrastructure/redact"
)

// HTTP status codes used across the codebase.
const (
	StatusBadRequest = 400
	StatusNotFound   = 404
	StatusConflict   = 409

	StatusInternalServerError = 500
	StatusBadGateway          = 502
)

// IAppError is the error contract: status for routing, Message for the
// frontend, Cause for internal diagnostics.
type IAppError interface {
	StatusCode() int
	// Code is the contract's stable machine code when the error names one —
	// a 409 that means "confirmation required" is not an invalid argument —
	// and "" when the status alone classifies it.
	Code() string
	Error() string
	Unwrap() error
	Caused() string
	IsUpstream() bool
}

// AppError carries a status code, a public message, and an internal cause.
// Upstream marks provider-originated failures so consumers can split a
// shared status — an upstream 429 is a provider rate limit, a local 429
// is a full queue.
type AppError struct {
	// Status is the HTTP status consumers route on.
	Status int
	// CodeName optionally names the stable machine code; the zero value
	// lets the transport derive one from Status.
	CodeName string
	Message  string
	Cause    error
	// Upstream marks provider-originated failures so consumers can split a
	// shared status — an upstream 429 is a provider rate limit, a local 429
	// is a full queue.
	Upstream bool
	// RetryDelay is a provider-asked quiet (Retry-After), surfaced for the
	// retry gate; 0 when the provider named none.
	RetryDelay time.Duration
}

// New builds an AppError — code must be a valid HTTP status (100-599).
func New(code int, message string, causedBy error) IAppError {
	return new(code, message, causedBy)
}

// NewCoded builds an AppError that names its own stable code — for a status
// whose derived code would say the wrong thing: a 409 "busy" or
// "confirmation_required" is not an invalid argument.
func NewCoded(code int, codeName, message string, causedBy error) IAppError {
	e := new(code, message, causedBy)
	e.CodeName = codeName
	return e
}

// new normalizes the code and builds the value.
func new(code int, message string, causedBy error) *AppError {
	if code < 100 || code > 599 {
		message = fmt.Sprintf("invalid status code provided: %d", code)
		causedBy = fmt.Errorf("invalid status code: %d", code)
		code = StatusInternalServerError
	}
	return &AppError{Status: code, Message: message, Cause: causedBy}
}

func (e *AppError) Error() string   { return e.Message }
func (e *AppError) Unwrap() error   { return e.Cause }
func (e *AppError) StatusCode() int { return e.Status }
func (e *AppError) Code() string    { return e.CodeName }

// IsUpstream reports whether the failure originated at the provider.
func (e *AppError) IsUpstream() bool { return e.Upstream }

// RetryAfter exposes the provider-asked wait so callers can route on it —
// an adapter is the only layer that sees the header, so it carries the
// delay here. 0 means the provider named none.
func (e *AppError) RetryAfter() time.Duration { return e.RetryDelay }

// Caused renders code + public message + internal cause for logs.
func (e *AppError) Caused() string {
	cause := e.Cause
	if cause == nil {
		cause = errors.New("unknown")
	}
	return fmt.Sprintf("error %d: %s (caused by: %v)", e.Status, e.Message, cause)
}

// FromStatus adapts an upstream HTTP status into an AppError — the shared
// baseline for adapters, marked IsUpstream so consumers can tell a
// provider failure from a local one on the same status. message must be
// safe for the frontend; raw upstream bodies belong in causedBy.
func FromStatus(status int, message string, causedBy error) IAppError {
	if status < 400 || status > 599 {
		status = StatusBadGateway
	}
	e := new(status, message, causedBy)
	e.Upstream = true
	return e
}

func NewNotFoundError(resource string, causedBy error) IAppError {
	return new(StatusNotFound, resource+" not found", causedBy)
}

func NewInternalError(message string, causedBy error) IAppError {
	return new(StatusInternalServerError, message, causedBy)
}

// catalogOverage is the catalog-budget 409. The integer is total - budget
// and is at least 1; every other 409 leaves it off the problem body.
type catalogOverage struct {
	*AppError
	n int
}

// NewCatalogOverage builds the catalog-budget refusal. overage is the number
// of bytes past the budget. The message stays the sentence the owner reads.
func NewCatalogOverage(message string, overage int) IAppError {
	return &catalogOverage{AppError: new(StatusConflict, message, nil), n: overage}
}

func (e *catalogOverage) Overage() int { return e.n }

// OverageOf reports the catalog-budget integer when err carries one of at
// least 1. Every other error, including an unset budget, reports false.
func OverageOf(err error) (int, bool) {
	type numbered interface{ Overage() int }
	var o numbered
	if !errors.As(err, &o) {
		return 0, false
	}
	if o.Overage() < 1 {
		return 0, false
	}
	return o.Overage(), true
}

// Agent failure classes. Absent is not unknown: a value is written only
// while the agent is holding the room.
const (
	AgentClassRateLimited    = "rate_limited"
	AgentClassQuotaExhausted = "quota_exhausted"
	AgentClassCredential     = "credential"
	AgentClassProcess        = "process"
	AgentClassUnknown        = "unknown"
)

// agentFailure is an agent turn that is holding the room. The class is the
// wire word. RetryDelay is set only when the agent named a delay.
type agentFailure struct {
	*AppError
	class string
}

// NewAgentFailure builds a classified agent failure. message is the agent's
// words; a stock stand-in is dropped in favor of the cause, then redacted
// and truncated. delay is the named wait, zero when the agent named none.
func NewAgentFailure(class string, status int, code, message string, cause error, delay time.Duration) IAppError {
	switch class {
	case AgentClassRateLimited, AgentClassQuotaExhausted, AgentClassCredential, AgentClassProcess, AgentClassUnknown:
	default:
		class = AgentClassUnknown
	}
	words := agentWords(message, cause)
	if words == "" && class == AgentClassProcess {
		words = "the agent process exited"
	}
	if words == "" {
		words = "the agent stopped without a reason"
	}
	e := new(status, words, cause)
	e.CodeName = code
	e.Upstream = true
	if delay > 0 {
		e.RetryDelay = delay
	}
	return &agentFailure{AppError: e, class: class}
}

func (e *agentFailure) AgentClass() string {
	if e == nil {
		return ""
	}
	return e.class
}

// AgentClass reports the hold class when err is an agent failure.
func AgentClass(err error) (string, bool) {
	var c interface{ AgentClass() string }
	if !errors.As(err, &c) {
		return "", false
	}
	class := c.AgentClass()
	switch class {
	case AgentClassRateLimited, AgentClassQuotaExhausted, AgentClassCredential, AgentClassProcess, AgentClassUnknown:
		return class, true
	default:
		return "", false
	}
}

func agentWords(message string, cause error) string {
	if words := cleanAgentWords(message); words != "" {
		return words
	}
	if cause == nil {
		return ""
	}
	return cleanAgentWords(cause.Error())
}

func cleanAgentWords(s string) string {
	s = strings.TrimSpace(redact.String(s))
	switch strings.ToLower(s) {
	case "", "agent turn failed", "unknown error", "internal error", "agent process exited":
		return ""
	}
	const max = 500
	if utf8.RuneCountInString(s) > max {
		s = string([]rune(s)[:max]) + "…"
	}
	return s
}

// As unwraps err into an IAppError via errors.As. Non-AppError causes fall
// back to a 500 "internal error" — no information leaks to Message.
func As(err error) IAppError {
	if appErr, ok := errors.AsType[IAppError](err); ok {
		return appErr
	}
	return New(StatusInternalServerError, "internal error", err)
}
