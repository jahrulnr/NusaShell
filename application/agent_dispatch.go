package application

import (
	"context"
	"encoding/json"

	"nusashell/contracts"
)

// dispatchAgent routes agent.* RPC methods (conversations, turns, ask,
// todos) to their handlers. Called by App.Dispatch for any method whose
// first segment is "agent".
func (a *App) dispatchAgent(ctx context.Context, method string, payload json.RawMessage) (any, *contracts.RPCError) {
	switch method {
	case contracts.MethodToolContracts:
		var req contracts.ToolContractsRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleToolContracts(req)
	case contracts.MethodConversationsList:
		return a.handleConversationsList()
	case contracts.MethodConversationsCreate:
		var req contracts.ConversationCreateRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleConversationsCreate(req)
	case contracts.MethodConversationsGet:
		var req contracts.ConversationIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleConversationsGet(req)
	case contracts.MethodConversationsChunk:
		var req contracts.ConversationChunkRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleConversationsChunk(req)
	case contracts.MethodConversationsRename:
		var req contracts.ConversationRenameRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleConversationsRename(req)
	case contracts.MethodConversationsDelete:
		var req contracts.ConversationIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleConversationsDelete(req)
	case contracts.MethodConversationsPickWorkspace:
		var req contracts.ConversationIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleConversationsPickWorkspace(req)
	case contracts.MethodTurnsStart:
		var req contracts.TurnStartRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleTurnsStart(ctx, req)
	case contracts.MethodTurnsStop:
		var req contracts.TurnStopRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleTurnsStop(req)
	case contracts.MethodToolStop:
		var req contracts.ToolStopRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleToolStop(req)
	case contracts.MethodTurnsRetry:
		var req contracts.TurnRetryRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleTurnsRetry(ctx, req)
	case contracts.MethodTurnsSteer:
		var req contracts.TurnSteerRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleTurnsSteer(ctx, req)
	case contracts.MethodTurnsCancelSteer:
		var req contracts.TurnCancelSteerRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleTurnsCancelSteer(req)
	case contracts.MethodTurnsActive:
		var req contracts.ConversationIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleTurnsActive(req)
	case contracts.MethodAskAnswer:
		var req contracts.AskAnswerRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleAskAnswer(req)
	case contracts.MethodAskCancel:
		var req contracts.AskCancelRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleAskCancel(req)
	case contracts.MethodAskPending:
		var req contracts.AskPendingListRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleAskPendingList(req)
	case contracts.MethodTodosGet:
		var req contracts.TodosGetRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleTodosGet(req)
	case contracts.MethodTodosDelete:
		var req contracts.TodosDeleteRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleTodosDelete(req)
	default:
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "unknown agent method: " + method}
	}
}
