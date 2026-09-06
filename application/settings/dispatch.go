package settings

import (
	"encoding/json"

	"nusashell/contracts"
	"nusashell/pkg/rpcdispatch"
)

// Dispatch routes settings.* RPC methods owned by this package.
// Pets methods stay on the root dispatcher (settings.pets_* on the wire);
// TTS/STT install methods route to application/media.
func (s *Service) Dispatch(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	if s == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "settings not available"}
	}
	return rpcdispatch.Table(map[string]rpcdispatch.Handler{
		contracts.MethodSettingsGet: rpcdispatch.NoPayload(s.HandleGet),
		contracts.MethodSettingsSet: rpcdispatch.DecodeReq(s.HandleSet),
	}, "settings")(method, payload)
}
