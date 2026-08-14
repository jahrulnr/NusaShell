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

// stubSkillStoreHyd is a minimal SkillStore for hydration tests.
type stubSkillStoreHyd struct{ skills []*domain.Skill }

func (s *stubSkillStoreHyd) List() []*domain.Skill { return s.skills }
func (s *stubSkillStoreHyd) Get(id string) (*domain.Skill, error) {
	return nil, fmt.Errorf("not found")
}
func (s *stubSkillStoreHyd) Save(sk *domain.Skill) error { return nil }
func (s *stubSkillStoreHyd) Delete(id string) error      { return nil }

// stubMCPStoreHyd is a minimal MCPServerStore for hydration tests.
type stubMCPStoreHyd struct{ servers []*domain.MCPServer }

func (s *stubMCPStoreHyd) List() []*domain.MCPServer { return s.servers }
func (s *stubMCPStoreHyd) Get(id string) (*domain.MCPServer, error) {
	return nil, fmt.Errorf("not found")
}
func (s *stubMCPStoreHyd) Save(srv *domain.MCPServer) error { return nil }
func (s *stubMCPStoreHyd) Delete(id string) error           { return nil }

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
	if result.CallCount != 5 {
		t.Fatalf("expected 5 hydration calls, got %d", result.CallCount)
	}
	if len(result.Messages) != 6 { // 1 assistant + 5 tool results
		t.Fatalf("expected 6 messages, got %d", len(result.Messages))
	}
	// First message: assistant with toolCalls
	if result.Messages[0].Role != "assistant" {
		t.Errorf("expected first message role=assistant, got %s", result.Messages[0].Role)
	}
	if len(result.Messages[0].ToolCalls) != 5 {
		t.Errorf("expected 5 toolCalls, got %d", len(result.Messages[0].ToolCalls))
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
		Memory: &stubMemStore{entries: []*domain.MemoryEntry{
			{ID: "m1", Content: "User likes Go", Tags: []string{"pref"}},
		}},
	})
	result := b.Build()
	// memory is the second tool result (index 2)
	memContent := result.Messages[2].ToolResult.Content
	var mem struct {
		Count   int `json:"count"`
		Entries []struct {
			ID      string   `json:"id"`
			Content string   `json:"content"`
			Tags    []string `json:"tags"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(memContent), &mem); err != nil {
		t.Fatalf("invalid memory JSON: %v", err)
	}
	if mem.Count != 1 {
		t.Errorf("expected 1 memory entry, got %d", mem.Count)
	}
	if len(mem.Entries) != 1 || mem.Entries[0].Content != "User likes Go" {
		t.Errorf("unexpected memory entries: %+v", mem.Entries)
	}
}

func TestHydrationMemoryNil(t *testing.T) {
	b := NewHydrationBuilder(HydrationSource{})
	result := b.Build()
	memContent := result.Messages[2].ToolResult.Content
	if memContent != "{}" {
		t.Errorf("expected {} for nil memory, got %s", memContent)
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
		MCPServers: &stubMCPStoreHyd{servers: []*domain.MCPServer{
			{ID: "srv1", Name: "github", Enabled: true},
			{ID: "srv2", Name: "fs", Enabled: true},
		}},
		MCP: &stubMCPReader{tools: map[string][]contracts.MCPToolDTO{
			"srv1": {{Name: "create_issue"}},
		}},
	})
	result := b.Build()
	mcpContent := result.Messages[4].ToolResult.Content
	var mcp struct {
		Running []struct {
			Name  string `json:"name"`
			Tools int    `json:"tools"`
		} `json:"running"`
	}
	if err := json.Unmarshal([]byte(mcpContent), &mcp); err != nil {
		t.Fatalf("invalid mcp_list JSON: %v", err)
	}
	if len(mcp.Running) != 1 {
		t.Fatalf("expected 1 running server, got %d", len(mcp.Running))
	}
	if mcp.Running[0].Name != "github" || mcp.Running[0].Tools != 1 {
		t.Errorf("expected github with 1 tool, got %+v", mcp.Running[0])
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
