package domain

import "testing"

func TestIsValidTodoStatus(t *testing.T) {
	cases := []struct {
		status TodoStatus
		want   bool
	}{
		{TodoPending, true},
		{TodoInProgress, true},
		{TodoCompleted, true},
		{TodoStatus("done"), false},
		{TodoStatus(""), false},
		{TodoStatus("PENDING"), false}, // case-sensitive
	}
	for _, c := range cases {
		if got := IsValidTodoStatus(c.status); got != c.want {
			t.Errorf("IsValidTodoStatus(%q) = %v, want %v", c.status, got, c.want)
		}
	}
}

func TestSummarizeTodos(t *testing.T) {
	items := []TodoItem{
		{ID: "1", Content: "a", Status: TodoCompleted},
		{ID: "2", Content: "b", Status: TodoInProgress},
		{ID: "3", Content: "c", Status: TodoPending},
		{ID: "4", Content: "d", Status: TodoPending},
	}
	s := SummarizeTodos(items)
	if s.Total != 4 {
		t.Errorf("Total = %d, want 4", s.Total)
	}
	if s.Completed != 1 {
		t.Errorf("Completed = %d, want 1", s.Completed)
	}
	if s.InProgress != 1 {
		t.Errorf("InProgress = %d, want 1", s.InProgress)
	}
	if s.Pending != 2 {
		t.Errorf("Pending = %d, want 2", s.Pending)
	}
}

func TestSummarizeTodosEmpty(t *testing.T) {
	s := SummarizeTodos(nil)
	if s.Total != 0 || s.Pending != 0 || s.InProgress != 0 || s.Completed != 0 {
		t.Errorf("SummarizeTodos(nil) = %+v, want all zeros", s)
	}
}

func TestCountOpenTodos(t *testing.T) {
	items := []TodoItem{
		{ID: "1", Content: "a", Status: TodoCompleted},
		{ID: "2", Content: "b", Status: TodoInProgress},
		{ID: "3", Content: "c", Status: TodoPending},
	}
	if got := CountOpenTodos(items); got != 2 {
		t.Errorf("CountOpenTodos = %d, want 2", got)
	}
}

func TestCountOpenTodosAllCompleted(t *testing.T) {
	items := []TodoItem{
		{ID: "1", Content: "a", Status: TodoCompleted},
		{ID: "2", Content: "b", Status: TodoCompleted},
	}
	if got := CountOpenTodos(items); got != 0 {
		t.Errorf("CountOpenTodos = %d, want 0", got)
	}
}
