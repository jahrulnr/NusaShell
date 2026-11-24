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
	"time"
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
	Code    int
	Message string
	Cause   error
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

// new normalizes the code and builds the value.
func new(code int, message string, causedBy error) *AppError {
	if code < 100 || code > 599 {
		message = fmt.Sprintf("invalid status code provided: %d", code)
		causedBy = fmt.Errorf("invalid status code: %d", code)
		code = StatusInternalServerError
	}
	return &AppError{Code: code, Message: message, Cause: causedBy}
}

func (e *AppError) Error() string   { return e.Message }
func (e *AppError) Unwrap() error   { return e.Cause }
func (e *AppError) StatusCode() int { return e.Code }

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
	return fmt.Sprintf("error %d: %s (caused by: %v)", e.Code, e.Message, cause)
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

// As unwraps err into an IAppError via errors.As. Non-AppError causes fall
// back to a 500 "internal error" — no information leaks to Message.
func As(err error) IAppError {
	if appErr, ok := errors.AsType[IAppError](err); ok {
		return appErr
	}
	return New(StatusInternalServerError, "internal error", err)
}
