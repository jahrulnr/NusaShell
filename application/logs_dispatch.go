package application

import (
	"encoding/json"

	"nusashell/contracts"
)

// dispatchLogs routes logs.* RPC methods to their handlers. Called by
// App.Dispatch for any method whose first segment is "logs".
func (a *App) dispatchLogs(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	return tableDispatcher(map[string]rpcHandler{
		contracts.MethodLogsList:  decodeReq(a.handleLogsList),
		contracts.MethodLogsClear: noPayload(a.handleLogsClear),
	}, "logs")(method, payload)
}
