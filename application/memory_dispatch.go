package application

import (
	"encoding/json"

	"nusashell/contracts"
)

// dispatchMemory routes memory.* RPC methods to their handlers. Called by
// App.Dispatch for any method whose first segment is "memory".
func (a *App) dispatchMemory(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	return tableDispatcher(map[string]rpcHandler{
		contracts.MethodMemoryList:          noPayload(a.handleMemoryList),
		contracts.MethodMemorySave:          decodeReq(a.handleMemorySave),
		contracts.MethodMemorySearch:        decodeReq(a.handleMemorySearch),
		contracts.MethodMemoryDelete:        decodeReq(a.handleMemoryDelete),
		contracts.MethodMemoryPrimaryUpdate: decodeReq(a.handleMemoryPrimaryUpdate),
		contracts.MethodMemoryAgentUpdate:   decodeReq(a.handleMemoryAgentUpdate),
	}, "memory")(method, payload)
}
