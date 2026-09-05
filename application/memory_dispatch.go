package application

import (
	"encoding/json"

	"nusashell/contracts"
)

func (a *App) dispatchMemory(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	return tableDispatcher(map[string]rpcHandler{
		contracts.MethodMemoryList:        noPayload(a.handleMemoryList),
		contracts.MethodMemorySearch:      decodeReq(a.handleMemorySearch),
		contracts.MethodMemoryGet:         decodeReq(a.handleMemoryGet),
		contracts.MethodMemoryRetire:      decodeReq(a.handleMemoryRetire),
		contracts.MethodMemoryUserUpdate:  decodeReq(a.handleMemoryUserUpdate),
		contracts.MethodMemoryAgentUpdate: decodeReq(a.handleMemoryAgentUpdate),
	}, "memory")(method, payload)
}
