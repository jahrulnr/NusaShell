package plugins

import (
	"encoding/json"

	"nusashell/contracts"
	"nusashell/pkg/rpcdispatch"
)

// Dispatch routes plugin.* RPC methods to their handlers.
func (s *Service) Dispatch(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	if s == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "plugin runtime not available"}
	}
	return rpcdispatch.Table(map[string]rpcdispatch.Handler{
		contracts.MethodPluginList:          rpcdispatch.NoPayload(s.handleList),
		contracts.MethodPluginSave:          rpcdispatch.DecodeReq(s.handleSave),
		contracts.MethodPluginDelete:        rpcdispatch.DecodeReq(s.handleDelete),
		contracts.MethodPluginTest:          rpcdispatch.DecodeReq(s.handleTest),
		contracts.MethodPluginStop:          rpcdispatch.DecodeReq(s.handleStop),
		contracts.MethodPluginToolsList:     rpcdispatch.NoPayload(s.handleToolsList),
		contracts.MethodPluginCatalog:       rpcdispatch.NoPayload(s.handleCatalog),
		contracts.MethodPluginInstall:       rpcdispatch.DecodeReq(s.handleInstall),
		contracts.MethodPluginUninstall:     rpcdispatch.DecodeReq(s.handleUninstall),
		contracts.MethodPluginCheckUpdates:  rpcdispatch.NoPayload(s.handleCheckUpdates),
		contracts.MethodPluginSetAutoUpdate: rpcdispatch.DecodeReq(s.handleSetAutoUpdate),
		contracts.MethodPluginSetAutoStart:  rpcdispatch.DecodeReq(s.handleSetAutoStart),
		contracts.MethodPluginUpdate:        rpcdispatch.DecodeReq(s.handleUpdate),
	}, "plugin")(method, payload)
}
