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
func (s *stubTodoPort) ClearBrief(convID string) error {
	delete(s.briefs, convID)
	return nil
}
func (s *stubTodoPort) PlanPath(convID string) string {
	if s.briefs[convID] == "" {
		return ""
	}
	return "/tmp/plans/" + convID + ".plan.md"
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

// Replace mode can update the status of existing items without re-emitting
// their content. This is a full list replacement, but content omitted for a
// known ID means "keep the existing description".
func TestExecTodoReplaceModePreservesExistingContentOnStatusUpdate(t *testing.T) {
	todoPort := &stubTodoPort{
		items: map[string][]domain.TodoItem{"conv_1": {
			{ID: "1", Content: "Research Hermes", Status: domain.TodoCompleted},
			{ID: "2", Content: "Research OpenClaw", Status: domain.TodoCompleted},
			{ID: "3", Content: "Compare the projects", Status: domain.TodoInProgress},
			{ID: "4", Content: "Identify strengths", Status: domain.TodoPending},
			{ID: "5", Content: "Present findings", Status: domain.TodoPending},
		}},
	}
	toolbox := &Toolbox{Todos: todoPort}
	ctx := application.WithConversationID(context.Background(), "conv_1")

	args, _ := json.Marshal(map[string]any{
		"items": []map[string]any{
			{"id": "1", "status": "completed"},
			{"id": "2", "status": "completed"},
			{"id": "3", "status": "completed"},
			{"id": "4", "status": "completed"},
			{"id": "5", "status": "completed"},
		},
		"mode": "replace",
	})
	if _, err := toolbox.execTodo(ctx, args); err != nil {
		t.Fatalf("status-only replace should preserve existing content: %v", err)
	}
	items := todoPort.Get("conv_1")
	if len(items) != 5 {
		t.Fatalf("expected 5 items, got %d", len(items))
	}
	for _, item := range items {
		if item.Status != domain.TodoCompleted {
			t.Errorf("item %s status = %s, want completed", item.ID, item.Status)
		}
		if item.Content == "" {
			t.Errorf("item %s content was lost", item.ID)
		}
	}
}

// Replace mode still requires content when an ID does not exist yet; there is
// nothing to preserve for a newly introduced item.
func TestExecTodoReplaceModeRequiresContentForNewItem(t *testing.T) {
	todoPort := &stubTodoPort{}
	toolbox := &Toolbox{Todos: todoPort}
	ctx := application.WithConversationID(context.Background(), "conv_1")

	args, _ := json.Marshal(map[string]any{
		"items": []map[string]any{{"id": "new", "status": "pending"}},
		"mode":  "replace",
	})
	if _, err := toolbox.execTodo(ctx, args); err == nil {
		t.Fatal("replace mode should require content for a new item")
	}
}

func TestTodoToolSchemaAllowsStatusOnlyUpdates(t *testing.T) {
	toolbox := &Toolbox{}
	var todo application.ToolInfo
	for _, tool := range toolbox.ListTools() {
		if tool.Name == "todo" {
			todo = tool
			break
		}
	}
	if todo.Name == "" {
		t.Fatal("todo tool is not advertised")
	}
	properties, ok := todo.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("todo schema properties missing: %#v", todo.InputSchema)
	}
	itemsSchema, ok := properties["items"].(map[string]any)
	if !ok {
		t.Fatalf("todo items schema missing: %#v", properties)
	}
	itemSchema, ok := itemsSchema["items"].(map[string]any)
	if !ok {
		t.Fatalf("todo item schema missing: %#v", itemsSchema)
	}
	required, ok := itemSchema["required"].([]string)
	if !ok {
		t.Fatalf("todo item required fields have unexpected type: %#v", itemSchema["required"])
	}
	if len(required) != 2 || required[0] != "id" || required[1] != "status" {
		t.Fatalf("todo item required fields = %#v, want [id status]", required)
	}
	if !strings.Contains(todo.Description, "`content` may be omitted for an existing ID") {
		t.Fatalf("todo description does not document status-only replace updates: %s", todo.Description)
	}
}

// clear_brief removes the brief and returns no plan_path; items survive.
func TestExecTodoClearBrief(t *testing.T) {
	todoPort := &stubTodoPort{}
	toolbox := &Toolbox{Todos: todoPort}
	ctx := application.WithConversationID(context.Background(), "conv_1")

	// Seed a brief + items.
	seed, _ := json.Marshal(map[string]any{
		"items": []map[string]any{{"id": "1", "content": "Task", "status": "pending"}},
		"brief": "## Objective\nX\n\n## Done when\nY",
	})
	if _, err := toolbox.execTodo(ctx, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Clear the brief (no items in this call — they must survive).
	clearArgs, _ := json.Marshal(map[string]any{"clear_brief": true})
	result, err := toolbox.execTodo(ctx, clearArgs)
	if err != nil {
		t.Fatalf("clear_brief: %v", err)
	}
	if todoPort.GetBrief("conv_1") != "" {
		t.Errorf("brief should be cleared, got %q", todoPort.GetBrief("conv_1"))
	}
	if len(todoPort.Get("conv_1")) != 1 {
		t.Errorf("items must survive clear_brief, got %d", len(todoPort.Get("conv_1")))
	}
	// No plan_path once the brief is gone.
	if strings.Contains(result, "plan_path") {
		t.Errorf("result must not carry plan_path after clear, got:\n%s", result)
	}
}

// clear_brief and brief together are rejected (mutually exclusive).
func TestExecTodoClearBriefAndBriefMutuallyExclusive(t *testing.T) {
	todoPort := &stubTodoPort{}
	toolbox := &Toolbox{Todos: todoPort}
	ctx := application.WithConversationID(context.Background(), "conv_1")

	args, _ := json.Marshal(map[string]any{
		"clear_brief": true,
		"brief":       "## Objective\nX\n\n## Done when\nY",
	})
	if _, err := toolbox.execTodo(ctx, args); err == nil {
		t.Fatal("clear_brief + brief should be rejected")
	}
}

// An empty brief string alone never clears — it means "don't change brief".
func TestExecTodoEmptyBriefDoesNotClear(t *testing.T) {
	todoPort := &stubTodoPort{}
	toolbox := &Toolbox{Todos: todoPort}
	ctx := application.WithConversationID(context.Background(), "conv_1")

	seed, _ := json.Marshal(map[string]any{
		"items": []map[string]any{{"id": "1", "content": "Task", "status": "pending"}},
		"brief": "## Objective\nX\n\n## Done when\nY",
	})
	if _, err := toolbox.execTodo(ctx, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Patch with an empty brief — must NOT clear.
	patch, _ := json.Marshal(map[string]any{
		"mode":  "patch",
		"items": []map[string]any{{"id": "1", "status": "completed"}},
		"brief": "",
	})
	if _, err := toolbox.execTodo(ctx, patch); err != nil {
		t.Fatalf("patch: %v", err)
	}
	if todoPort.GetBrief("conv_1") != "## Objective\nX\n\n## Done when\nY" {
		t.Errorf("empty brief must not clear, got %q", todoPort.GetBrief("conv_1"))
	}
}

// Setting a brief returns plan_path in the result.
func TestExecTodoReturnsPlanPath(t *testing.T) {
	todoPort := &stubTodoPort{}
	toolbox := &Toolbox{Todos: todoPort}
	ctx := application.WithConversationID(context.Background(), "conv_1")

	args, _ := json.Marshal(map[string]any{
		"items": []map[string]any{{"id": "1", "content": "Task", "status": "pending"}},
		"brief": "## Objective\nX\n\n## Done when\nY",
	})
	result, err := toolbox.execTodo(ctx, args)
	if err != nil {
		t.Fatalf("execTodo: %v", err)
	}
	if !strings.Contains(result, "plan_path") {
		t.Errorf("result should carry plan_path when a brief is set, got:\n%s", result)
	}
}
