package application

import (
	"encoding/json"

	"nusashell/contracts"
)

// dispatchDocs routes docs.* RPC methods to their handlers. Called by
// App.Dispatch for any method whose first segment is "docs".
func (a *App) dispatchDocs(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	switch method {
	case contracts.MethodDocsList:
		return a.handleDocsList()
	case contracts.MethodDocsSearch:
		var req contracts.DocsSearchRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleDocsSearch(req)
	case contracts.MethodDocsRead:
		var req contracts.DocReadRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleDocsRead(req)
	default:
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "unknown docs method: " + method}
	}
}
