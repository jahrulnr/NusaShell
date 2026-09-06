package memory

import (
	"encoding/json"

	"nusashell/contracts"
	"nusashell/pkg/rpcdispatch"
)

// Dispatch routes memory.* RPC methods to their handlers.
func (s *Service) Dispatch(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	if s == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "memory not available"}
	}
	return rpcdispatch.Table(map[string]rpcdispatch.Handler{
		contracts.MethodMemoryList:        rpcdispatch.NoPayload(s.HandleList),
		contracts.MethodMemorySearch:      rpcdispatch.DecodeReq(s.HandleSearch),
		contracts.MethodMemoryGet:         rpcdispatch.DecodeReq(s.HandleGet),
		contracts.MethodMemoryRetire:      rpcdispatch.DecodeReq(s.HandleRetire),
		contracts.MethodMemoryDelete:      rpcdispatch.DecodeReq(s.HandleDelete),
		contracts.MethodMemoryUserUpdate:  rpcdispatch.DecodeReq(s.HandleUserUpdate),
		contracts.MethodMemoryAgentUpdate: rpcdispatch.DecodeReq(s.HandleAgentUpdate),
	}, "memory")(method, payload)
}
