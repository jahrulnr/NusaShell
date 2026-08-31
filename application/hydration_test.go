package application

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
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
	// The memory slot reads primary.md via file_read and enriches the body
	// with usage stats. The file body carries its own YAML frontmatter
	// (last_updated/version) which must be stripped from the slot content.
	primaryPath := "/data/memory/primary.md"
	exec := &stubHydrationExecutor{fn: func(name string, _ []byte) (string, error) {
		switch name {
		case "file_read":
			// file_read yamlMD output: meta block + raw file body.
			return "---\nbytes: 60\n---\n\n" +
				"---\nlast_updated: \"2026-01-01T00:00:00Z\"\nversion: 2\n---\n\n" +
				"User prefers Indonesian. Repo uses Go + Clean Architecture.", nil
		case "skill", "mcp_list", "tool_list":
			return emptyToolOutput, nil
		}
		return "", fmt.Errorf("unexpected tool %q", name)
	}}
	b := NewHydrationBuilder(HydrationSource{Executor: exec, PrimaryPath: primaryPath})
	result := b.Build()
	if got := result.Messages[0].ToolCalls[1].Args; got != `{"path":"/data/memory/primary.md"}` {
		t.Errorf("memory call args = %s, want file_read path", got)
	}
	// memory is the second slot (after runtime_context)
	memContent := hydrationResultByName(t, result, "memory")
	var mem struct {
		Count   int `json:"count"`
		Entries []struct {
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
	if mem.Count != 1 {
		t.Errorf("expected 1 primary entry, got %d", mem.Count)
	}
	if len(mem.Entries) != 1 {
		t.Fatalf("unexpected entries: %+v", mem.Entries)
	}
	wantBody := "User prefers Indonesian. Repo uses Go + Clean Architecture."
	if mem.Entries[0].Content != wantBody {
		t.Errorf("entry content = %q, want %q", mem.Entries[0].Content, wantBody)
	}
	// Usage should report the primary char budget.
	if mem.Usage.Limit != domain.PrimaryCharCap {
		t.Errorf("limit = %d, want %d", mem.Usage.Limit, domain.PrimaryCharCap)
	}
	if mem.Usage.Chars != len(wantBody) {
		t.Errorf("chars = %d, want %d", mem.Usage.Chars, len(wantBody))
	}
}

// TestHydrationAgentsMD pins the forced AGENTS.md injection: the slot is a
// REAL file_read call against <workspace>/AGENTS.md, attached verbatim, and
// positioned right after runtime_context so project instructions lead.
func TestHydrationAgentsMD(t *testing.T) {
	agentsOut := "---\nbytes: 42\n---\n\n# Project rules\nUse Go, keep it simple."
	wantPath := filepath.Join("/ws/proj", "AGENTS.md")
	exec := &stubHydrationExecutor{fn: func(name string, args []byte) (string, error) {
		switch name {
		case "file_read":
			// Decode the JSON args and compare the path field so the check
			// is platform-neutral (Windows backslashes are escaped in the
			// raw JSON string).
			var fa struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(args, &fa); err == nil && fa.Path == wantPath {
				return agentsOut, nil
			}
			return "", fmt.Errorf("unexpected file_read args: %s", args)
		case "skill", "mcp_list", "tool_list":
			return emptyToolOutput, nil
		}
		return "", fmt.Errorf("unexpected tool %q", name)
	}}
	b := NewHydrationBuilder(HydrationSource{
		Executor:       exec,
		RuntimeContext: RuntimeContextSnapshot{Workspace: "/ws/proj"},
	})
	result := b.Build()

	// The slot is a genuine file_read call with the workspace AGENTS.md path.
	var idx = -1
	for i, c := range result.Messages[0].ToolCalls {
		if c.Name == "file_read" {
			idx = i
			// Decode the args and compare the path field so the check is
			// platform-neutral (Windows backslashes are escaped in the raw
			// JSON string).
			var fa struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal([]byte(c.Args), &fa); err != nil || fa.Path != wantPath {
				t.Errorf("file_read args = %s, want AGENTS.md path", c.Args)
			}
		}
	}
	if idx < 0 {
		t.Fatal("file_read (AGENTS.md) slot missing from hydration transcript")
	}
	// Positioned right after runtime_context.
	if idx != 1 || result.Messages[0].ToolCalls[0].Name != "runtime_context" {
		t.Errorf("AGENTS.md slot must follow runtime_context, got index %d (first=%s)",
			idx, result.Messages[0].ToolCalls[0].Name)
	}
	// Verbatim real tool output.
	if got := result.Messages[idx+1].ToolResult.Content; got != agentsOut {
		t.Errorf("AGENTS.md slot must carry the real file_read output verbatim:\n got %q\nwant %q", got, agentsOut)
	}
}

// TestHydrationAgentsMDHidden covers the fail-soft rules: no workspace, a
// missing file (file_read error), or an empty body all hide the slot.
func TestHydrationAgentsMDHidden(t *testing.T) {
	// No workspace → hidden even with an executor.
	exec := &stubHydrationExecutor{fn: func(name string, _ []byte) (string, error) {
		switch name {
		case "skill", "mcp_list", "tool_list":
			return emptyToolOutput, nil
		}
		return "", fmt.Errorf("unexpected tool %q", name)
	}}
	result := NewHydrationBuilder(HydrationSource{Executor: exec}).Build()
	for _, c := range result.Messages[0].ToolCalls {
		if c.Name == "file_read" {
			t.Fatal("AGENTS.md slot must be hidden without a workspace")
		}
	}

	// file_read error (missing file) → hidden.
	exec = &stubHydrationExecutor{fn: func(name string, _ []byte) (string, error) {
		switch name {
		case "file_read":
			return "", fmt.Errorf("open /ws/proj/AGENTS.md: no such file or directory")
		case "skill", "mcp_list", "tool_list":
			return emptyToolOutput, nil
		}
		return "", fmt.Errorf("unexpected tool %q", name)
	}}
	result = NewHydrationBuilder(HydrationSource{
		Executor:       exec,
		RuntimeContext: RuntimeContextSnapshot{Workspace: "/ws/proj"},
	}).Build()
	for _, c := range result.Messages[0].ToolCalls {
		if c.Name == "file_read" {
			t.Fatal("AGENTS.md slot must be hidden when the file is missing")
		}
	}

	// Empty body → hidden.
	exec = &stubHydrationExecutor{fn: func(name string, _ []byte) (string, error) {
		switch name {
		case "file_read":
			return "---\nbytes: 0\n---\n", nil
		case "skill", "mcp_list", "tool_list":
			return emptyToolOutput, nil
		}
		return "", fmt.Errorf("unexpected tool %q", name)
	}}
	result = NewHydrationBuilder(HydrationSource{
		Executor:       exec,
		RuntimeContext: RuntimeContextSnapshot{Workspace: "/ws/proj"},
	}).Build()
	for _, c := range result.Messages[0].ToolCalls {
		if c.Name == "file_read" {
			t.Fatal("AGENTS.md slot must be hidden when the file body is empty")
		}
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
	// Executor + PrimaryPath present but the primary.md body is empty:
	// also hidden.
	exec := &stubHydrationExecutor{fn: func(name string, _ []byte) (string, error) {
		switch name {
		case "file_read":
			// file_read of an empty primary.md: meta block + frontmatter only.
			return "---\nbytes: 0\n---\n\n---\nversion: 2\n---\n", nil
		case "skill", "mcp_list", "tool_list":
			return emptyToolOutput, nil
		}
		return "", fmt.Errorf("unexpected tool %q", name)
	}}
	result = NewHydrationBuilder(HydrationSource{Executor: exec, PrimaryPath: "/data/memory/primary.md"}).Build()
	for _, c := range result.Messages[0].ToolCalls {
		if c.Name == "memory" {
			t.Fatal("memory slot must be hidden when primary memory is empty")
		}
	}
}

func TestHydrationSkillsRealOutput(t *testing.T) {
	// The skill slot attaches the real tool output (op=list) verbatim.
	skillOutput := "---\ncount: 2\n---\n" +
		`{"name":"alpha","description":"a"}` + "\n" +
		`{"name":"zebra","description":"z"}`
	exec := &stubHydrationExecutor{fn: func(name string, _ []byte) (string, error) {
		switch name {
		case "skill":
			return skillOutput, nil
		case "memory", "mcp_list", "tool_list":
			return emptyToolOutput, nil
		}
		return "", fmt.Errorf("unexpected tool %q", name)
	}}
	b := NewHydrationBuilder(HydrationSource{Executor: exec})
	result := b.Build()
	if got := hydrationResultByName(t, result, "skill"); got != skillOutput {
		t.Fatalf("skill slot must contain the real tool output verbatim:\n got %q\nwant %q", got, skillOutput)
	}
}

func TestHydrationSkillsHiddenWhenEmpty(t *testing.T) {
	exec := &stubHydrationExecutor{fn: func(name string, _ []byte) (string, error) {
		switch name {
		case "memory", "skill", "mcp_list", "tool_list":
			return emptyToolOutput, nil
		}
		return "", fmt.Errorf("unexpected tool %q", name)
	}}
	result := NewHydrationBuilder(HydrationSource{Executor: exec}).Build()
	for _, c := range result.Messages[0].ToolCalls {
		if c.Name == "skill" {
			t.Fatal("skill slot must be hidden when the skill library is empty")
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
			case "memory", "skill", "tool_list":
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
		case "memory", "skill", "mcp_list", "tool_list":
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
		case "memory", "skill":
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
	if !strings.Contains(todoContent, "[~] (2) Add parser") {
		t.Errorf("expected in_progress item with ID, got: %s", todoContent)
	}
	if !strings.Contains(todoContent, "[ ] (3) Write tests") {
		t.Errorf("expected pending item with ID, got: %s", todoContent)
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

func (f *fakeTodoPort) ClearBrief(convID string) error {
	delete(f.briefs, convID)
	return nil
}

func (f *fakeTodoPort) PlanPath(convID string) string {
	if f.briefs[convID] == "" {
		return ""
	}
	return "/tmp/plans/" + convID + ".plan.md"
}

func (f *fakeTodoPort) Patch(convID string, patches []domain.TodoItem) {
	if f.items == nil {
		f.items = map[string][]domain.TodoItem{}
	}
	existing := f.items[convID]
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
	f.items[convID] = existing
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

type stubProjectMemoryStore struct {
	extract domain.ProjectIndexExtract
	ok      bool
}

func (s *stubProjectMemoryStore) Query(string, domain.ProjectMemoryQuery) ([]domain.ProjectMemoryHit, error) {
	return nil, nil
}
func (s *stubProjectMemoryStore) List(string) ([]string, error) { return nil, nil }
func (s *stubProjectMemoryStore) Read(string, string, string) (string, error) {
	return "", nil
}
func (s *stubProjectMemoryStore) Admit(string, string, string, string) (domain.ProjectMemoryAdmitResult, error) {
	return domain.ProjectMemoryAdmitResult{}, nil
}
func (s *stubProjectMemoryStore) Archive(string, string) error { return nil }
func (s *stubProjectMemoryStore) Lint(string) ([]domain.ProjectMemoryLintProblem, error) {
	return nil, nil
}
func (s *stubProjectMemoryStore) IndexExtract(string) (domain.ProjectIndexExtract, bool, error) {
	return s.extract, s.ok, nil
}

func hydrationHasSlot(result HydrationResult, name string) bool {
	if len(result.Messages) == 0 {
		return false
	}
	for _, c := range result.Messages[0].ToolCalls {
		if c.Name == name {
			return true
		}
	}
	return false
}

func TestHydrationProjectMemoryPresent(t *testing.T) {
	b := NewHydrationBuilder(HydrationSource{
		RuntimeContext: RuntimeContextSnapshot{Workspace: "/apps/payments/api"},
		ProjectMemory: &stubProjectMemoryStore{
			ok: true,
			extract: domain.ProjectIndexExtract{
				Purpose: "payments API",
				Locks:   "never rewrite auth",
			},
		},
	})
	result := b.Build()
	if !hydrationHasSlot(result, "memory_project") {
		t.Fatal("expected memory_project slot when index extract is present")
	}
	body := hydrationResultByName(t, result, "memory_project")
	if !strings.Contains(body, "payments API") || !strings.Contains(body, "never rewrite auth") {
		t.Fatalf("extract body = %s", body)
	}
}

func TestHydrationProjectMemoryHiddenWithoutWorkspace(t *testing.T) {
	b := NewHydrationBuilder(HydrationSource{
		ProjectMemory: &stubProjectMemoryStore{
			ok:      true,
			extract: domain.ProjectIndexExtract{Purpose: "x"},
		},
	})
	if hydrationHasSlot(b.Build(), "memory_project") {
		t.Fatal("memory_project must hide without workspace")
	}
}

func TestHydrationProjectMemoryHiddenOnEmptyStore(t *testing.T) {
	b := NewHydrationBuilder(HydrationSource{
		RuntimeContext: RuntimeContextSnapshot{Workspace: "/apps/x"},
		ProjectMemory:  &stubProjectMemoryStore{ok: false},
	})
	if hydrationHasSlot(b.Build(), "memory_project") {
		t.Fatal("memory_project must hide when index is missing")
	}
	b = NewHydrationBuilder(HydrationSource{
		RuntimeContext: RuntimeContextSnapshot{Workspace: "/apps/x"},
	})
	if hydrationHasSlot(b.Build(), "memory_project") {
		t.Fatal("memory_project must hide when store is nil")
	}
}
