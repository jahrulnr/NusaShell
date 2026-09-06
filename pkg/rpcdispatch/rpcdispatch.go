// Package rpcdispatch is the shared RPC table helper used by application
// feature packages. Subpackages cannot import the root application package,
// so decode-and-route stays here instead of a common/ or shared/ tree.
package rpcdispatch

import (
	"encoding/json"

	"nusashell/contracts"
)

// Handler is one RPC route: it decodes its own payload (or ignores it
// for payload-less methods) and returns the response or a wire error.
type Handler func(payload json.RawMessage) (any, *contracts.RPCError)

// NoPayload adapts a payload-less handler to the Handler shape.
func NoPayload(h func() (any, *contracts.RPCError)) Handler {
	return func(json.RawMessage) (any, *contracts.RPCError) { return h() }
}

// DecodeReq adapts a typed handler by decoding the payload into T first.
// Decode failures produce the standard RPC validation error.
func DecodeReq[T any](h func(T) (any, *contracts.RPCError)) Handler {
	return func(payload json.RawMessage) (any, *contracts.RPCError) {
		var req T
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return h(req)
	}
}

// Table builds the family dispatcher shape shared by every `<family>.*`
// RPC group: route by exact method name, decode per route, fail with the
// standard "unknown <family> method" error otherwise.
func Table(routes map[string]Handler, family string) func(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	return func(method string, payload json.RawMessage) (any, *contracts.RPCError) {
		if fn, ok := routes[method]; ok {
			return fn(payload)
		}
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "unknown " + family + " method: " + method}
	}
}

// Internal wraps an unexpected error as a wire internal RPC error.
func Internal(err error) *contracts.RPCError {
	return &contracts.RPCError{Code: contracts.CodeInternal, Message: err.Error()}
}
