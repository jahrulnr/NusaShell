// Package todosconv converts domain todo values into wire-level DTOs.
// Extracted from the application root so the todo handlers depend on a
// small leaf package instead of the whole application package.
package todosconv

import (
	"nusashell/contracts"
	"nusashell/domain"
)

func TodoItemDTO(item domain.TodoItem) contracts.TodoItemDTO {
	return contracts.TodoItemDTO{
		ID:      item.ID,
		Content: item.Content,
		Status:  string(item.Status),
	}
}

func TodoSummaryDTO(s domain.TodoSummary) contracts.TodoSummaryDTO {
	return contracts.TodoSummaryDTO{
		Total:      s.Total,
		Pending:    s.Pending,
		InProgress: s.InProgress,
		Completed:  s.Completed,
	}
}
