package media

import (
	"encoding/json"

	"nusashell/contracts"
	"nusashell/pkg/rpcdispatch"
)

// Dispatch routes settings TTS/STT install RPC methods.
func (s *Service) Dispatch(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	if s == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "media not available"}
	}
	return rpcdispatch.Table(map[string]rpcdispatch.Handler{
		contracts.MethodSettingsTTSInstallStatus: rpcdispatch.NoPayload(s.HandleTTSInstallStatus),
		contracts.MethodSettingsTTSInstallStart:  rpcdispatch.DecodeReq(s.HandleTTSInstallStart),
		contracts.MethodSettingsSTTInstallStatus: rpcdispatch.NoPayload(s.HandleSTTInstallStatus),
		contracts.MethodSettingsSTTInstallStart:  rpcdispatch.DecodeReq(s.HandleSTTInstallStart),
		contracts.MethodSettingsSTTInstallCancel: rpcdispatch.NoPayload(s.HandleSTTInstallCancel),
	}, "settings")(method, payload)
}
