// Package contracts defines the wire protocol shared by the frontend and the
// backend: HTTP /rpc envelopes, WebSocket envelopes, SSE events, and the
// method roster. Golden fixtures under testdata/ pin the JSON shapes.
package contracts

import (
	"encoding/json"
	"fmt"
)

// Request is the HTTP POST /rpc body.
type Request struct {
	Method  string          `json:"method"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// ErrorCode classifies RPC failures. There is deliberately no auth/ratelimit
// code: NusaShell Light is a personal/community shell without a security layer.
type ErrorCode string

const (
	CodeValidation ErrorCode = "VALIDATION_ERROR"
	CodeNotFound   ErrorCode = "NOT_FOUND"
	CodeConflict   ErrorCode = "CONFLICT"
	CodeProvider   ErrorCode = "PROVIDER_ERROR"
	CodeInternal   ErrorCode = "INTERNAL_ERROR"
)

// RPCError is the machine-readable error body.
type RPCError struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
}

func (e *RPCError) Error() string { return e.Message }

// Response is the HTTP POST /rpc response body.
type Response struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}

// OKResult builds a successful response.
func OKResult(v any) Response {
	b, err := json.Marshal(v)
	if err != nil {
		return ErrResult(CodeInternal, err.Error())
	}
	return Response{OK: true, Result: b}
}

// ErrResult builds a failed response.
func ErrResult(code ErrorCode, message string) Response {
	return Response{OK: false, Error: &RPCError{Code: code, Message: message}}
}

// Event is one server-pushed event, carried by both SSE and WebSocket.
type Event struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// NewEvent wraps a payload into an Event.
func NewEvent(typ string, v any) Event {
	b, err := json.Marshal(v)
	if err != nil {
		b = json.RawMessage(fmt.Sprintf(`{"error":%q}`, err.Error()))
	}
	return Event{Type: typ, Payload: b}
}

// WSRequest is a client message over the WebSocket transport.
type WSRequest struct {
	ID      int             `json:"id"`
	Method  string          `json:"method"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// WSResponse answers a WSRequest; events on the same socket are plain Events.
type WSResponse struct {
	ID     int             `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}

// DecodePayload unmarshals a raw payload into dst, or returns a validation error.
func DecodePayload(raw json.RawMessage, dst any) *RPCError {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return &RPCError{Code: CodeValidation, Message: fmt.Sprintf("malformed payload: %v", err)}
	}
	return nil
}
