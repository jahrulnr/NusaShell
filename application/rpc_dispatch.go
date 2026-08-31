package application

import (
	"encoding/json"

	"nusashell/contracts"
)

// rpcHandler is one RPC route: it decodes its own payload (or ignores it
// for payload-less methods) and returns the response or a wire error.
type rpcHandler func(payload json.RawMessage) (any, *contracts.RPCError)

// noPayload adapts a payload-less handler to the rpcHandler shape.
func noPayload(h func() (any, *contracts.RPCError)) rpcHandler {
	return func(json.RawMessage) (any, *contracts.RPCError) { return h() }
}

// decodeReq adapts a typed handler by decoding the payload into T first.
// Decode failures produce the standard RPC validation error.
func decodeReq[T any](h func(T) (any, *contracts.RPCError)) rpcHandler {
	return func(payload json.RawMessage) (any, *contracts.RPCError) {
		var req T
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return h(req)
	}
}

// tableDispatcher builds the family dispatcher shape shared by every
// `<family>.*` RPC group: route by exact method name, decode per route,
// fail with the standard "unknown <family> method" error otherwise.
func tableDispatcher(routes map[string]rpcHandler, family string) func(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	return func(method string, payload json.RawMessage) (any, *contracts.RPCError) {
		if fn, ok := routes[method]; ok {
			return fn(payload)
		}
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "unknown " + family + " method: " + method}
	}
}
