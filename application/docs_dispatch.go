package application

import (
	"encoding/json"

	"nusashell/contracts"
)

// dispatchDocs routes docs.* RPC methods to their handlers. Called by
// App.Dispatch for any method whose first segment is "docs".
func (a *App) dispatchDocs(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	return tableDispatcher(map[string]rpcHandler{
		contracts.MethodDocsList:   noPayload(a.handleDocsList),
		contracts.MethodDocsSearch: decodeReq(a.handleDocsSearch),
		contracts.MethodDocsRead:   decodeReq(a.handleDocsRead),
	}, "docs")(method, payload)
}
