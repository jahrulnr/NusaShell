package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"nusashell/domain"
)

// stubSkillStoreHyd is a minimal SkillStore for hydration tests.
type stubSkillStoreHyd struct{ skills []*domain.Skill }

func (s *stubSkillStoreHyd) List() []*domain.Skill { return s.skills }
func (s *stubSkillStoreHyd) Get(id, ownedBy string) (*domain.Skill, error) {
	return nil, fmt.Errorf("not found")
}
func (s *stubSkillStoreHyd) Save(sk *domain.Skill) error     { return nil }
func (s *stubSkillStoreHyd) Delete(id, ownedBy string) error { return nil }
func (s *stubSkillStoreHyd) ReadFile(id, ownedBy, path string, offset, maxChars int) (*domain.SkillFile, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *stubSkillStoreHyd) Files(id, ownedBy string) ([]domain.SkillFileEntry, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *stubSkillStoreHyd) WriteFile(id, ownedBy, path, content string) error {
	return fmt.Errorf("not implemented")
}
func (s *stubSkillStoreHyd) Install(zipData []byte) (string, error) {
	return "", fmt.Errorf("not implemented")
}
func (s *stubSkillStoreHyd) MountPluginSkills(pluginID, dir string) error { return nil }
func (s *stubSkillStoreHyd) UnmountPluginSkills(pluginID string) error    { return nil }

// stubHydrationExecutor is a scripted ToolExecutor for hydration tests. It
// records every call and returns the scripted output per tool name.
type stubHydrationExecutor struct {
	fn    func(name string, args []byte) (string, error)
	calls []string
}

func (s *stubHydrationExecutor) ListTools() []ToolInfo { return nil }
func (s *stubHydrationExecutor) Execute(_ context.Context, name string, args []byte) (string, error) {
	s.calls = append(s.calls, name+" "+string(args))
	return s.fn(name, args)
}

// emptyToolOutput is a yamlJSONL output with no records — the builder must
// hide slots whose real tool reports nothing.
const emptyToolOutput = "---\ncount: 0\n---\n"

// hydrationResultByName returns the tool-result content of the named slot.
// The transcript is dynamic, so tests look slots up by name, never by index.
func hydrationResultByName(t *testing.T, result HydrationResult, name string) string {
	t.Helper()
	for i, c := range result.Messages[0].ToolCalls {
		if c.Name == name {
			r := result.Messages[i+1]
			if r.ToolResult == nil {
				t.Fatalf("slot %q has no tool result", name)
			}
			return r.ToolResult.Content
		}
	}
	t.Fatalf("slot %q not found in hydration transcript", name)
	return ""
}

func TestHydrationBuildBasic(t *testing.T) {
	b := NewHydrationBuilder(HydrationSource{
		RuntimeContext: RuntimeContextSnapshot{
			CurrentDate: "2026-01-01T00:00:00Z",
			Environment: "test",
			RuntimeOS:   "linux/amd64",
			Workspace:   "/home/user",
		},
	})
	result := b.Build()
	// Without an executor or todos, every tool-backed slot is hidden: only
	// runtime_context remains (dynamic transcript).
	if result.CallCount != 1 {
		t.Fatalf("expected 1 hydration call, got %d", result.CallCount)
	}
	if len(result.Messages) != 2 { // 1 assistant + 1 tool result
		t.Fatalf("expected 2 messages, got %d", len(result.Messages))
	}
	// First message: assistant with toolCalls
	if result.Messages[0].Role != "assistant" {
		t.Errorf("expected first message role=assistant, got %s", result.Messages[0].Role)
	}
	if len(result.Messages[0].ToolCalls) != 1 {
		t.Errorf("expected 1 toolCall, got %d", len(result.Messages[0].ToolCalls))
	}
	// All call IDs must have hydrate: prefix
	for _, c := range result.Messages[0].ToolCalls {
		if !strings.HasPrefix(c.ID, domain.HydrateToolCallPrefix) {
			t.Errorf("call ID %s should have hydrate: prefix", c.ID)
		}
	}
	// Tool results must match call IDs
	for i, c := range result.Messages[0].ToolCalls {
		result := result.Messages[i+1]
		if result.Role != "tool" {
			t.Errorf("expected message %d role=tool, got %s", i+1, result.Role)
		}
		if result.ToolResult.ToolCallID != c.ID {
			t.Errorf("tool result ID mismatch: %s != %s", result.ToolResult.ToolCallID, c.ID)
		}
	}
}

func TestHydrationRuntimeContext(t *testing.T) {
	b := NewHydrationBuilder(HydrationSource{
		RuntimeContext: RuntimeContextSnapshot{
			CurrentDate: "2026-01-01T00:00:00Z",
			Environment: "test-env",
			RuntimeOS:   "darwin/arm64",
			Workspace:   "/Users/test",
		},
	})
	result := b.Build()
	// runtime_context is the first tool result
	rtContent := result.Messages[1].ToolResult.Content
	var rt map[string]string
	if err := json.Unmarshal([]byte(rtContent), &rt); err != nil {
		t.Fatalf("invalid runtime_context JSON: %v", err)
	}
	if rt["currentDate"] != "2026-01-01T00:00:00Z" {
		t.Errorf("expected currentDate, got %s", rt["currentDate"])
	}
	if rt["environment"] != "test-env" {
		t.Errorf("expected environment, got %s", rt["environment"])
	}
	if rt["workspace"] != "/Users/test" {
		t.Errorf("expected workspace, got %s", rt["workspace"])
	}
}

func TestHydrationMemory(t *testing.T) {
	// The memory slot runs the real memory_list tool (target=primary) and
	// enriches its entries with usage stats computed from the output.
	exec := &stubHydrationExecutor{fn: func(name string, _ []byte) (string, error) {
		switch name {
		case "memory_list":
			return "---\ncount: 2\n---\n" +
				`{"id":"frag_1","content":"User prefers Indonesian"}` + "\n" +
				`{"id":"frag_2","content":"Repo uses Go + Clean Architecture"}`, nil
		case "skill_list", "mcp_list", "tool_list":
			return emptyToolOutput, nil
		}
		return "", fmt.Errorf("unexpected tool %q", name)
	}}
	b := NewHydrationBuilder(HydrationSource{Executor: exec})
	result := b.Build()
	if got := result.Messages[0].ToolCalls[1].Args; got != `{"target":"primary"}` {
		t.Errorf("memory call args = %s, want target=primary", got)
	}
	// memory is the second slot (after runtime_context)
	memContent := hydrationResultByName(t, result, "memory")
	var mem struct {
		Count   int `json:"count"`
		Entries []struct {
			ID      string `json:"id"`
			Content string `json:"content"`
		} `json:"entries"`
		Usage struct {
			Chars int `json:"chars"`
			Limit int `json:"limit"`
			Pct   int `json:"pct"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(memContent), &mem); err != nil {
		t.Fatalf("invalid memory JSON: %v", err)
	}
	if mem.Count != 2 {
		t.Errorf("expected 2 primary entries, got %d", mem.Count)
	}
	if len(mem.Entries) != 2 {
		t.Fatalf("unexpected entries: %+v", mem.Entries)
	}
	if mem.Entries[0].ID != "frag_1" || mem.Entries[0].Content != "User prefers Indonesian" {
		t.Errorf("entry 0 = %+v", mem.Entries[0])
	}
	// Usage should report the primary char budget.
	if mem.Usage.Limit != domain.PrimaryCharCap {
		t.Errorf("limit = %d, want %d", mem.Usage.Limit, domain.PrimaryCharCap)
	}
	expectedChars := len("User prefers Indonesian") + len("Repo uses Go + Clean Architecture")
	if mem.Usage.Chars != expectedChars {
		t.Errorf("chars = %d, want %d", mem.Usage.Chars, expectedChars)
	}
}

func TestHydrationMemoryHiddenWhenEmpty(t *testing.T) {
	// No executor: the memory slot is hidden, not emitted as an empty stub.
	b := NewHydrationBuilder(HydrationSource{})
	result := b.Build()
	for _, c := range result.Messages[0].ToolCalls {
		if c.Name == "memory" {
			t.Fatal("memory slot must be hidden when the real tool is unavailable")
		}
	}
	// Executor present but primary memory empty: also hidden.
	exec := &stubHydrationExecutor{fn: func(name string, _ []byte) (string, error) {
		switch name {
		case "memory_list", "skill_list", "mcp_list", "tool_list":
			return emptyToolOutput, nil
		}
		return "", fmt.Errorf("unexpected tool %q", name)
	}}
	result = NewHydrationBuilder(HydrationSource{Executor: exec}).Build()
	for _, c := range result.Messages[0].ToolCalls {
		if c.Name == "memory" {
			t.Fatal("memory slot must be hidden when primary memory is empty")
		}
	}
}

func TestHydrationSkillsRealOutput(t *testing.T) {
	// The skill_list slot attaches the real tool's output verbatim.
	skillOutput := "---\ncount: 2\n---\n" +
		`{"name":"alpha","description":"a"}` + "\n" +
		`{"name":"zebra","description":"z"}`
	exec := &stubHydrationExecutor{fn: func(name string, _ []byte) (string, error) {
		switch name {
		case "skill_list":
			return skillOutput, nil
		case "memory_list", "mcp_list", "tool_list":
			return emptyToolOutput, nil
		}
		return "", fmt.Errorf("unexpected tool %q", name)
	}}
	b := NewHydrationBuilder(HydrationSource{Executor: exec})
	result := b.Build()
	if got := hydrationResultByName(t, result, "skill_list"); got != skillOutput {
		t.Fatalf("skill_list slot must contain the real tool output verbatim:\n got %q\nwant %q", got, skillOutput)
	}
}

func TestHydrationSkillsHiddenWhenEmpty(t *testing.T) {
	exec := &stubHydrationExecutor{fn: func(name string, _ []byte) (string, error) {
		switch name {
		case "memory_list", "skill_list", "mcp_list", "tool_list":
			return emptyToolOutput, nil
		}
		return "", fmt.Errorf("unexpected tool %q", name)
	}}
	result := NewHydrationBuilder(HydrationSource{Executor: exec}).Build()
	for _, c := range result.Messages[0].ToolCalls {
		if c.Name == "skill_list" {
			t.Fatal("skill_list slot must be hidden when the skill library is empty")
		}
	}
}

func TestHydrationMcpListExecutesRealTool(t *testing.T) {
	realOutput := "---\ncount: 2\n---\n" +
		`{"name":"fs","id":"srv2","running":false,"tools":0}` + "\n" +
		`{"name":"github","id":"srv1","running":true,"tools":1}`
	b := NewHydrationBuilder(HydrationSource{
		Executor: &stubHydrationExecutor{fn: func(name string, _ []byte) (string, error) {
			switch name {
			case "mcp_list":
				return realOutput, nil
			case "memory_list", "skill_list", "tool_list":
				return emptyToolOutput, nil
			}
			return "", fmt.Errorf("unexpected tool %q", name)
		}},
	})
	result := b.Build()
	mcpContent := hydrationResultByName(t, result, "mcp_list")
	if mcpContent != realOutput {
		t.Fatalf("mcp_list slot must contain the real tool output verbatim:\n got %q\nwant %q", mcpContent, realOutput)
	}
}

func TestHydrationMcpListHiddenWhenNoPlugins(t *testing.T) {
	exec := &stubHydrationExecutor{fn: func(name string, _ []byte) (string, error) {
		switch name {
		case "memory_list", "skill_list", "mcp_list", "tool_list":
			return emptyToolOutput, nil
		}
		return "", fmt.Errorf("unexpected tool %q", name)
	}}
	result := NewHydrationBuilder(HydrationSource{Executor: exec}).Build()
	for _, c := range result.Messages[0].ToolCalls {
		if c.Name == "mcp_list" || c.Name == "tool_list" {
			t.Fatalf("mcp/tool slots must be hidden when no plugins exist, got %s", c.Name)
		}
	}
}

// TestHydrationToolListLoopsRealToolPerServer pins the discovery workflow:
// the real mcp_list runs first, then the real tool_list runs once per
// RUNNING server id (from the mcp_list result) — the same sequence the agent
// itself would execute. No tool_list call happens for stopped servers, and
// the built-in catalog is never injected (tools[] covers it).
func TestHydrationToolListLoopsRealToolPerServer(t *testing.T) {
	mcpOutput := "---\ncount: 2\n---\n" +
		`{"name":"Files","id":"nusashell.files","running":true,"tools":1}` + "\n" +
		`{"name":"Offline","id":"nusashell.offline","running":false,"tools":0}`
	filesOutput := "---\ncount: 1\n---\n" +
		`{"ref":"nusashell.files:read_file","name":"read_file","server":"nusashell.files","description":"Read a file","parameters":{"type":"object"}}`
	exec := &stubHydrationExecutor{fn: func(name string, args []byte) (string, error) {
		switch name {
		case "mcp_list":
			return mcpOutput, nil
		case "tool_list":
			if string(args) == `{"server":"nusashell.files"}` {
				return filesOutput, nil
			}
			return emptyToolOutput, nil
		case "memory_list", "skill_list":
			return emptyToolOutput, nil
		}
		return "", fmt.Errorf("unexpected tool %q", name)
	}}
	b := NewHydrationBuilder(HydrationSource{Executor: exec})
	result := b.Build()

	// mcp_list first, then exactly one tool_list call per running server,
	// with the server id from the mcp_list result as the argument.
	var toolListCalls []string
	for _, call := range exec.calls {
		if strings.HasPrefix(call, "tool_list ") {
			toolListCalls = append(toolListCalls, strings.TrimPrefix(call, "tool_list "))
		}
	}
	if len(toolListCalls) != 1 {
		t.Fatalf("expected 1 tool_list call for 1 running server, got %d (%v)", len(toolListCalls), exec.calls)
	}
	if toolListCalls[0] != `{"server":"nusashell.files"}` {
		t.Errorf("tool_list args = %s, want server=nusashell.files", toolListCalls[0])
	}

	// The tool_list result in the transcript carries the real output verbatim.
	var found bool
	for i := 1; i < len(result.Messages); i++ {
		m := result.Messages[i]
		if m.Role == "tool" && m.ToolResult != nil && m.ToolResult.Name == "tool_list" {
			if m.ToolResult.Content != filesOutput {
				t.Fatalf("tool_list slot must contain the real tool output verbatim:\n got %q\nwant %q", m.ToolResult.Content, filesOutput)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("tool_list result not found in the hydration transcript")
	}
}

func TestHydrationTodoList(t *testing.T) {
	// In-memory todo port for testing
	port := &fakeTodoPort{items: map[string][]domain.TodoItem{
		"conv_1": {
			{ID: "1", Content: "Create CLI", Status: domain.TodoCompleted},
			{ID: "2", Content: "Add parser", Status: domain.TodoInProgress},
			{ID: "3", Content: "Write tests", Status: domain.TodoPending},
		},
	}}
	b := NewHydrationBuilder(HydrationSource{Todos: port, ConvID: "conv_1"})
	result := b.Build()
	todoContent := hydrationResultByName(t, result, "todo_list")
	if !strings.Contains(todoContent, "CURRENT TASKS") {
		t.Errorf("expected CURRENT TASKS header, got: %s", todoContent)
	}
	if !strings.Contains(todoContent, "[~] Add parser") {
		t.Errorf("expected in_progress item, got: %s", todoContent)
	}
	if !strings.Contains(todoContent, "[ ] Write tests") {
		t.Errorf("expected pending item, got: %s", todoContent)
	}
	// Completed items should be filtered out
	if strings.Contains(todoContent, "Create CLI") {
		t.Errorf("completed item should not appear, got: %s", todoContent)
	}
}

func TestHydrationTodoListWithBrief(t *testing.T) {
	port := &fakeTodoPort{
		items: map[string][]domain.TodoItem{
			"conv_1": {
				{ID: "1", Content: "Step 1", Status: domain.TodoInProgress},
			},
		},
		briefs: map[string]string{
			"conv_1": "Build a CLI tool that converts Markdown to HTML with custom templates.",
		},
	}
	b := NewHydrationBuilder(HydrationSource{Todos: port, ConvID: "conv_1"})
	result := b.Build()
	todoContent := hydrationResultByName(t, result, "todo_list")
	if !strings.Contains(todoContent, "USER BRIEF") {
		t.Errorf("expected USER BRIEF header, got: %s", todoContent)
	}
	if !strings.Contains(todoContent, "Build a CLI tool that converts Markdown") {
		t.Errorf("expected brief text, got: %s", todoContent)
	}
	if !strings.Contains(todoContent, "CURRENT TASKS") {
		t.Errorf("expected CURRENT TASKS header, got: %s", todoContent)
	}
	// Brief should appear before tasks
	briefIdx := strings.Index(todoContent, "USER BRIEF")
	tasksIdx := strings.Index(todoContent, "CURRENT TASKS")
	if briefIdx == -1 || tasksIdx == -1 || briefIdx > tasksIdx {
		t.Errorf("brief should appear before tasks, briefIdx=%d tasksIdx=%d", briefIdx, tasksIdx)
	}
}

func TestHydrationTodoListBriefOnly(t *testing.T) {
	port := &fakeTodoPort{
		items: map[string][]domain.TodoItem{},
		briefs: map[string]string{
			"conv_1": "Refactor the auth module to use JWT.",
		},
	}
	b := NewHydrationBuilder(HydrationSource{Todos: port, ConvID: "conv_1"})
	result := b.Build()
	todoContent := hydrationResultByName(t, result, "todo_list")
	if !strings.Contains(todoContent, "USER BRIEF") {
		t.Errorf("expected USER BRIEF header, got: %s", todoContent)
	}
	if strings.Contains(todoContent, "CURRENT TASKS") {
		t.Errorf("should not have CURRENT TASKS when no items, got: %s", todoContent)
	}
}

// TestHydrationTodoListHiddenWhenEmpty pins the dynamic rule: no brief and no
// open items → the todo_list slot is omitted entirely (not an empty stub).
func TestHydrationTodoListHiddenWhenEmpty(t *testing.T) {
	port := &fakeTodoPort{items: map[string][]domain.TodoItem{}}
	result := NewHydrationBuilder(HydrationSource{Todos: port, ConvID: "conv_1"}).Build()
	for _, c := range result.Messages[0].ToolCalls {
		if c.Name == "todo_list" {
			t.Fatal("todo_list slot must be hidden when there is no brief and no open items")
		}
	}
	// Nil port: also hidden.
	result = NewHydrationBuilder(HydrationSource{}).Build()
	for _, c := range result.Messages[0].ToolCalls {
		if c.Name == "todo_list" {
			t.Fatal("todo_list slot must be hidden when no todo port is configured")
		}
	}
}

// fakeTodoPort is a minimal in-memory ConversationTodoPort for testing.
type fakeTodoPort struct {
	items  map[string][]domain.TodoItem
	briefs map[string]string
}

func (f *fakeTodoPort) Get(convID string) []domain.TodoItem {
	return f.items[convID]
}

func (f *fakeTodoPort) GetBrief(convID string) string {
	if f.briefs == nil {
		return ""
	}
	return f.briefs[convID]
}

func (f *fakeTodoPort) Set(convID string, items []domain.TodoItem) {
	if f.items == nil {
		f.items = map[string][]domain.TodoItem{}
	}
	f.items[convID] = items
}

func (f *fakeTodoPort) SetBrief(convID string, goal string) {
	if f.briefs == nil {
		f.briefs = map[string]string{}
	}
	f.briefs[convID] = goal
}

func (f *fakeTodoPort) Clear(convID string) {
	delete(f.items, convID)
	delete(f.briefs, convID)
}

func TestFilterHydrationRemovesAll(t *testing.T) {
	b := NewHydrationBuilder(HydrationSource{})
	result := b.Build()
	messages := append([]ChatMessage{
		{Role: "user", Content: "hello"},
	}, result.Messages...)
	messages = append(messages, ChatMessage{Role: "assistant", Content: "hi there"})
	filtered := FilterHydration(messages)
	if len(filtered) != 2 {
		t.Fatalf("expected 2 messages after filter (user + assistant), got %d", len(filtered))
	}
	if filtered[0].Role != "user" || filtered[1].Role != "assistant" {
		t.Errorf("expected user+assistant, got %s+%s", filtered[0].Role, filtered[1].Role)
	}
}

func TestFilterHydrationPreservesRealToolCalls(t *testing.T) {
	b := NewHydrationBuilder(HydrationSource{})
	result := b.Build()
	// Mix real tool calls with hydration
	mixed := ChatMessage{
		Role: "assistant",
		ToolCalls: append(
			[]domain.ToolCall{{ID: "real_call_1", Name: "skill_list", Args: "{}"}},
			result.Messages[0].ToolCalls...,
		),
	}
	messages := []ChatMessage{
		{Role: "user", Content: "hello"},
		mixed,
	}
	// Add real tool result
	messages = append(messages, ChatMessage{Role: "tool", ToolResult: &ToolResult{ToolCallID: "real_call_1", Name: "skill_list", Content: "result"}})
	// Add hydration tool results
	messages = append(messages, result.Messages[1:]...)
	filtered := FilterHydration(messages)
	// Should keep: user, assistant (with only real_call_1), tool (real_call_1 result)
	if len(filtered) != 3 {
		t.Fatalf("expected 3 messages after filter, got %d", len(filtered))
	}
	if filtered[1].Role != "assistant" || len(filtered[1].ToolCalls) != 1 {
		t.Errorf("expected assistant with 1 real tool call, got role=%s calls=%d", filtered[1].Role, len(filtered[1].ToolCalls))
	}
	if filtered[1].ToolCalls[0].ID != "real_call_1" {
		t.Errorf("expected real_call_1 to survive, got %s", filtered[1].ToolCalls[0].ID)
	}
}

func TestFilterHydrationDropsEmptyAssistant(t *testing.T) {
	b := NewHydrationBuilder(HydrationSource{})
	result := b.Build()
	// Assistant with ONLY hydration calls and no content
	messages := []ChatMessage{
		{Role: "user", Content: "hello"},
		result.Messages[0], // assistant with only hydration toolCalls
	}
	messages = append(messages, result.Messages[1:]...)
	filtered := FilterHydration(messages)
	// Should keep only user message (empty assistant dropped)
	if len(filtered) != 1 {
		t.Fatalf("expected 1 message after filter, got %d", len(filtered))
	}
	if filtered[0].Role != "user" {
		t.Errorf("expected user message, got %s", filtered[0].Role)
	}
}

func TestHasHydrationTrue(t *testing.T) {
	b := NewHydrationBuilder(HydrationSource{})
	result := b.Build()
	messages := append([]ChatMessage{
		{Role: "user", Content: "hello"},
	}, result.Messages...)
	if !HasHydration(messages) {
		t.Error("expected HasHydration=true")
	}
}

func TestHasHydrationReusesCheckpointAfterLaterUserMessage(t *testing.T) {
	b := NewHydrationBuilder(HydrationSource{})
	result := b.Build()
	messages := append([]ChatMessage{{Role: "user", Content: "hello"}}, result.Messages...)
	messages = append(messages,
		ChatMessage{Role: "assistant", Content: "hi"},
		ChatMessage{Role: "user", Content: "follow up"},
	)
	if !HasHydration(messages) {
		t.Error("expected the existing hydration checkpoint to remain reusable")
	}
}

func TestHasHydrationFalse(t *testing.T) {
	messages := []ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}
	if HasHydration(messages) {
		t.Error("expected HasHydration=false for no hydration")
	}
}

func TestHasHydrationFalseIncomplete(t *testing.T) {
	// Assistant with hydration calls but no matching tool results
	messages := []ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", ToolCalls: []domain.ToolCall{
			{ID: "hydrate-abc_0", Name: "runtime_context", Args: "{}"},
		}},
		// No tool result for hydrate-abc_0
		{Role: "assistant", Content: "hi"},
	}
	if HasHydration(messages) {
		t.Error("expected HasHydration=false for incomplete hydration")
	}
}

func TestHydrationNonceUnique(t *testing.T) {
	b1 := NewHydrationBuilder(HydrationSource{})
	b2 := NewHydrationBuilder(HydrationSource{})
	r1 := b1.Build()
	r2 := b2.Build()
	if r1.Nonce == r2.Nonce {
		t.Error("expected different nonces for two builds")
	}
}
