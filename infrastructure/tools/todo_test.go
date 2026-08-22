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
	items  map[string][]domain.TodoItem
	briefs map[string]string
}

func (s *stubTodoPort) Get(convID string) []domain.TodoItem {
	return s.items[convID]
}
func (s *stubTodoPort) GetBrief(convID string) string {
	if s.briefs == nil {
		return ""
	}
	return s.briefs[convID]
}
func (s *stubTodoPort) Set(convID string, items []domain.TodoItem) {
	if s.items == nil {
		s.items = map[string][]domain.TodoItem{}
	}
	s.items[convID] = items
}
func (s *stubTodoPort) SetBrief(convID string, brief string) {
	if s.briefs == nil {
		s.briefs = map[string]string{}
	}
	s.briefs[convID] = brief
}
func (s *stubTodoPort) Clear(convID string) {
	delete(s.items, convID)
	delete(s.briefs, convID)
}
func (s *stubTodoPort) Patch(convID string, patches []domain.TodoItem) {
	if s.items == nil {
		s.items = map[string][]domain.TodoItem{}
	}
	existing := s.items[convID]
	byID := make(map[string]int, len(existing))
	for i, item := range existing {
		byID[item.ID] = i
	}
	for _, p := range patches {
		if idx, ok := byID[p.ID]; ok {
			existing[idx].Status = p.Status
			if p.Content != "" {
				existing[idx].Content = p.Content
			}
		} else {
			existing = append(existing, p)
			byID[p.ID] = len(existing) - 1
		}
	}
	s.items[convID] = existing
}

func TestExecTodoWithBrief(t *testing.T) {
	todoPort := &stubTodoPort{}
	toolbox := &Toolbox{Todos: todoPort}
	ctx := application.WithConversationID(context.Background(), "conv_1")

	brief := "## Objective\nBuild a REST API with Go and Clean Architecture.\n\n## Done when\nAPI serves endpoints; tests pass."
	args, _ := json.Marshal(map[string]any{
		"items": []map[string]any{
			{"id": "1", "content": "Step 1", "status": "in_progress"},
			{"id": "2", "content": "Step 2", "status": "pending"},
		},
		"brief": brief,
	})

	result, err := toolbox.execTodo(ctx, args)
	if err != nil {
		t.Fatalf("execTodo failed: %v", err)
	}

	// Tool result is a compact acknowledgment (summary counts only); the
	// full items and brief are NOT echoed back to save tokens. The agent
	// just sent them, and the UI gets the full list via agent.todo.updated.
	if !strings.Contains(result, "ok: true") {
		t.Errorf("expected ok: true in result, got: %s", result)
	}
	if !strings.Contains(result, "total: 2") {
		t.Errorf("expected total: 2 in result, got: %s", result)
	}
	if strings.Contains(result, "Build a REST API with Go and Clean Architecture.") {
		t.Errorf("brief must not be echoed in result, got: %s", result)
	}
	if strings.Contains(result, `"status":"in_progress"`) {
		t.Errorf("items must not be echoed as JSONL in result, got: %s", result)
	}
	if todoPort.GetBrief("conv_1") != brief {
		t.Errorf("brief not persisted in port")
	}
	items := todoPort.Get("conv_1")
	if len(items) != 2 {
		t.Errorf("expected 2 items, got %d", len(items))
	}
}

func TestExecTodoBriefPreservedOnItemsOnlyUpdate(t *testing.T) {
	todoPort := &stubTodoPort{
		briefs: map[string]string{"conv_1": "## Objective\nOriginal\n\n## Done when\nDone"},
	}
	toolbox := &Toolbox{Todos: todoPort}
	ctx := application.WithConversationID(context.Background(), "conv_1")

	// Update items without setting brief — brief should be preserved.
	args, _ := json.Marshal(map[string]any{
		"items": []map[string]any{
			{"id": "1", "content": "Step 1 done", "status": "completed"},
		},
	})

	_, err := toolbox.execTodo(ctx, args)
	if err != nil {
		t.Fatalf("execTodo failed: %v", err)
	}

	if todoPort.GetBrief("conv_1") != "## Objective\nOriginal\n\n## Done when\nDone" {
		t.Errorf("brief should be preserved, got %q", todoPort.GetBrief("conv_1"))
	}
}

func TestExecTodoBriefTooLong(t *testing.T) {
	todoPort := &stubTodoPort{}
	toolbox := &Toolbox{Todos: todoPort}
	ctx := application.WithConversationID(context.Background(), "conv_1")

	longBrief := "## Objective\n" + strings.Repeat("x", todoMaxBriefChars+1) + "\n\n## Done when\nD"
	args, _ := json.Marshal(map[string]any{
		"items": []map[string]any{
			{"id": "1", "content": "Step 1", "status": "pending"},
		},
		"brief": longBrief,
	})

	_, err := toolbox.execTodo(ctx, args)
	if err == nil {
		t.Fatal("expected error for brief exceeding max chars")
	}
	if !strings.Contains(err.Error(), "brief exceeds") {
		t.Errorf("expected brief exceeds error, got: %v", err)
	}
}

func TestExecTodoEmptyItemsWithBrief(t *testing.T) {
	todoPort := &stubTodoPort{}
	toolbox := &Toolbox{Todos: todoPort}
	ctx := application.WithConversationID(context.Background(), "conv_1")

	brief := "## Objective\nJust a brief, no steps yet.\n\n## Done when\nN/A"
	args, _ := json.Marshal(map[string]any{
		"items": []map[string]any{},
		"brief": brief,
	})

	result, err := toolbox.execTodo(ctx, args)
	if err != nil {
		t.Fatalf("execTodo failed: %v", err)
	}

	// Result is a compact ack; brief is not echoed (saves tokens).
	if !strings.Contains(result, "ok: true") {
		t.Errorf("expected ok: true in result, got: %s", result)
	}
	if strings.Contains(result, "Just a brief, no steps yet.") {
		t.Errorf("brief must not be echoed in result, got: %s", result)
	}
	if todoPort.GetBrief("conv_1") != brief {
		t.Errorf("brief not persisted")
	}
}

func TestExecTodoWithBriefStructuredSections(t *testing.T) {
	todoPort := &stubTodoPort{}
	toolbox := &Toolbox{Todos: todoPort}
	ctx := application.WithConversationID(context.Background(), "conv_1")

	brief := "## Objective\nFix the login bug reported in conv_abc.\n\n## Approach\nReproduce → trace auth flow → patch root cause → verify.\n\n## Done when\nLogin succeeds for user X; no regression in auth tests."
	args, _ := json.Marshal(map[string]any{
		"items": []map[string]any{
			{"id": "1", "content": "Reproduce bug", "status": "in_progress"},
			{"id": "2", "content": "Patch root cause", "status": "pending"},
		},
		"brief": brief,
	})

	result, err := toolbox.execTodo(ctx, args)
	if err != nil {
		t.Fatalf("execTodo with brief failed: %v", err)
	}
	// Result is a compact ack; brief content is not echoed (saves tokens).
	if !strings.Contains(result, "ok: true") {
		t.Errorf("expected ok: true in result, got: %s", result)
	}
	if strings.Contains(result, "Objective") {
		t.Errorf("brief must not be echoed in result, got: %s", result)
	}
	if todoPort.GetBrief("conv_1") != brief {
		t.Errorf("brief not persisted in port, got: %q", todoPort.GetBrief("conv_1"))
	}
}

func TestExecTodoBriefRejectMissingObjective(t *testing.T) {
	todoPort := &stubTodoPort{}
	toolbox := &Toolbox{Todos: todoPort}
	ctx := application.WithConversationID(context.Background(), "conv_1")

	// Brief has Done when but no Objective — must be rejected.
	brief := "## Done when\nLogin works."
	args, _ := json.Marshal(map[string]any{
		"items": []map[string]any{
			{"id": "1", "content": "Step 1", "status": "pending"},
		},
		"brief": brief,
	})

	_, err := toolbox.execTodo(ctx, args)
	if err == nil {
		t.Fatal("expected error for brief missing ## Objective")
	}
	if !strings.Contains(err.Error(), "Objective") {
		t.Errorf("expected Objective error, got: %v", err)
	}
}

func TestExecTodoBriefRejectMissingDoneWhen(t *testing.T) {
	todoPort := &stubTodoPort{}
	toolbox := &Toolbox{Todos: todoPort}
	ctx := application.WithConversationID(context.Background(), "conv_1")

	// Brief has Objective but no Done when — must be rejected.
	brief := "## Objective\nFix the login bug."
	args, _ := json.Marshal(map[string]any{
		"items": []map[string]any{
			{"id": "1", "content": "Step 1", "status": "pending"},
		},
		"brief": brief,
	})

	_, err := toolbox.execTodo(ctx, args)
	if err == nil {
		t.Fatal("expected error for brief missing ## Done when")
	}
	if !strings.Contains(err.Error(), "Done when") {
		t.Errorf("expected Done when error, got: %v", err)
	}
}

// Backward compat: legacy `goal` arg still accepted, mapped to brief internally.
func TestExecTodoLegacyGoalArgMapsToBrief(t *testing.T) {
	todoPort := &stubTodoPort{}
	toolbox := &Toolbox{Todos: todoPort}
	ctx := application.WithConversationID(context.Background(), "conv_1")

	args, _ := json.Marshal(map[string]any{
		"items": []map[string]any{
			{"id": "1", "content": "Step 1", "status": "pending"},
		},
		"goal": "## Objective\nLegacy goal arg\n\n## Done when\nDone",
	})

	_, err := toolbox.execTodo(ctx, args)
	if err != nil {
		t.Fatalf("legacy goal arg should still work: %v", err)
	}
	if todoPort.GetBrief("conv_1") != "## Objective\nLegacy goal arg\n\n## Done when\nDone" {
		t.Errorf("legacy goal should map to brief, got %q", todoPort.GetBrief("conv_1"))
	}
}

// Patch mode: update status of existing item by ID without re-emitting full list.
func TestExecTodoPatchUpdatesStatusByID(t *testing.T) {
	todoPort := &stubTodoPort{
		items: map[string][]domain.TodoItem{"conv_1": {
			{ID: "1", Content: "Design API", Status: domain.TodoCompleted},
			{ID: "2", Content: "Implement handler", Status: domain.TodoInProgress},
			{ID: "3", Content: "Write tests", Status: domain.TodoPending},
		}},
	}
	toolbox := &Toolbox{Todos: todoPort}
	ctx := application.WithConversationID(context.Background(), "conv_1")

	// Patch: mark item 2 as completed, leave others unchanged.
	args, _ := json.Marshal(map[string]any{
		"mode": "patch",
		"items": []map[string]any{
			{"id": "2", "status": "completed"},
		},
	})
	_, err := toolbox.execTodo(ctx, args)
	if err != nil {
		t.Fatalf("patch should succeed: %v", err)
	}
	items := todoPort.Get("conv_1")
	if len(items) != 3 {
		t.Fatalf("patch should keep all items, got %d", len(items))
	}
	if items[1].Status != domain.TodoCompleted {
		t.Errorf("item 2 status should be completed, got %s", items[1].Status)
	}
	if items[1].Content != "Implement handler" {
		t.Errorf("item 2 content should be preserved, got %q", items[1].Content)
	}
	if items[2].Status != domain.TodoPending {
		t.Errorf("item 3 should be unchanged (pending), got %s", items[2].Status)
	}
}

// Patch mode: content empty = keep existing content.
func TestExecTodoPatchEmptyContentKeepsExisting(t *testing.T) {
	todoPort := &stubTodoPort{
		items: map[string][]domain.TodoItem{"conv_1": {
			{ID: "1", Content: "Original content", Status: domain.TodoPending},
		}},
	}
	toolbox := &Toolbox{Todos: todoPort}
	ctx := application.WithConversationID(context.Background(), "conv_1")

	args, _ := json.Marshal(map[string]any{
		"mode": "patch",
		"items": []map[string]any{
			{"id": "1", "content": "", "status": "in_progress"},
		},
	})
	_, err := toolbox.execTodo(ctx, args)
	if err != nil {
		t.Fatalf("patch with empty content should succeed: %v", err)
	}
	items := todoPort.Get("conv_1")
	if items[0].Content != "Original content" {
		t.Errorf("content should be preserved, got %q", items[0].Content)
	}
	if items[0].Status != domain.TodoInProgress {
		t.Errorf("status should be updated, got %s", items[0].Status)
	}
}

// Patch mode: new ID = add new item.
func TestExecTodoPatchAddsNewItem(t *testing.T) {
	todoPort := &stubTodoPort{
		items: map[string][]domain.TodoItem{"conv_1": {
			{ID: "1", Content: "Existing", Status: domain.TodoPending},
		}},
	}
	toolbox := &Toolbox{Todos: todoPort}
	ctx := application.WithConversationID(context.Background(), "conv_1")

	args, _ := json.Marshal(map[string]any{
		"mode": "patch",
		"items": []map[string]any{
			{"id": "2", "content": "New item", "status": "pending"},
		},
	})
	_, err := toolbox.execTodo(ctx, args)
	if err != nil {
		t.Fatalf("patch with new item should succeed: %v", err)
	}
	items := todoPort.Get("conv_1")
	if len(items) != 2 {
		t.Fatalf("should have 2 items after patch, got %d", len(items))
	}
	if items[1].ID != "2" || items[1].Content != "New item" {
		t.Errorf("new item not added correctly: %+v", items[1])
	}
}

// Patch mode: brief is preserved when not sent.
func TestExecTodoPatchPreservesBrief(t *testing.T) {
	todoPort := &stubTodoPort{
		briefs: map[string]string{"conv_1": "## Objective\nOriginal\n\n## Done when\nDone"},
		items:  map[string][]domain.TodoItem{"conv_1": {{ID: "1", Content: "Task", Status: domain.TodoPending}}},
	}
	toolbox := &Toolbox{Todos: todoPort}
	ctx := application.WithConversationID(context.Background(), "conv_1")

	args, _ := json.Marshal(map[string]any{
		"mode": "patch",
		"items": []map[string]any{
			{"id": "1", "status": "completed"},
		},
	})
	_, err := toolbox.execTodo(ctx, args)
	if err != nil {
		t.Fatalf("patch should succeed: %v", err)
	}
	if todoPort.GetBrief("conv_1") != "## Objective\nOriginal\n\n## Done when\nDone" {
		t.Errorf("brief should be preserved, got %q", todoPort.GetBrief("conv_1"))
	}
}

// Replace mode (default): empty content should still be rejected.
func TestExecTodoReplaceModeRejectsEmptyContent(t *testing.T) {
	todoPort := &stubTodoPort{}
	toolbox := &Toolbox{Todos: todoPort}
	ctx := application.WithConversationID(context.Background(), "conv_1")

	args, _ := json.Marshal(map[string]any{
		"items": []map[string]any{
			{"id": "1", "content": "", "status": "pending"},
		},
	})
	_, err := toolbox.execTodo(ctx, args)
	if err == nil {
		t.Fatal("replace mode should reject empty content")
	}
}
