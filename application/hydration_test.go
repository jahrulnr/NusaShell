package application

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"nusashell/contracts"
	"nusashell/domain"
)

// stubMemStore is a minimal MemoryStore for testing.
type stubMemStore struct{ entries []*domain.MemoryEntry }

func (s *stubMemStore) List() []*domain.MemoryEntry      { return s.entries }
func (s *stubMemStore) Save(e *domain.MemoryEntry) error { return nil }
func (s *stubMemStore) Delete(id string) error           { return nil }
func (s *stubMemStore) Replace(target, oldText, content string) error {
	return nil
}

// stubPrimaryStore is a minimal PrimaryStore for hydration tests.
type stubPrimaryStore struct{ mem *domain.PrimaryMemory }

func (s *stubPrimaryStore) Load() *domain.PrimaryMemory { return s.mem }
func (s *stubPrimaryStore) Update(entries []domain.PrimaryEntry) error {
	return nil
}
func (s *stubPrimaryStore) Replace(oldText, content string) error { return nil }

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
func (s *stubSkillStoreHyd) Install(zipData []byte) (string, error) {
	return "", fmt.Errorf("not implemented")
}
func (s *stubSkillStoreHyd) MountPluginSkills(pluginID, dir string) error { return nil }
func (s *stubSkillStoreHyd) UnmountPluginSkills(pluginID string) error    { return nil }

// stubPluginStoreHyd is a minimal PluginStore for hydration tests.
type stubPluginStoreHyd struct{ plugins []*domain.Plugin }

func (s *stubPluginStoreHyd) List() ([]*domain.Plugin, error) { return s.plugins, nil }
func (s *stubPluginStoreHyd) Get(id string) (*domain.Plugin, error) {
	return nil, fmt.Errorf("not found")
}
func (s *stubPluginStoreHyd) Install(sourceDir string) (*domain.Plugin, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *stubPluginStoreHyd) Uninstall(id string) error   { return nil }
func (s *stubPluginStoreHyd) Save(p *domain.Plugin) error { return nil }
func (s *stubPluginStoreHyd) Delete(id string) error      { return nil }

// stubMCPReader is a minimal MCPToolReader for testing.
type stubMCPReader struct {
	tools map[string][]contracts.MCPToolDTO
}

func (m *stubMCPReader) ToolsFor(serverID string) ([]contracts.MCPToolDTO, bool) {
	tools, ok := m.tools[serverID]
	return tools, ok
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
	if result.CallCount != 6 {
		t.Fatalf("expected 6 hydration calls, got %d", result.CallCount)
	}
	if len(result.Messages) != 7 { // 1 assistant + 6 tool results
		t.Fatalf("expected 7 messages, got %d", len(result.Messages))
	}
	// First message: assistant with toolCalls
	if result.Messages[0].Role != "assistant" {
		t.Errorf("expected first message role=assistant, got %s", result.Messages[0].Role)
	}
	if len(result.Messages[0].ToolCalls) != 6 {
		t.Errorf("expected 6 toolCalls, got %d", len(result.Messages[0].ToolCalls))
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
	b := NewHydrationBuilder(HydrationSource{
		Primary: &stubPrimaryStore{mem: &domain.PrimaryMemory{
			Entries: []domain.PrimaryEntry{
				{ID: "frag_1", Content: "User prefers Indonesian"},
				{ID: "frag_2", Content: "Repo uses Go + Clean Architecture"},
			},
		}},
	})
	result := b.Build()
	// memory is the second tool result (index 2)
	memContent := result.Messages[2].ToolResult.Content
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

func TestHydrationMemoryNil(t *testing.T) {
	b := NewHydrationBuilder(HydrationSource{})
	result := b.Build()
	memContent := result.Messages[2].ToolResult.Content
	if memContent != "{}" {
		t.Errorf("expected {} for nil primary, got %s", memContent)
	}
}

func TestHydrationSkillsSorted(t *testing.T) {
	b := NewHydrationBuilder(HydrationSource{
		Skills: &stubSkillStoreHyd{skills: []*domain.Skill{
			{ID: "s3", Name: "zebra", Description: "z"},
			{ID: "s1", Name: "alpha", Description: "a"},
			{ID: "s2", Name: "mid", Description: "m"},
		}},
	})
	result := b.Build()
	skillContent := result.Messages[3].ToolResult.Content
	var skills []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(skillContent), &skills); err != nil {
		t.Fatalf("invalid skill_list JSON: %v", err)
	}
	if len(skills) != 3 {
		t.Fatalf("expected 3 skills, got %d", len(skills))
	}
	if skills[0].Name != "alpha" || skills[1].Name != "mid" || skills[2].Name != "zebra" {
		t.Errorf("expected sorted skills, got %s %s %s", skills[0].Name, skills[1].Name, skills[2].Name)
	}
}

func TestHydrationMcpList(t *testing.T) {
	b := NewHydrationBuilder(HydrationSource{
		Plugins: &stubPluginStoreHyd{plugins: []*domain.Plugin{
			{Manifest: domain.PluginManifest{ID: "srv1", Name: "github", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio}}},
			{Manifest: domain.PluginManifest{ID: "srv2", Name: "fs", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio}}},
		}},
		MCP: &stubMCPReader{tools: map[string][]contracts.MCPToolDTO{
			"plugin:srv1": {{Name: "create_issue"}},
		}},
	})
	result := b.Build()
	mcpContent := result.Messages[4].ToolResult.Content
	var mcp struct {
		Plugins []struct {
			Name    string `json:"name"`
			Running bool   `json:"running"`
			Tools   int    `json:"tools"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal([]byte(mcpContent), &mcp); err != nil {
		t.Fatalf("invalid mcp_list JSON: %v", err)
	}
	// The merged plugin list must include ALL plugins — running and idle.
	if len(mcp.Plugins) != 2 {
		t.Fatalf("expected 2 plugins (running + idle), got %d", len(mcp.Plugins))
	}
	if mcp.Plugins[1].Name != "github" || !mcp.Plugins[1].Running || mcp.Plugins[1].Tools != 1 {
		t.Errorf("expected github running with 1 tool, got %+v", mcp.Plugins[1])
	}
	if mcp.Plugins[0].Name != "fs" || mcp.Plugins[0].Running {
		t.Errorf("expected fs idle, got %+v", mcp.Plugins[0])
	}
}

func TestHydrationToolList(t *testing.T) {
	b := NewHydrationBuilder(HydrationSource{
		Tools: []ToolInfo{
			{Name: "skill_list", Description: "List skills"},
			{Name: "memory_save", Description: "Save memory"},
		},
	})
	result := b.Build()
	toolContent := result.Messages[5].ToolResult.Content
	var tl struct {
		Count int `json:"count"`
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(toolContent), &tl); err != nil {
		t.Fatalf("invalid tool_list JSON: %v", err)
	}
	if tl.Count != 2 {
		t.Errorf("expected 2 tools, got %d", tl.Count)
	}
	// Should be sorted
	if tl.Tools[0].Name != "memory_save" || tl.Tools[1].Name != "skill_list" {
		t.Errorf("expected sorted tools, got %s %s", tl.Tools[0].Name, tl.Tools[1].Name)
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
	// todo_list is the 6th slot (index 6 in messages: 0=assistant, 1-6=results)
	todoContent := result.Messages[6].ToolResult.Content
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

func TestHydrationTodoListWithGoal(t *testing.T) {
	port := &fakeTodoPort{
		items: map[string][]domain.TodoItem{
			"conv_1": {
				{ID: "1", Content: "Step 1", Status: domain.TodoInProgress},
			},
		},
		goals: map[string]string{
			"conv_1": "Build a CLI tool that converts Markdown to HTML with custom templates.",
		},
	}
	b := NewHydrationBuilder(HydrationSource{Todos: port, ConvID: "conv_1"})
	result := b.Build()
	todoContent := result.Messages[6].ToolResult.Content
	if !strings.Contains(todoContent, "USER GOAL") {
		t.Errorf("expected USER GOAL header, got: %s", todoContent)
	}
	if !strings.Contains(todoContent, "Build a CLI tool that converts Markdown") {
		t.Errorf("expected goal text, got: %s", todoContent)
	}
	if !strings.Contains(todoContent, "CURRENT TASKS") {
		t.Errorf("expected CURRENT TASKS header, got: %s", todoContent)
	}
	// Goal should appear before tasks
	goalIdx := strings.Index(todoContent, "USER GOAL")
	tasksIdx := strings.Index(todoContent, "CURRENT TASKS")
	if goalIdx == -1 || tasksIdx == -1 || goalIdx > tasksIdx {
		t.Errorf("goal should appear before tasks, goalIdx=%d tasksIdx=%d", goalIdx, tasksIdx)
	}
}

func TestHydrationTodoListGoalOnly(t *testing.T) {
	port := &fakeTodoPort{
		items: map[string][]domain.TodoItem{},
		goals: map[string]string{
			"conv_1": "Refactor the auth module to use JWT.",
		},
	}
	b := NewHydrationBuilder(HydrationSource{Todos: port, ConvID: "conv_1"})
	result := b.Build()
	todoContent := result.Messages[6].ToolResult.Content
	if !strings.Contains(todoContent, "USER GOAL") {
		t.Errorf("expected USER GOAL header, got: %s", todoContent)
	}
	if strings.Contains(todoContent, "CURRENT TASKS") {
		t.Errorf("should not have CURRENT TASKS when no items, got: %s", todoContent)
	}
}

func TestHydrationTodoListEmpty(t *testing.T) {
	port := &fakeTodoPort{items: map[string][]domain.TodoItem{}}
	b := NewHydrationBuilder(HydrationSource{Todos: port, ConvID: "conv_1"})
	result := b.Build()
	todoContent := result.Messages[6].ToolResult.Content
	if todoContent != "" {
		t.Errorf("expected empty content for no todos, got: %s", todoContent)
	}
}

func TestHydrationTodoListNilPort(t *testing.T) {
	b := NewHydrationBuilder(HydrationSource{})
	result := b.Build()
	todoContent := result.Messages[6].ToolResult.Content
	if todoContent != "" {
		t.Errorf("expected empty content for nil todo port, got: %s", todoContent)
	}
}

// fakeTodoPort is a minimal in-memory ConversationTodoPort for testing.
type fakeTodoPort struct {
	items map[string][]domain.TodoItem
	goals map[string]string
}

func (f *fakeTodoPort) Get(convID string) []domain.TodoItem {
	return f.items[convID]
}

func (f *fakeTodoPort) GetGoal(convID string) string {
	if f.goals == nil {
		return ""
	}
	return f.goals[convID]
}

func (f *fakeTodoPort) Set(convID string, items []domain.TodoItem) {
	if f.items == nil {
		f.items = map[string][]domain.TodoItem{}
	}
	f.items[convID] = items
}

func (f *fakeTodoPort) SetGoal(convID string, goal string) {
	if f.goals == nil {
		f.goals = map[string]string{}
	}
	f.goals[convID] = goal
}

func (f *fakeTodoPort) Clear(convID string) {
	delete(f.items, convID)
	delete(f.goals, convID)
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
