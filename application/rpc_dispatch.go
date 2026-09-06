package application

import (
	"encoding/json"

	"nusashell/contracts"
	"nusashell/pkg/rpcdispatch"
)

// rpcHandler is one RPC route: it decodes its own payload (or ignores it
// for payload-less methods) and returns the response or a wire error.
type rpcHandler = rpcdispatch.Handler

// noPayload adapts a payload-less handler to the rpcHandler shape.
func noPayload(h func() (any, *contracts.RPCError)) rpcHandler {
	return rpcdispatch.NoPayload(h)
}

// decodeReq adapts a typed handler by decoding the payload into T first.
// Decode failures produce the standard RPC validation error.
func decodeReq[T any](h func(T) (any, *contracts.RPCError)) rpcHandler {
	return rpcdispatch.DecodeReq(h)
}

// tableDispatcher builds the family dispatcher shape shared by every
// `<family>.*` RPC group: route by exact method name, decode per route,
// fail with the standard "unknown <family> method" error otherwise.
func tableDispatcher(routes map[string]rpcHandler, family string) func(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	return rpcdispatch.Table(routes, family)
}
