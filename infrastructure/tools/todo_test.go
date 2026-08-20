package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"nusashell/application"
	"nusashell/domain"
)

// stubTodoPort is a minimal in-memory ConversationTodoPort for tool tests.
type stubTodoPort struct {
	items map[string][]domain.TodoItem
	goals map[string]string
}

func (s *stubTodoPort) Get(convID string) []domain.TodoItem {
	return s.items[convID]
}
func (s *stubTodoPort) GetGoal(convID string) string {
	if s.goals == nil {
		return ""
	}
	return s.goals[convID]
}
func (s *stubTodoPort) Set(convID string, items []domain.TodoItem) {
	if s.items == nil {
		s.items = map[string][]domain.TodoItem{}
	}
	s.items[convID] = items
}
func (s *stubTodoPort) SetGoal(convID string, goal string) {
	if s.goals == nil {
		s.goals = map[string]string{}
	}
	s.goals[convID] = goal
}
func (s *stubTodoPort) Clear(convID string) {
	delete(s.items, convID)
	delete(s.goals, convID)
}

func TestExecTodoWithGoal(t *testing.T) {
	todoPort := &stubTodoPort{}
	toolbox := &Toolbox{Todos: todoPort}
	ctx := application.WithConversationID(context.Background(), "conv_1")

	args, _ := json.Marshal(map[string]any{
		"items": []map[string]any{
			{"id": "1", "content": "Step 1", "status": "in_progress"},
			{"id": "2", "content": "Step 2", "status": "pending"},
		},
		"goal": "Build a REST API with Go and Clean Architecture.",
	})

	result, err := toolbox.execTodo(ctx, args)
	if err != nil {
		t.Fatalf("execTodo failed: %v", err)
	}

	if !strings.Contains(result, "Build a REST API with Go and Clean Architecture.") {
		t.Errorf("goal missing in result, got: %s", result)
	}
	if !strings.Contains(result, "goal: Build a REST API") {
		t.Errorf("expected goal field in meta, got: %s", result)
	}
	if !strings.Contains(result, `"status":"in_progress"`) {
		t.Errorf("expected JSONL item with in_progress status, got: %s", result)
	}
	if todoPort.GetGoal("conv_1") != "Build a REST API with Go and Clean Architecture." {
		t.Errorf("goal not persisted in port")
	}
	items := todoPort.Get("conv_1")
	if len(items) != 2 {
		t.Errorf("expected 2 items, got %d", len(items))
	}
}

func TestExecTodoGoalPreservedOnItemsOnlyUpdate(t *testing.T) {
	todoPort := &stubTodoPort{
		goals: map[string]string{"conv_1": "Original goal"},
	}
	toolbox := &Toolbox{Todos: todoPort}
	ctx := application.WithConversationID(context.Background(), "conv_1")

	// Update items without setting goal — goal should be preserved.
	args, _ := json.Marshal(map[string]any{
		"items": []map[string]any{
			{"id": "1", "content": "Step 1 done", "status": "completed"},
		},
	})

	_, err := toolbox.execTodo(ctx, args)
	if err != nil {
		t.Fatalf("execTodo failed: %v", err)
	}

	if todoPort.GetGoal("conv_1") != "Original goal" {
		t.Errorf("goal should be preserved, got %q", todoPort.GetGoal("conv_1"))
	}
}

func TestExecTodoGoalTooLong(t *testing.T) {
	todoPort := &stubTodoPort{}
	toolbox := &Toolbox{Todos: todoPort}
	ctx := application.WithConversationID(context.Background(), "conv_1")

	longGoal := strings.Repeat("x", todoMaxGoalChars+1)
	args, _ := json.Marshal(map[string]any{
		"items": []map[string]any{
			{"id": "1", "content": "Step 1", "status": "pending"},
		},
		"goal": longGoal,
	})

	_, err := toolbox.execTodo(ctx, args)
	if err == nil {
		t.Fatal("expected error for goal exceeding max chars")
	}
	if !strings.Contains(err.Error(), "goal exceeds") {
		t.Errorf("expected goal exceeds error, got: %v", err)
	}
}

func TestExecTodoEmptyItemsWithGoal(t *testing.T) {
	todoPort := &stubTodoPort{}
	toolbox := &Toolbox{Todos: todoPort}
	ctx := application.WithConversationID(context.Background(), "conv_1")

	args, _ := json.Marshal(map[string]any{
		"items": []map[string]any{},
		"goal":  "Just a goal, no steps yet.",
	})

	result, err := toolbox.execTodo(ctx, args)
	if err != nil {
		t.Fatalf("execTodo failed: %v", err)
	}

	if !strings.Contains(result, "Just a goal, no steps yet.") {
		t.Errorf("goal missing in result, got: %s", result)
	}
	if !strings.Contains(result, "goal: Just a goal") {
		t.Errorf("expected goal field in meta, got: %s", result)
	}
	if todoPort.GetGoal("conv_1") != "Just a goal, no steps yet." {
		t.Errorf("goal not persisted")
	}
}
