package application

import (
	"encoding/json"

	"nusashell/contracts"
)

// dispatchSettings routes settings.* RPC methods to their handlers. Called
// by App.Dispatch for any method whose first segment is "settings".
func (a *App) dispatchSettings(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	switch method {
	case contracts.MethodSettingsGet:
		return a.handleSettingsGet()
	case contracts.MethodSettingsSet:
		var req contracts.SettingsSetRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleSettingsSet(req)
	case contracts.MethodSettingsTTSInstallStatus:
		return a.handleTTSSettingsInstallStatus()
	case contracts.MethodSettingsTTSInstallStart:
		var req contracts.TTSInstallStartRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleTTSSettingsInstallStart(req)
	case contracts.MethodSettingsSTTInstallStatus:
		return a.handleSTTSettingsInstallStatus()
	case contracts.MethodSettingsSTTInstallStart:
		var req contracts.STTInstallStartRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleSTTSettingsInstallStart(req)
	case contracts.MethodSettingsSTTInstallCancel:
		return a.handleSTTSettingsInstallCancel()
	default:
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "unknown settings method: " + method}
	}
}
