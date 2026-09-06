package provider

import (
	"encoding/json"

	"nusashell/contracts"
	"nusashell/pkg/rpcdispatch"
)

// Dispatch routes ai.* RPC methods (providers, models) to their handlers.
func (s *Service) Dispatch(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	if s == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "provider runtime not available"}
	}
	return rpcdispatch.Table(map[string]rpcdispatch.Handler{
		contracts.MethodProvidersList:   rpcdispatch.NoPayload(s.HandleList),
		contracts.MethodProvidersSave:   rpcdispatch.DecodeReq(s.HandleSave),
		contracts.MethodProvidersDelete: rpcdispatch.DecodeReq(s.HandleDelete),
		contracts.MethodProvidersTest:   rpcdispatch.DecodeReq(s.HandleTest),
		contracts.MethodProvidersImport: rpcdispatch.DecodeReq(s.HandleImport),
		contracts.MethodModelsList:      rpcdispatch.NoPayload(s.HandleModelsList),
		contracts.MethodModelsEndpoints: rpcdispatch.DecodeReq(s.HandleModelEndpoints),
	}, "ai")(method, payload)
}
