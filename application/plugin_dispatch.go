package application

import (
	"encoding/json"

	"nusashell/contracts"
)

// dispatchPlugin routes plugin.* RPC methods to their handlers. Called by
// App.Dispatch for any method whose first segment is "plugin".
func (a *App) dispatchPlugin(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	switch method {
	case contracts.MethodPluginList:
		return a.handlePluginList()
	case contracts.MethodPluginSave:
		var req contracts.PluginSaveRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handlePluginSave(req)
	case contracts.MethodPluginDelete:
		var req contracts.PluginIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handlePluginDelete(req)
	case contracts.MethodPluginTest:
		var req contracts.PluginIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handlePluginTest(req)
	case contracts.MethodPluginStop:
		var req contracts.PluginIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handlePluginStop(req)
	case contracts.MethodPluginToolsList:
		return a.handlePluginToolsList()
	case contracts.MethodPluginCatalog:
		return a.handlePluginCatalog()
	case contracts.MethodPluginInstall:
		var req contracts.PluginInstallRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handlePluginInstall(req)
	case contracts.MethodPluginUninstall:
		var req contracts.PluginIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handlePluginUninstall(req)
	case contracts.MethodPluginCheckUpdates:
		return a.handlePluginCheckUpdates()
	case contracts.MethodPluginSetAutoUpdate:
		var req contracts.PluginSetFlagRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handlePluginSetAutoUpdate(req)
	case contracts.MethodPluginSetAutoStart:
		var req contracts.PluginSetFlagRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handlePluginSetAutoStart(req)
	case contracts.MethodPluginUpdate:
		var req contracts.PluginIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handlePluginUpdate(req)
	default:
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "unknown plugin method: " + method}
	}
}
