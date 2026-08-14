package domain

// TodoStatus is the lifecycle state of a conversation todo item.
type TodoStatus string

const (
	TodoPending    TodoStatus = "pending"
	TodoInProgress TodoStatus = "in_progress"
	TodoCompleted  TodoStatus = "completed"
)

// validTodoStatuses is the set of allowed todo status values. Used by both
// the tool handler (model input validation) and the RPC handler (UI input
// validation) so the rule has a single source of truth.
var validTodoStatuses = map[TodoStatus]bool{
	TodoPending:    true,
	TodoInProgress: true,
	TodoCompleted:  true,
}

// IsValidTodoStatus reports whether s is one of the allowed todo status
// values (pending, in_progress, completed).
func IsValidTodoStatus(s TodoStatus) bool {
	return validTodoStatuses[s]
}

// TodoItem is a single agent-owned checklist entry. The ID is stable across
// updates so the UI can track items as the model rewrites the full list.
type TodoItem struct {
	ID      string     `json:"id"`
	Content string     `json:"content"`
	Status  TodoStatus `json:"status"`
}

// TodoSummary holds aggregate counts derived from a todo list. Used by the
// tool result, the RPC result, and the auto-continue policy.
type TodoSummary struct {
	Total      int `json:"total"`
	Pending    int `json:"pending"`
	InProgress int `json:"in_progress"`
	Completed  int `json:"completed"`
}

// SummarizeTodos computes aggregate counts for a todo list.
func SummarizeTodos(items []TodoItem) TodoSummary {
	var s TodoSummary
	for _, item := range items {
		s.Total++
		switch item.Status {
		case TodoPending:
			s.Pending++
		case TodoInProgress:
			s.InProgress++
		case TodoCompleted:
			s.Completed++
		}
	}
	return s
}

// CountOpenTodos returns the number of items that are not yet completed
// (pending or in_progress). Used by the auto-continue policy to decide
// whether the agent still has unfinished checklist work.
func CountOpenTodos(items []TodoItem) int {
	n := 0
	for _, item := range items {
		if item.Status != TodoCompleted {
			n++
		}
	}
	return n
}
