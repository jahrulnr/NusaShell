package application

import (
	"nusashell/application/internal/service/todosconv"
	"nusashell/contracts"
	"nusashell/domain"
)

func (a *App) handleTodosGet(req contracts.TodosGetRequest) (any, *contracts.RPCError) {
	if a.Todos == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "todo tracking is not available"}
	}
	if req.ConversationID == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "conversation_id is required"}
	}
	items := a.Todos.Get(req.ConversationID)
	dtos := make([]contracts.TodoItemDTO, 0, len(items))
	for _, item := range items {
		dtos = append(dtos, todosconv.TodoItemDTO(item))
	}
	return contracts.TodosGetResult{
		ConversationID: req.ConversationID,
		Items:          dtos,
		Summary:        todosconv.TodoSummaryDTO(domain.SummarizeTodos(items)),
		Brief:          a.Todos.GetBrief(req.ConversationID),
	}, nil
}

func (a *App) handleTodosDelete(req contracts.TodosDeleteRequest) (any, *contracts.RPCError) {
	if a.Todos == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "todo tracking is not available"}
	}
	if req.ConversationID == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "conversation_id is required"}
	}
	current := a.Todos.Get(req.ConversationID)
	toDelete := make(map[string]bool, len(req.IDs))
	for _, id := range req.IDs {
		if id != "" {
			toDelete[id] = true
		}
	}
	remaining := make([]domain.TodoItem, 0, len(current))
	for _, item := range current {
		if !toDelete[item.ID] {
			remaining = append(remaining, item)
		}
	}
	a.Todos.Set(req.ConversationID, remaining)
	dtos := make([]contracts.TodoItemDTO, 0, len(remaining))
	for _, item := range remaining {
		dtos = append(dtos, todosconv.TodoItemDTO(item))
	}
	return contracts.TodosGetResult{
		ConversationID: req.ConversationID,
		Items:          dtos,
		Summary:        todosconv.TodoSummaryDTO(domain.SummarizeTodos(remaining)),
	}, nil
}
