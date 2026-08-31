package application

import (
	"encoding/json"

	"nusashell/contracts"
)

// dispatchPlugin routes plugin.* RPC methods to their handlers. Called by
// App.Dispatch for any method whose first segment is "plugin".
func (a *App) dispatchPlugin(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	return tableDispatcher(map[string]rpcHandler{
		contracts.MethodPluginList:          noPayload(a.handlePluginList),
		contracts.MethodPluginSave:          decodeReq(a.handlePluginSave),
		contracts.MethodPluginDelete:        decodeReq(a.handlePluginDelete),
		contracts.MethodPluginTest:          decodeReq(a.handlePluginTest),
		contracts.MethodPluginStop:          decodeReq(a.handlePluginStop),
		contracts.MethodPluginToolsList:     noPayload(a.handlePluginToolsList),
		contracts.MethodPluginCatalog:       noPayload(a.handlePluginCatalog),
		contracts.MethodPluginInstall:       decodeReq(a.handlePluginInstall),
		contracts.MethodPluginUninstall:     decodeReq(a.handlePluginUninstall),
		contracts.MethodPluginCheckUpdates:  noPayload(a.handlePluginCheckUpdates),
		contracts.MethodPluginSetAutoUpdate: decodeReq(a.handlePluginSetAutoUpdate),
		contracts.MethodPluginSetAutoStart:  decodeReq(a.handlePluginSetAutoStart),
		contracts.MethodPluginUpdate:        decodeReq(a.handlePluginUpdate),
	}, "plugin")(method, payload)
}
