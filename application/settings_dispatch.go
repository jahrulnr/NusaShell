package application

import (
	"encoding/json"

	"nusashell/contracts"
)

// dispatchSettings routes settings.* RPC methods to their handlers. Called
// by App.Dispatch for any method whose first segment is "settings".
func (a *App) dispatchSettings(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	return tableDispatcher(map[string]rpcHandler{
		contracts.MethodSettingsGet:              noPayload(a.handleSettingsGet),
		contracts.MethodSettingsSet:              decodeReq(a.handleSettingsSet),
		contracts.MethodSettingsTTSInstallStatus: noPayload(a.handleTTSSettingsInstallStatus),
		contracts.MethodSettingsTTSInstallStart:  decodeReq(a.handleTTSSettingsInstallStart),
		contracts.MethodSettingsSTTInstallStatus: noPayload(a.handleSTTSettingsInstallStatus),
		contracts.MethodSettingsSTTInstallStart:  decodeReq(a.handleSTTSettingsInstallStart),
		contracts.MethodSettingsSTTInstallCancel: noPayload(a.handleSTTSettingsInstallCancel),
	}, "settings")(method, payload)
}
