package application

import (
	"encoding/json"

	"nusashell/contracts"
)

// dispatchSettings routes settings.* RPC methods. Get/set live in the
// settings package; pets methods in pets; TTS/STT install in media.
func (a *App) dispatchSettings(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	switch method {
	case contracts.MethodPetsStatus, contracts.MethodPetsInstallStart, contracts.MethodPetsLaunch:
		return a.petsService().Dispatch(method, payload)
	case contracts.MethodSettingsTTSInstallStatus, contracts.MethodSettingsTTSInstallStart,
		contracts.MethodSettingsSTTInstallStatus, contracts.MethodSettingsSTTInstallStart,
		contracts.MethodSettingsSTTInstallCancel:
		return a.mediaService().Dispatch(method, payload)
	default:
		return a.settingsService().Dispatch(method, payload)
	}
}
