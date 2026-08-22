package application

import (
	"encoding/json"

	"nusashell/contracts"
)

// dispatchMemory routes memory.* RPC methods to their handlers. Called by
// App.Dispatch for any method whose first segment is "memory".
func (a *App) dispatchMemory(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	switch method {
	case contracts.MethodMemoryList:
		return a.handleMemoryList()
	case contracts.MethodMemorySave:
		var req contracts.MemorySaveRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleMemorySave(req)
	case contracts.MethodMemorySearch:
		var req contracts.MemorySearchRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleMemorySearch(req)
	case contracts.MethodMemoryDelete:
		var req contracts.MemoryIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleMemoryDelete(req)
	default:
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "unknown memory method: " + method}
	}
}
