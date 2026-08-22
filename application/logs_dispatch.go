package application

import (
	"encoding/json"

	"nusashell/contracts"
)

// dispatchLogs routes logs.* RPC methods to their handlers. Called by
// App.Dispatch for any method whose first segment is "logs".
func (a *App) dispatchLogs(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	switch method {
	case contracts.MethodLogsList:
		var req contracts.LogsListRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleLogsList(req)
	case contracts.MethodLogsClear:
		return a.handleLogsClear()
	default:
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "unknown logs method: " + method}
	}
}
