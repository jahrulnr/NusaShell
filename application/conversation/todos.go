package conversation

import (
	"nusashell/application/service/todosconv"
	"nusashell/contracts"
	"nusashell/domain"
)

// HandleTodosGet returns the checklist and brief for a conversation.
func (s *Service) HandleTodosGet(req contracts.TodosGetRequest) (any, *contracts.RPCError) {
	if s.todos == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "todo tracking is not available"}
	}
	if req.ConversationID == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "conversation_id is required"}
	}
	items := s.todos.Get(req.ConversationID)
	dtos := make([]contracts.TodoItemDTO, 0, len(items))
	for _, item := range items {
		dtos = append(dtos, todosconv.TodoItemDTO(item))
	}
	return contracts.TodosGetResult{
		ConversationID: req.ConversationID,
		Items:          dtos,
		Summary:        todosconv.TodoSummaryDTO(domain.SummarizeTodos(items)),
		Brief:          s.todos.GetBrief(req.ConversationID),
	}, nil
}

// HandleTodosDelete removes selected items by ID.
func (s *Service) HandleTodosDelete(req contracts.TodosDeleteRequest) (any, *contracts.RPCError) {
	if s.todos == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "todo tracking is not available"}
	}
	if req.ConversationID == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "conversation_id is required"}
	}
	current := s.todos.Get(req.ConversationID)
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
	s.todos.Set(req.ConversationID, remaining)
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
