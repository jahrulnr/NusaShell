package conversation

import (
	"encoding/json"

	"nusashell/contracts"
	"nusashell/pkg/rpcdispatch"
)

// Dispatch routes agent.conversations.*, agent.todos.*, and
// agent.workspace.list-dirs. Turns and ask stay on the root agent dispatcher.
func (s *Service) Dispatch(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	if s == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "conversation runtime not available"}
	}
	return rpcdispatch.Table(map[string]rpcdispatch.Handler{
		contracts.MethodConversationsList:         rpcdispatch.NoPayload(s.HandleList),
		contracts.MethodConversationsCreate:       rpcdispatch.DecodeReq(s.HandleCreate),
		contracts.MethodConversationsGet:          rpcdispatch.DecodeReq(s.HandleGet),
		contracts.MethodConversationsChunk:        rpcdispatch.DecodeReq(s.HandleChunk),
		contracts.MethodConversationsRename:       rpcdispatch.DecodeReq(s.HandleRename),
		contracts.MethodConversationsDelete:       rpcdispatch.DecodeReq(s.HandleDelete),
		contracts.MethodConversationsSetWorkspace: rpcdispatch.DecodeReq(s.HandleSetWorkspace),
		contracts.MethodWorkspaceListDirs:         rpcdispatch.DecodeReq(s.HandleWorkspaceListDirs),
		contracts.MethodTodosGet:                  rpcdispatch.DecodeReq(s.HandleTodosGet),
		contracts.MethodTodosDelete:               rpcdispatch.DecodeReq(s.HandleTodosDelete),
	}, "agent")(method, payload)
}
