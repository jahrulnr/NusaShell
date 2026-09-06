package pets

import (
	"encoding/json"

	"nusashell/contracts"
	"nusashell/pkg/rpcdispatch"
)

// Dispatch routes settings.pets_* RPC methods.
func (s *Service) Dispatch(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	if s == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "pets not available"}
	}
	return rpcdispatch.Table(map[string]rpcdispatch.Handler{
		contracts.MethodPetsStatus:       rpcdispatch.NoPayload(s.HandleStatus),
		contracts.MethodPetsInstallStart: rpcdispatch.DecodeReq(s.HandleInstallStart),
		contracts.MethodPetsLaunch:       rpcdispatch.NoPayload(s.HandleLaunch),
	}, "settings")(method, payload)
}
