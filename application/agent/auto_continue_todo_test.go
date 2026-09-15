package agent

import (
	"strings"
	"testing"

	"nusashell/domain"
	clock "nusashell/pkg/time"
)

// autoTodoPort is a minimal TodoPort for auto-continue hydration tests.
type autoTodoPort struct {
	items []domain.TodoItem
	brief string
}

func (f *autoTodoPort) Get(string) []domain.TodoItem    { return f.items }
func (f *autoTodoPort) GetBrief(string) string          { return f.brief }
func (f *autoTodoPort) Set(string, []domain.TodoItem)   {}
func (f *autoTodoPort) SetBrief(string, string)         {}
func (f *autoTodoPort) Clear(string)                    {}
func (f *autoTodoPort) ClearBrief(string) error         { return nil }
func (f *autoTodoPort) PlanPath(string) string          { return "" }
func (f *autoTodoPort) Patch(string, []domain.TodoItem) {}

// TestAutoContinueTodoHydrationIsPureHydration proves the hidden todo_list
// checkpoint built at the auto-continue boundary is a pure hydration
// message (IsHydrationMessage==true), carries the todo_list tool name with
// a ToolOK status, includes open items and the brief, and omits completed
// items.
func TestAutoContinueTodoHydrationIsPureHydration(t *testing.T) {
	svc := New(Deps{
		Todos: &autoTodoPort{
			items: []domain.TodoItem{
				{ID: "t1", Content: "open work", Status: domain.TodoPending},
				{ID: "t2", Content: "active work", Status: domain.TodoInProgress},
				{ID: "t3", Content: "done work", Status: domain.TodoCompleted},
			},
			brief: "## Objective\nShip the feature\n\n## Done when\nTests pass",
		},
	})
	msg := svc.autoContinueTodoHydration("conv_auto")
	if msg == nil {
		t.Fatal("hydration message must be built when open todos exist")
	}
	if !domain.IsHydrationMessage(*msg) {
		t.Fatalf("message must be pure hydration (hidden), got: %+v", msg)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("want 1 tool call, got %d", len(msg.ToolCalls))
	}
	tc := msg.ToolCalls[0]
	if tc.Name != "todo_list" {
		t.Fatalf("tool name = %q, want todo_list", tc.Name)
	}
	if tc.Status != domain.ToolOK {
		t.Fatalf("tool status = %q, want %q", tc.Status, domain.ToolOK)
	}
	if !domain.IsHydrationCallID(tc.ID) {
		t.Fatalf("call id %q must use the hydrate- prefix", tc.ID)
	}
	if !strings.Contains(tc.Output, "open work") {
		t.Errorf("output missing open pending item:\n%s", tc.Output)
	}
	if !strings.Contains(tc.Output, "active work") {
		t.Errorf("output missing in-progress item:\n%s", tc.Output)
	}
	if strings.Contains(tc.Output, "done work") {
		t.Errorf("output must omit completed items:\n%s", tc.Output)
	}
	if !strings.Contains(tc.Output, "Ship the feature") {
		t.Errorf("output missing the brief:\n%s", tc.Output)
	}
}

// TestAutoContinueTodoHydrationOmittedWhenNoTodos proves the helper returns
// nil when there is no todo content, so the boundary stays a single
// announcement + assistant placeholder (the old shape).
func TestAutoContinueTodoHydrationOmittedWhenNoTodos(t *testing.T) {
	svc := New(Deps{
		Todos: &autoTodoPort{},
	})
	if msg := svc.autoContinueTodoHydration("conv_empty"); msg != nil {
		t.Fatalf("no todos must yield nil hydration, got: %+v", msg)
	}
}

// TestAutoContinueTodoHydrationOmittedWhenNoTodoPort proves the helper
// returns nil when the Todos port is absent, preserving the old shape for
// App/Service wiring without a todo store.
func TestAutoContinueTodoHydrationOmittedWhenNoTodoPort(t *testing.T) {
	svc := New(Deps{})
	if msg := svc.autoContinueTodoHydration("conv_notodos"); msg != nil {
		t.Fatalf("no Todos port must yield nil hydration, got: %+v", msg)
	}
}

// TestAppendAutoContinueBoundaryOrdering proves the auto-continue boundary
// assembles the visible announcement, then the hidden todo_list hydration,
// then the fresh assistant placeholder, in that chronological order. The
// hidden hydration must not leak into the visible announcement message.
func TestAppendAutoContinueBoundaryOrdering(t *testing.T) {
	now := clock.NewTime().Time()
	store := &lifecycleConvStore{byID: map[string]*domain.Conversation{
		"conv_order": {
			ID:        "conv_order",
			Status:    "running",
			UpdatedAt: now,
			Messages: []domain.Message{
				{ID: "m_prev", Role: domain.RoleAssistant, Content: "previous output", Status: domain.StatusDone},
			},
		},
	}}
	svc := New(Deps{
		Conversations: store,
		Todos: &autoTodoPort{
			items: []domain.TodoItem{
				{ID: "t1", Content: "remaining work", Status: domain.TodoInProgress},
			},
			brief: "## Objective\nShip it\n\n## Done when\nTests pass",
		},
	})

	nextMsgID, err := svc.appendAutoContinueBoundary("conv_order", domain.AutoContinueDecision{
		ShouldContinue: true, ContinuesUsed: 1, OpenTodoCount: 1,
	})
	if err != nil {
		t.Fatalf("appendAutoContinueBoundary: %v", err)
	}
	if nextMsgID == "" {
		t.Fatal("next assistant message id must be returned")
	}

	conv, _ := store.Get("conv_order")
	// Expect: m_prev (assistant output) → announcement → hidden hydration → assistant placeholder.
	if len(conv.Messages) != 4 {
		t.Fatalf("messages = %d, want 4 (prev + announcement + hydration + placeholder):\n%+v", len(conv.Messages), conv.Messages)
	}
	if conv.Messages[0].ID != "m_prev" {
		t.Fatalf("message[0] must be the previous assistant output: %+v", conv.Messages[0])
	}
	announcement := conv.Messages[1]
	if announcement.Role != domain.RoleAssistant || len(announcement.ToolCalls) != 1 {
		t.Fatalf("message[1] must be the visible announcement: %+v", announcement)
	}
	if announcement.ToolCalls[0].Name != domain.AnnouncementToolName {
		t.Fatalf("announcement tool name = %q, want %q", announcement.ToolCalls[0].Name, domain.AnnouncementToolName)
	}
	if domain.IsHydrationMessage(announcement) {
		t.Fatalf("announcement must NOT be hidden hydration (it would leak a hidden tool card): %+v", announcement)
	}
	hydration := conv.Messages[2]
	if !domain.IsHydrationMessage(hydration) {
		t.Fatalf("message[2] must be the hidden todo_list hydration: %+v", hydration)
	}
	if len(hydration.ToolCalls) != 1 || hydration.ToolCalls[0].Name != "todo_list" {
		t.Fatalf("hydration must carry a single todo_list tool call: %+v", hydration)
	}
	if conv.Messages[3].Role != domain.RoleAssistant || conv.Messages[3].ID != nextMsgID {
		t.Fatalf("message[3] must be the fresh assistant placeholder: %+v", conv.Messages[3])
	}
}

// TestAppendAutoContinueBoundaryNoTodosPreservesOldShape proves that when
// the App/Service has no Todos port (or no open items), the boundary keeps
// the old one-announcement shape: announcement → assistant placeholder, with
// no hidden hydration message in between.
func TestAppendAutoContinueBoundaryNoTodosPreservesOldShape(t *testing.T) {
	now := clock.NewTime().Time()
	store := &lifecycleConvStore{byID: map[string]*domain.Conversation{
		"conv_notodos": {
			ID:        "conv_notodos",
			Status:    "running",
			UpdatedAt: now,
			Messages: []domain.Message{
				{ID: "m_prev", Role: domain.RoleAssistant, Content: "previous output", Status: domain.StatusDone},
			},
		},
	}}
	svc := New(Deps{Conversations: store})

	nextMsgID, err := svc.appendAutoContinueBoundary("conv_notodos", domain.AutoContinueDecision{
		ShouldContinue: true, ContinuesUsed: 1, OpenTodoCount: 0,
	})
	if err != nil {
		t.Fatalf("appendAutoContinueBoundary: %v", err)
	}

	conv, _ := store.Get("conv_notodos")
	if len(conv.Messages) != 3 {
		t.Fatalf("messages = %d, want 3 (prev + announcement + placeholder) when no todos:\n%+v", len(conv.Messages), conv.Messages)
	}
	if conv.Messages[1].ToolCalls[0].Name != domain.AnnouncementToolName {
		t.Fatalf("message[1] must be the announcement: %+v", conv.Messages[1])
	}
	if domain.IsHydrationMessage(conv.Messages[1]) {
		t.Fatalf("announcement must not be hidden hydration: %+v", conv.Messages[1])
	}
	if conv.Messages[2].Role != domain.RoleAssistant || conv.Messages[2].ID != nextMsgID {
		t.Fatalf("message[2] must be the fresh assistant placeholder: %+v", conv.Messages[2])
	}
}

// TestAutoContinueTodoHydrationFilteredByCompaction proves the hidden
// todo_list hydration message is stripped by domain.FilterHydrationDomainMessages
// (the compaction filter), so the hidden tool card never leaks into a
// compaction summary.
func TestAutoContinueTodoHydrationFilteredByCompaction(t *testing.T) {
	svc := New(Deps{
		Todos: &autoTodoPort{
			items: []domain.TodoItem{
				{ID: "t1", Content: "open work", Status: domain.TodoPending},
			},
			brief: "## Objective\nShip it",
		},
	})
	hyd := svc.autoContinueTodoHydration("conv_filter")
	if hyd == nil {
		t.Fatal("hydration message must be built")
	}
	announcement := domain.Message{
		ID:        "m_announce",
		Role:      domain.RoleAssistant,
		Status:    domain.StatusDone,
		ToolCalls: []domain.ToolCall{{ID: "announce-x", Name: domain.AnnouncementToolName, Status: domain.ToolOK, Output: "continue"}},
	}
	msgs := []domain.Message{announcement, *hyd}
	filtered := domain.FilterHydrationDomainMessages(msgs)
	if len(filtered) != 1 {
		t.Fatalf("compaction filter must drop the hidden hydration, got %d messages:\n%+v", len(filtered), filtered)
	}
	if filtered[0].ID != "m_announce" {
		t.Fatalf("compaction filter must keep the visible announcement, got: %+v", filtered[0])
	}
}

// TestAutoContinueTodoHydrationRetainedInProviderMessages proves the hidden
// todo_list checkpoint persisted by appendAutoContinueBoundary survives the
// chatMessages translation that builds the provider-facing request: the
// provider sees a todo_list tool result carrying the current open item, while
// the persisted hydration message in the conversation stays a pure hydration
// message (domain.IsHydrationMessage). This closes the gap between "persisted
// order + pure hydration + compaction filtering" and the actual provider path.
//
// The caps deliberately enable ReasoningReplay so the hydration reasoning is
// not stripped, but the test does not depend on that — the tool result is the
// carrier of the open TODO state.
func TestAutoContinueTodoHydrationRetainedInProviderMessages(t *testing.T) {
	now := clock.NewTime().Time()
	store := &lifecycleConvStore{byID: map[string]*domain.Conversation{
		"conv_provider": {
			ID:        "conv_provider",
			Status:    "running",
			UpdatedAt: now,
			Messages: []domain.Message{
				{ID: "m_user", Role: domain.RoleUser, Content: "ship it"},
				{ID: "m_prev", Role: domain.RoleAssistant, Content: "previous output", Status: domain.StatusDone},
			},
		},
	}}
	svc := New(Deps{
		Conversations: store,
		Todos: &autoTodoPort{
			items: []domain.TodoItem{
				{ID: "t1", Content: "open work", Status: domain.TodoPending},
				{ID: "t2", Content: "active work", Status: domain.TodoInProgress},
				{ID: "t3", Content: "done work", Status: domain.TodoCompleted},
			},
			brief: "## Objective\nShip the feature\n\n## Done when\nTests pass",
		},
	})

	nextMsgID, err := svc.appendAutoContinueBoundary("conv_provider", domain.AutoContinueDecision{
		ShouldContinue: true, ContinuesUsed: 1, OpenTodoCount: 2,
	})
	if err != nil {
		t.Fatalf("appendAutoContinueBoundary: %v", err)
	}
	if nextMsgID == "" {
		t.Fatal("next assistant message id must be returned")
	}

	conv, _ := store.Get("conv_provider")
	if conv == nil {
		t.Fatal("conversation must be persisted")
	}

	// Locate the persisted hidden hydration message and assert it stays pure
	// hydration (the persisted shape must not be mutated by the provider path).
	var persistedHydration *domain.Message
	for i := range conv.Messages {
		if domain.IsHydrationMessage(conv.Messages[i]) {
			persistedHydration = &conv.Messages[i]
			break
		}
	}
	if persistedHydration == nil {
		t.Fatalf("persisted conversation must contain a hidden hydration message:\n%+v", conv.Messages)
	}
	if len(persistedHydration.ToolCalls) != 1 || persistedHydration.ToolCalls[0].Name != "todo_list" {
		t.Fatalf("persisted hydration must carry a single todo_list tool call: %+v", persistedHydration)
	}
	if !domain.IsHydrationCallID(persistedHydration.ToolCalls[0].ID) {
		t.Fatalf("persisted hydration tool call id must use hydrate- prefix: %q", persistedHydration.ToolCalls[0].ID)
	}

	// Build the provider-facing messages with caps that do not suppress the
	// hydration checkpoint. ReasoningReplay is enabled so hydration reasoning
	// is preserved (the test is robust to it being disabled, but we exercise
	// the non-suppressing path explicitly).
	caps := ModelCapabilities{ReasoningReplay: true}
	providerMsgs := chatMessages(conv, nextMsgID, caps)
	if len(providerMsgs) == 0 {
		t.Fatal("provider messages must not be empty")
	}

	// Find the todo_list tool result in the provider-facing messages.
	var todoResult *ToolResult
	var todoAssistant *ChatMessage
	for i := range providerMsgs {
		pm := &providerMsgs[i]
		if pm.Role == "tool" && pm.ToolResult != nil && pm.ToolResult.Name == "todo_list" {
			todoResult = pm.ToolResult
		}
		if pm.Role == "assistant" {
			for _, tc := range pm.ToolCalls {
				if tc.Name == "todo_list" {
					todoAssistant = pm
				}
			}
		}
	}
	if todoResult == nil {
		t.Fatalf("provider messages must include a todo_list tool result:\n%+v", providerMsgs)
	}
	if todoAssistant == nil {
		t.Fatalf("provider messages must include the assistant turn carrying the todo_list tool call:\n%+v", providerMsgs)
	}
	if !strings.Contains(todoResult.Content, "open work") {
		t.Errorf("todo_list tool result missing open pending item:\n%s", todoResult.Content)
	}
	if !strings.Contains(todoResult.Content, "active work") {
		t.Errorf("todo_list tool result missing in-progress item:\n%s", todoResult.Content)
	}
	if strings.Contains(todoResult.Content, "done work") {
		t.Errorf("todo_list tool result must omit completed items:\n%s", todoResult.Content)
	}
	if !strings.Contains(todoResult.Content, "Ship the feature") {
		t.Errorf("todo_list tool result missing the brief:\n%s", todoResult.Content)
	}

	// The fresh assistant placeholder (nextMsgID) must be skipped by
	// chatMessages (it is empty), so it never appears as a trailing empty
	// assistant turn in the provider payload.
	for _, pm := range providerMsgs {
		if pm.Role == "assistant" && pm.Content == "" && len(pm.ToolCalls) == 0 && pm.Reasoning == "" {
			t.Errorf("provider messages must not contain the empty fresh assistant placeholder:\n%+v", pm)
		}
	}

	// Re-load the persisted conversation and confirm the hydration message is
	// still pure hydration after the provider path ran (the provider path must
	// not mutate persisted state).
	conv2, _ := store.Get("conv_provider")
	for i := range conv2.Messages {
		if domain.IsHydrationMessage(conv2.Messages[i]) {
			if !domain.IsHydrationCallID(conv2.Messages[i].ToolCalls[0].ID) {
				t.Errorf("persisted hydration tool call id lost hydrate- prefix after provider path: %q", conv2.Messages[i].ToolCalls[0].ID)
			}
			if conv2.Messages[i].ToolCalls[0].Name != "todo_list" {
				t.Errorf("persisted hydration tool name changed after provider path: %q", conv2.Messages[i].ToolCalls[0].Name)
			}
		}
	}
}
