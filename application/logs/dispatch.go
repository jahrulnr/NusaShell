package logs

import (
	"encoding/json"

	"nusashell/contracts"
	"nusashell/pkg/rpcdispatch"
)

// Dispatch routes logs.* RPC methods to their handlers.
func (s *Service) Dispatch(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	if s == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "log store not available"}
	}
	return rpcdispatch.Table(map[string]rpcdispatch.Handler{
		contracts.MethodLogsList:  rpcdispatch.DecodeReq(s.handleList),
		contracts.MethodLogsClear: rpcdispatch.NoPayload(s.handleClear),
	}, "logs")(method, payload)
}
