package application

import (
	"time"

	"nusashell/application/memory"
	"nusashell/contracts"
	"nusashell/domain"
	clock "nusashell/pkg/time"
)

type MemoryService = memory.Service

var NewMemoryService = memory.NewMemoryService

const taskMemoryAnnounceType = memory.TaskMemoryAnnounceType

var (
	taskMemoryQuery      = memory.TaskMemoryQuery
	taskMemoryAlphaWords = memory.TaskMemoryAlphaWords
	truncateUTF8         = memory.TruncateUTF8
)

var clockNow = func() time.Time {
	return clock.NewTime().Time()
}

func (a *App) handleMemoryList() (any, *contracts.RPCError) {
	return a.memoryService().HandleList()
}

func (a *App) handleMemorySearch(req contracts.MemorySearchRequest) (any, *contracts.RPCError) {
	return a.memoryService().HandleSearch(req)
}

func (a *App) handleMemoryGet(req contracts.MemoryIDRequest) (any, *contracts.RPCError) {
	return a.memoryService().HandleGet(req)
}

func (a *App) handleMemoryRetire(req contracts.MemoryIDRequest) (any, *contracts.RPCError) {
	return a.memoryService().HandleRetire(req)
}

func (a *App) handleMemoryDelete(req contracts.MemoryIDRequest) (any, *contracts.RPCError) {
	return a.memoryService().HandleDelete(req)
}

func (a *App) handleMemoryUserUpdate(req contracts.MemoryUserUpdateRequest) (any, *contracts.RPCError) {
	return a.memoryService().HandleUserUpdate(req)
}

func (a *App) handleMemoryAgentUpdate(req contracts.MemoryAgentUpdateRequest) (any, *contracts.RPCError) {
	return a.memoryService().HandleAgentUpdate(req)
}

func (a *App) emitMemoryUpdated() {
	if a.Bus != nil {
		a.Bus.Emit(contracts.EventMemoryUpdated, map[string]any{"source": "rpc"})
	}
}

func (a *App) maybeAnnounceTaskMemory(conversationID string, conversation *domain.Conversation) {
	a.memoryService().MaybeAnnounceTaskMemory(conversationID, conversation, clockNow)
}
