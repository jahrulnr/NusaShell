package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"nusashell/application/subagent"
	"nusashell/domain"
	"nusashell/infrastructure/ai/core"
	"path/filepath"
	"strings"
	"testing"
)

// --- from hydration_test.go ---

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
func (s *stubSkillStoreHyd) Promote(id, ownedBy string) (*domain.Skill, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *stubSkillStoreHyd) Rollback(id, ownedBy string, version int) (*domain.Skill, error) {
	return nil, fmt.Errorf("not implemented")
}

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

func hydrationResultByFilePath(t *testing.T, result HydrationResult, path string) string {
	t.Helper()
	for i, c := range result.Messages[0].ToolCalls {
		if c.Name != "file_read" {
			continue
		}
		var args struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal([]byte(c.Args), &args); err != nil {
			t.Fatalf("file_read args for %q are invalid JSON: %v", path, err)
		}
		if args.Path != path {
			continue
		}
		r := result.Messages[i+1]
		if r.ToolResult == nil {
			t.Fatalf("file_read slot for %q has no tool result", path)
		}
		return r.ToolResult.Content
	}
	t.Fatalf("file_read slot for %q not found in hydration transcript", path)
	return ""
}

func TestHydrationMemoryUsesFileReadForEachDocument(t *testing.T) {
	userPath := "/data/memory/user.md"
	soulPath := "/data/memory/soul.md"
	userOutput := "---\nbytes: 22\n---\n\n---\nversion: 2\n---\n\nUser context."
	soulOutput := "---\nbytes: 21\n---\n\n---\nversion: 2\n---\n\nSoul context."
	exec := &stubHydrationExecutor{fn: func(name string, args []byte) (string, error) {
		switch name {
		case "file_read":
			var fileArgs struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(args, &fileArgs); err != nil {
				return "", err
			}
			switch fileArgs.Path {
			case userPath:
				return userOutput, nil
			case soulPath:
				return soulOutput, nil
			default:
				return "", fmt.Errorf("unexpected file path %q", fileArgs.Path)
			}
		case "skill", "mcp_list", "tool_list":
			return emptyToolOutput, nil
		default:
			return "", fmt.Errorf("unexpected tool %q", name)
		}
	}}

	result := NewHydrationBuilder(HydrationSource{
		Executor:  exec,
		UserPath:  userPath,
		AgentPath: soulPath,
	}).Build()

	for _, call := range result.Messages[0].ToolCalls {
		if call.Name == "memory" {
			t.Fatalf("memory hydration summary must be replaced by direct file_read calls: %+v", call)
		}
	}
	if got := hydrationResultByFilePath(t, result, userPath); got != userOutput {
		t.Errorf("user file_read result = %q, want verbatim output %q", got, userOutput)
	}
	if got := hydrationResultByFilePath(t, result, soulPath); got != soulOutput {
		t.Errorf("soul file_read result = %q, want verbatim output %q", got, soulOutput)
	}
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

func TestHydrationRuntimeContextListsInstructionFiles(t *testing.T) {
	dir := instructionFixture(t)
	app := &App{Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": {ID: "c1"}}}}
	msgs := app.buildHydration(&domain.Conversation{ID: "c1", Workspace: dir})
	runtimeSlot := findHydrationTool(msgs, "runtime_context")
	if runtimeSlot == nil {
		t.Fatal("runtime_context hydration slot missing")
	}
	var ctx RuntimeContextSnapshot
	if err := json.Unmarshal([]byte(runtimeSlot.Content), &ctx); err != nil {
		t.Fatalf("runtime_context payload not JSON: %v", err)
	}
	if ctx.Workspace != dir {
		t.Fatalf("workspace = %q, want %q", ctx.Workspace, dir)
	}
	joined := strings.Join(ctx.InstructionFiles, ",")
	for _, want := range []string{"AGENTS.md", "application/AGENTS.md", "frontend/AGENTS.md"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("instructionFiles missing %s: %v", want, ctx.InstructionFiles)
		}
	}
	for _, p := range ctx.InstructionFiles {
		if strings.Contains(p, "node_modules") || strings.Contains(p, "vendor") || strings.Contains(p, ".experimental") {
			t.Fatalf("instructionFiles leaked ignored path %q", p)
		}
	}
}

func TestHydrationMemory(t *testing.T) {
	// Each always-injected memory document is represented by its own real
	// file_read call/result pair. The result is kept verbatim, including the
	// metadata and document frontmatter returned by file_read.
	userPath := "/data/memory/user.md"
	soulPath := "/data/memory/soul.md"
	userBody := "User prefers Indonesian. Repo uses Go + Clean Architecture."
	soulBody := "Soul conventions: gate scripts must be stdlib-only."
	front := "---\nbytes: 60\n---\n\n---\nlast_updated: \"2026-01-01T00:00:00Z\"\nversion: 2\n---\n\n"
	userOutput := front + userBody
	soulOutput := front + soulBody
	exec := &stubHydrationExecutor{fn: func(name string, args []byte) (string, error) {
		switch name {
		case "file_read":
			var a struct {
				Path string `json:"path"`
			}
			_ = json.Unmarshal(args, &a)
			// file_read yamlMD output: meta block + raw file body.
			switch a.Path {
			case userPath:
				return front + userBody, nil
			case soulPath:
				return front + soulBody, nil
			}
			return "", fmt.Errorf("unexpected path %q", a.Path)
		case "skill", "mcp_list", "tool_list":
			return emptyToolOutput, nil
		}
		return "", fmt.Errorf("unexpected tool %q", name)
	}}
	b := NewHydrationBuilder(HydrationSource{Executor: exec, UserPath: userPath, AgentPath: soulPath})
	result := b.Build()
	for _, call := range result.Messages[0].ToolCalls {
		if call.Name == "memory" {
			t.Fatalf("memory hydration summary must not replace file_read calls: %+v", call)
		}
	}
	if got := hydrationResultByFilePath(t, result, userPath); got != userOutput {
		t.Errorf("user file_read result = %q, want verbatim output", got)
	}
	if got := hydrationResultByFilePath(t, result, soulPath); got != soulOutput {
		t.Errorf("soul file_read result = %q, want verbatim output", got)
	}
}

func TestHydrationMemoryUserOnlyWithoutAgentPath(t *testing.T) {
	// When no AgentPath is configured only the user document is emitted.
	userPath := "/data/memory/user.md"
	userBody := "User prefers Indonesian."
	userOutput := "---\nbytes: 60\n---\n\n---\nlast_updated: \"2026-01-01T00:00:00Z\"\nversion: 2\n---\n\n" + userBody
	exec := &stubHydrationExecutor{fn: func(name string, _ []byte) (string, error) {
		switch name {
		case "file_read":
			return userOutput, nil
		case "skill", "mcp_list", "tool_list":
			return emptyToolOutput, nil
		}
		return "", fmt.Errorf("unexpected tool %q", name)
	}}
	b := NewHydrationBuilder(HydrationSource{Executor: exec, UserPath: userPath})
	result := b.Build()
	if got := hydrationResultByFilePath(t, result, userPath); got != userOutput {
		t.Errorf("user-only file_read result = %q, want %q", got, userOutput)
	}
	for _, call := range result.Messages[0].ToolCalls {
		if call.Name == "memory" {
			t.Fatalf("memory hydration summary must not be emitted: %+v", call)
		}
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
	// No executor: memory file_read slots are hidden, not emitted as empty stubs.
	b := NewHydrationBuilder(HydrationSource{})
	result := b.Build()
	for _, c := range result.Messages[0].ToolCalls {
		if c.Name == "file_read" {
			t.Fatal("memory file_read slots must be hidden when the real tool is unavailable")
		}
	}
	// Executor + both memory paths present but their document bodies are empty:
	// both file_read slots are hidden independently.
	exec := &stubHydrationExecutor{fn: func(name string, _ []byte) (string, error) {
		switch name {
		case "file_read":
			// file_read of an empty memory document: meta block + frontmatter only.
			return "---\nbytes: 0\n---\n\n---\nversion: 2\n---\n", nil
		case "skill", "mcp_list", "tool_list":
			return emptyToolOutput, nil
		}
		return "", fmt.Errorf("unexpected tool %q", name)
	}}
	result = NewHydrationBuilder(HydrationSource{
		Executor:  exec,
		UserPath:  "/data/memory/user.md",
		AgentPath: "/data/memory/soul.md",
	}).Build()
	for _, c := range result.Messages[0].ToolCalls {
		if c.Name == "file_read" {
			t.Fatal("memory file_read slots must be hidden when the document is empty")
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
func (s *stubProjectMemoryStore) Lint(string, ...string) ([]domain.ProjectMemoryLintProblem, error) {
	return nil, nil
}
func (s *stubProjectMemoryStore) IndexExtract(string) (domain.ProjectIndexExtract, bool, error) {
	return s.extract, s.ok, nil
}
func (s *stubProjectMemoryStore) Audit(string) (string, error) { return "", nil }
func (s *stubProjectMemoryStore) Gate(string, string) (string, error) {
	return "", nil
}
func (s *stubProjectMemoryStore) TrackPatterns(string, string) (string, error) {
	return "", nil
}
func (s *stubProjectMemoryStore) Path(string, string, bool) (string, error) { return "", nil }
func (s *stubProjectMemoryStore) ScriptPath(string, string, bool) (string, error) {
	return "", nil
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

func TestHydrationApplyBlockHiddenWhenEmpty(t *testing.T) {
	result := NewHydrationBuilder(HydrationSource{
		RuntimeContext: RuntimeContextSnapshot{
			CurrentDate: "2026-01-01T00:00:00Z",
			Environment: "test",
			RuntimeOS:   "linux/amd64",
		},
	}).Build()
	if hydrationHasSlot(result, "memory") {
		t.Fatal("empty ApplyBlock must hide the memory list slot")
	}
}

func TestHydrationApplyBlockShownWithListOp(t *testing.T) {
	block := "APPLY:\n- [project] prefer Go over Rust"
	result := NewHydrationBuilder(HydrationSource{
		RuntimeContext: RuntimeContextSnapshot{
			CurrentDate: "2026-01-01T00:00:00Z",
			Environment: "test",
			RuntimeOS:   "linux/amd64",
		},
		ApplyBlock: block,
	}).Build()
	if !hydrationHasSlot(result, "memory") {
		t.Fatal("ApplyBlock must appear as a memory slot")
	}
	var args string
	for _, c := range result.Messages[0].ToolCalls {
		if c.Name == "memory" {
			args = c.Args
			break
		}
	}
	if args != `{"op":"list"}` {
		t.Fatalf("memory slot args=%q, want {\"op\":\"list\"}", args)
	}
	if got := hydrationResultByName(t, result, "memory"); got != block {
		t.Fatalf("ApplyBlock content=%q", got)
	}
}

// --- from hydration_background_runs_test.go ---

// TestHydrationIncludesPendingBackgroundRuns covers the async-tool edge case
// for hydration: after compaction (or at any hydration epoch), the runtime
// context slot must list the active background-agent runs (subagent,
// delegate) so the model knows which background tools were spawned and are
// still pending — correlatable with the later synthetic result calls by ID.
func TestHydrationIncludesPendingBackgroundRuns(t *testing.T) {
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": {ID: "c1"}}},
		pendingRuns:   map[string]map[string]string{},
	}
	// The internal delegate run stays live: the headless turn goroutine is
	// never started, so the runtime keeps the run registered with its
	// worker detail.
	svc := subagent.New(subagent.Deps{
		Go:           func(_ string, fn func()) {},
		ResolveModel: func(string) (string, error) { return "glm-5-2", nil },
		TrackPending: app.trackPendingRun,
	})
	app.subagentSvc = svc

	// Two pending runs: an ACP subagent (details not tracked) and an
	// internal delegate (carries an AcpRun with worker detail).
	app.trackPendingRun("c1", "run-b", "subagent")
	out, err := svc.SpawnSubagents(context.Background(), "c1", "call_parent", []byte(`{"prompt":"inspect","agent_id":"internal","workspace":"/ws"}`))
	if err != nil {
		t.Fatalf("spawn internal delegate: %v", err)
	}
	runID := firstSpawnedRunID(t, out)

	conv := &domain.Conversation{ID: "c1", Workspace: "/ws"}
	msgs := app.buildHydration(conv)
	runtimeSlot := findHydrationTool(msgs, "runtime_context")
	if runtimeSlot == nil {
		t.Fatalf("runtime_context hydration slot missing; slots: %s", hydrationToolNames(msgs))
	}
	var ctx RuntimeContextSnapshot
	if err := json.Unmarshal([]byte(runtimeSlot.Content), &ctx); err != nil {
		t.Fatalf("runtime_context payload not JSON: %v", err)
	}
	if len(ctx.BackgroundRuns) != 2 {
		t.Fatalf("backgroundRuns = %d, want 2 (%s)", len(ctx.BackgroundRuns), runtimeSlot.Content)
	}
	// Deterministic ID order: '-' sorts before '_'.
	if ctx.BackgroundRuns[0].ID != "run-b" || ctx.BackgroundRuns[1].ID != runID {
		t.Fatalf("backgroundRuns not ID-sorted: %+v", ctx.BackgroundRuns)
	}
	delegate := ctx.BackgroundRuns[1]
	if delegate.Tool != "subagent" || delegate.Model != "glm-5-2" || delegate.Agent != "NusaShell delegate" || delegate.Workspace != "/ws" {
		t.Fatalf("delegate details missing: %+v", delegate)
	}
	if ctx.BackgroundRuns[0].Tool != "subagent" {
		t.Fatalf("subagent tool missing: %+v", ctx.BackgroundRuns[1])
	}
}

// TestHydrationOmitsBackgroundRunsWhenNonePending: with no active runs the
// runtime context slot must not carry a backgroundRuns field at all.
func TestHydrationOmitsBackgroundRunsWhenNonePending(t *testing.T) {
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": {ID: "c1"}}},
		pendingRuns:   map[string]map[string]string{},
	}
	msgs := app.buildHydration(&domain.Conversation{ID: "c1"})
	runtimeSlot := findHydrationTool(msgs, "runtime_context")
	if runtimeSlot == nil {
		t.Fatalf("runtime_context hydration slot missing")
	}
	if strings.Contains(runtimeSlot.Content, "backgroundRuns") {
		t.Fatalf("backgroundRuns present with no pending runs: %s", runtimeSlot.Content)
	}
}

// TestCompactionRehydrationKeepsPendingBackgroundRuns: persistCompactedConversation
// rebuilds hydration; the rebuilt runtime context must still list runs that
// are pending at compaction time, so a continuation agent knows the
// background agents are still out there.
func TestCompactionRehydrationKeepsPendingBackgroundRuns(t *testing.T) {
	conv := &domain.Conversation{ID: "c1", Messages: []domain.Message{
		{ID: "u1", Role: domain.RoleUser, Content: strings.Repeat("q", 200), Status: domain.StatusDone},
		{ID: "a1", Role: domain.RoleAssistant, Content: "ans", Status: domain.StatusDone},
	}}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	app := &App{
		Conversations: store,
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		pendingRuns:   map[string]map[string]string{},
	}
	app.trackPendingRun("c1", "run-x", "subagent")

	if err := app.persistCompactedConversation(conv, "summary", 64_000); err != nil {
		t.Fatalf("persistCompactedConversation: %v", err)
	}
	saved, err := store.Get("c1")
	if err != nil {
		t.Fatal(err)
	}
	var payload string
	for i := range saved.Messages {
		for j := range saved.Messages[i].ToolCalls {
			tc := &saved.Messages[i].ToolCalls[j]
			if tc.Name == "runtime_context" && tc.Output != "" {
				payload = tc.Output
			}
		}
	}
	if payload == "" {
		t.Fatalf("no runtime_context payload after compaction re-hydration")
	}
	var ctx RuntimeContextSnapshot
	if err := json.Unmarshal([]byte(payload), &ctx); err != nil {
		t.Fatalf("runtime_context payload not JSON: %v", err)
	}
	if len(ctx.BackgroundRuns) != 1 || ctx.BackgroundRuns[0].ID != "run-x" {
		t.Fatalf("backgroundRuns after compaction = %+v, want run-x", ctx.BackgroundRuns)
	}
}

// findHydrationTool returns the tool result of the first hydration slot
// matching name.
func findHydrationTool(msgs []ChatMessage, name string) *ToolResult {
	for _, m := range msgs {
		if m.Role != "tool" || m.ToolResult == nil {
			continue
		}
		if m.ToolResult.Name == name {
			return m.ToolResult
		}
	}
	return nil
}

// hydrationToolNames lists the tool names present in a hydration transcript
// (for failure messages).
func hydrationToolNames(msgs []ChatMessage) string {
	var names []string
	for _, m := range msgs {
		if m.Role == "tool" && m.ToolResult != nil {
			names = append(names, m.ToolResult.Name)
		}
	}
	return strings.Join(names, ",")
}

// --- from hydration_position_test.go ---

// hydrationPositionFixture returns a conversation shaped like the moment a
// checkpoint is built: [user, empty assistant placeholder].
func hydrationPositionFixture() *domain.Conversation {
	c := domain.NewConversation("conv_pos", "position")
	c.Messages = append(c.Messages,
		domain.Message{ID: "m_user", Role: domain.RoleUser, Content: "halo"},
		domain.Message{ID: "m_asst", Role: domain.RoleAssistant},
	)
	return c
}

func hydrationPositionMsgs() []ChatMessage {
	id := domain.HydrateToolCallPrefix + "ab12cd34_0"
	return []ChatMessage{
		{Role: "assistant", ToolCalls: []domain.ToolCall{{ID: id, Name: "runtime_context", Args: "{}"}}},
		{Role: "tool", ToolResult: &ToolResult{ToolCallID: id, Name: "runtime_context", Content: "{}"}},
	}
}

// TestPersistHydrationInsertsBeforePendingAssistant pins the transcript
// position invariant: system prompt → user → hydration → assistant. The
// checkpoint must land immediately before the current turn's assistant
// placeholder, not at the end of the conversation.
func TestPersistHydrationInsertsBeforePendingAssistant(t *testing.T) {
	app := &App{}
	c := app.persistHydration(hydrationPositionFixture(), hydrationPositionMsgs())

	if len(c.Messages) != 3 {
		t.Fatalf("len(Messages) = %d, want 3 (user, hydration, placeholder)", len(c.Messages))
	}
	hyd := c.Messages[1]
	if hyd.Role != domain.RoleAssistant || len(hyd.ToolCalls) != 1 ||
		!strings.HasPrefix(hyd.ToolCalls[0].ID, domain.HydrateToolCallPrefix) {
		t.Fatalf("Messages[1] is not the hydration assistant: %+v", hyd)
	}
	if hyd.ToolCalls[0].Output != "{}" {
		t.Fatalf("hydration tool output not attached: %q", hyd.ToolCalls[0].Output)
	}
	if c.Messages[2].ID != "m_asst" || c.Messages[2].Content != "" {
		t.Fatalf("assistant placeholder must stay last, got: %+v", c.Messages[2])
	}

	// Provider view: user → hydration assistant+result; empty placeholder skipped.
	msgs := chatMessages(c, "m_asst", ModelCapabilities{})
	if len(msgs) != 3 ||
		msgs[0].Role != "user" ||
		msgs[1].Role != "assistant" || len(msgs[1].ToolCalls) == 0 ||
		!domain.IsHydrationCallID(msgs[1].ToolCalls[0].ID) ||
		msgs[2].Role != "tool" {
		t.Fatalf("provider order violates user → hydration → assistant: %+v", msgs)
	}
	// The checkpoint must still be detected so later rounds reuse it
	// (the domain-level predicate pins this; see domain/hydration.go).
}

// TestPersistHydrationFallsBackToAppendWhenAnchorMissing inserts after the
// last user when the placeholder ID is not present. With only a user
// message that is equivalent to append-at-end.
func TestPersistHydrationFallsBackToAppendWhenAnchorMissing(t *testing.T) {
	app := &App{}
	c := domain.NewConversation("conv_pos2", "position")
	c.Messages = append(c.Messages,
		domain.Message{ID: "m_user", Role: domain.RoleUser, Content: "hi"},
	)

	out := app.persistHydration(c, hydrationPositionMsgs())

	if len(out.Messages) != 2 {
		t.Fatalf("len = %d, want 2", len(out.Messages))
	}
	last := out.Messages[1]
	if last.Role != domain.RoleAssistant || len(last.ToolCalls) == 0 ||
		!strings.HasPrefix(last.ToolCalls[0].ID, domain.HydrateToolCallPrefix) {
		t.Fatalf("fallback did not append the checkpoint: %+v", out.Messages)
	}
}

// TestPersistHydrationDoesNotInsertWithoutUser pins the OpenAI/Claude
// constraint: an assistant+tool turn cannot sit under the system prompt with
// no user message yet. Workspace pick on an empty room used to persist the
// checkpoint at index 0; the first user was then appended after it.
func TestPersistHydrationDoesNotInsertWithoutUser(t *testing.T) {
	app := &App{}
	c := domain.NewConversation("conv_empty", "empty")

	out := app.persistHydration(c, hydrationPositionMsgs())

	if len(out.Messages) != 0 {
		t.Fatalf("empty room must not persist hydration before a user exists, got %d messages: %+v", len(out.Messages), out.Messages)
	}
}

// TestChatMessagesEmitsUserBeforeLeadingHydration is the wire invariant
// OpenAI Chat Completions and Anthropic Messages require: after the system
// prompt, the first history role is user, then the hydration assistant+tools.
// A checkpoint persisted at index 0 (empty-room workspace pick) must not be
// replayed in that leading position.
func TestChatMessagesEmitsUserBeforeLeadingHydration(t *testing.T) {
	c := &domain.Conversation{
		ID: "c_lead",
		Messages: []domain.Message{
			hydrationCheckpointMessage(),
			{ID: "u1", Role: domain.RoleUser, Content: "halo", Status: domain.StatusDone},
			{ID: "a1", Role: domain.RoleAssistant, Content: "work", Status: domain.StatusDone},
		},
	}
	msgs := chatMessages(c, "", ModelCapabilities{})
	if len(msgs) < 2 {
		t.Fatalf("got %d provider messages, want at least user + hydration", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Fatalf("provider messages[0] role = %q, want user (got %+v)", msgs[0].Role, rolesOf(msgs))
	}
	if msgs[1].Role != "assistant" || len(msgs[1].ToolCalls) == 0 ||
		!domain.IsHydrationCallID(msgs[1].ToolCalls[0].ID) {
		t.Fatalf("provider messages[1] must be the hydration assistant, got %+v", msgs[1])
	}
}

// TestRelocateHydrationMovesCheckpointAfterFirstUser pins the stored
// transcript repair: [hydration, user, work] becomes [user, hydration, work]
// without rebuilding the checkpoint (prompt-cache IDs stay put).
func TestRelocateHydrationMovesCheckpointAfterFirstUser(t *testing.T) {
	hyd := hydrationCheckpointMessage()
	msgs := []domain.Message{
		hyd,
		{ID: "u1", Role: domain.RoleUser, Content: "halo", Status: domain.StatusDone},
		{ID: "a1", Role: domain.RoleAssistant, Content: "work", Status: domain.StatusDone},
	}
	got := domain.RelocateHydrationAfterFirstUser(msgs)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0].ID != "u1" || !domain.IsHydrationMessage(got[1]) || got[2].ID != "a1" {
		t.Fatalf("order = %s hyd=%v %s, want user, hydration, work", got[0].ID, domain.IsHydrationMessage(got[1]), got[2].ID)
	}
	if got[1].ID != hyd.ID {
		t.Fatalf("checkpoint was rebuilt (id %s → %s); repair must move the existing message", hyd.ID, got[1].ID)
	}
}

// TestRelocateHydrationIsNoOpWhenAlreadyAfterUser keeps a correctly parked
// checkpoint from bouncing around (which would bust the prompt-cache prefix).
func TestRelocateHydrationIsNoOpWhenAlreadyAfterUser(t *testing.T) {
	msgs := []domain.Message{
		{ID: "u1", Role: domain.RoleUser, Content: "halo", Status: domain.StatusDone},
		hydrationCheckpointMessage(),
		{ID: "a1", Role: domain.RoleAssistant, Content: "work", Status: domain.StatusDone},
	}
	got := domain.RelocateHydrationAfterFirstUser(msgs)
	if len(got) != 3 || got[0].ID != "u1" || !domain.IsHydrationMessage(got[1]) || got[2].ID != "a1" {
		t.Fatalf("correct order must stay put: %+v", got)
	}
}

// TestPersistHydrationKeepsRoundTwoPrefixStable verifies the prompt-cache
// property that motivated the insertion point: once round 1's assistant
// message is filled in place, the provider prefix up to and including the
// checkpoint is identical to round 1's — the history only ever grows.
func TestPersistHydrationKeepsRoundTwoPrefixStable(t *testing.T) {
	app := &App{}
	c := app.persistHydration(hydrationPositionFixture(), hydrationPositionMsgs())

	round1 := chatMessages(c, "m_asst", ModelCapabilities{})

	// Round 1 completes: the placeholder is filled in place with content.
	a := &c.Messages[2]
	a.Content = "jawaban"
	a.Status = domain.StatusDone

	round2 := chatMessages(c, "m_asst", ModelCapabilities{})

	if len(round2) < len(round1) {
		t.Fatalf("round2 shorter than round1: %d < %d", len(round2), len(round1))
	}
	for i := range round1 {
		got, want := round2[i], round1[i]
		if got.Role != want.Role || got.Content != want.Content || got.ToolResult == nil != (want.ToolResult == nil) {
			t.Fatalf("prefix diverges at %d: %+v vs round1 %+v", i, got, want)
		}
	}
}

// TestPersistHydrationInsertsAfterLastUserWhenKeepHasPriorRounds is the
// post-compaction shape: handover user, then retained assistant rounds,
// then the in-flight placeholder. Inserting before the placeholder parks
// the checkpoint in the middle of agent work. It must land immediately
// after the last user so the provider sees user → hydration → assistants.
func TestPersistHydrationInsertsAfterLastUserWhenKeepHasPriorRounds(t *testing.T) {
	app := &App{}
	c := domain.NewConversation("conv_mid", "mid")
	c.Messages = append(c.Messages,
		domain.Message{ID: "handover", Role: domain.RoleUser, Content: "[COMPACTION CHECKPOINT]\n## Goal"},
		domain.Message{ID: "old1", Role: domain.RoleAssistant, Content: "Now update main.go:"},
		domain.Message{ID: "old2", Role: domain.RoleAssistant, Content: "\n\n"},
		domain.Message{ID: "m_asst", Role: domain.RoleAssistant},
	)

	c = app.persistHydration(c, hydrationPositionMsgs())

	if len(c.Messages) != 5 {
		t.Fatalf("len(Messages) = %d, want 5", len(c.Messages))
	}
	if c.Messages[0].ID != "handover" {
		t.Fatalf("Messages[0] = %s, want handover", c.Messages[0].ID)
	}
	hyd := c.Messages[1]
	if hyd.Role != domain.RoleAssistant || len(hyd.ToolCalls) != 1 ||
		!strings.HasPrefix(hyd.ToolCalls[0].ID, domain.HydrateToolCallPrefix) {
		t.Fatalf("Messages[1] is not the hydration checkpoint: %+v", hyd)
	}
	if c.Messages[2].ID != "old1" || c.Messages[3].ID != "old2" || c.Messages[4].ID != "m_asst" {
		t.Fatalf("kept assistants shifted wrongly: %+v %+v %+v", c.Messages[2].ID, c.Messages[3].ID, c.Messages[4].ID)
	}

	msgs := chatMessages(c, "m_asst", ModelCapabilities{})
	if len(msgs) < 3 || msgs[0].Role != "user" || msgs[1].Role != "assistant" ||
		len(msgs[1].ToolCalls) == 0 || !domain.IsHydrationCallID(msgs[1].ToolCalls[0].ID) {
		t.Fatalf("provider order must start user → hydration, got %+v", rolesOf(msgs))
	}
}

func rolesOf(msgs []ChatMessage) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Role
	}
	return out
}

type freshTurnStreamAdapter struct {
	first *core.Request
}

func (a *freshTurnStreamAdapter) Name() string { return "fresh-turn" }
func (a *freshTurnStreamAdapter) Chat(context.Context, *core.Request) (*core.Response, error) {
	return &core.Response{Blocks: []core.Block{core.TextBlock{Text: "hello"}}, FinishReason: core.FinishReasonStop}, nil
}
func (a *freshTurnStreamAdapter) Stream(ctx context.Context, req *core.Request) (core.Stream, error) {
	if a.first == nil {
		a.first = req
	}
	return &stubStream{events: coreResponseEvents(&core.Response{
		Blocks:       []core.Block{core.TextBlock{Text: "hello"}},
		FinishReason: core.FinishReasonStop,
	})}, nil
}

// TestFreshTurnHydrationSitsBetweenUserAndAssistant pins the never-compacted
// room: user → hydration checkpoint → first working assistant. Hydration is
// appended in addTurnMessages before the placeholder so Save stays
// append-only; runTurn must not move it past the assistant.
func TestFreshTurnHydrationSitsBetweenUserAndAssistant(t *testing.T) {
	conv := &domain.Conversation{ID: "c_fresh"}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c_fresh": conv}}
	adapter := &freshTurnStreamAdapter{}
	settings := domain.DefaultSettings()
	settings.CompactionEnabled = false
	app := &App{
		Conversations: store,
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		Toolbox:       &recordingToolbox{},
		Settings:      &fakeSettingsStore{settings: settings},
		Factory: func(context.Context, *domain.Provider, string) (AIProvider, error) {
			return adapter, nil
		},
		runs: map[string]*TurnRun{},
	}
	app.addTurnMessages(conv,
		domain.Message{ID: "u1", Role: domain.RoleUser, Content: "halo", Status: domain.StatusDone},
		domain.Message{ID: "a1", Role: domain.RoleAssistant},
	)
	if err := bindConversation(store, conv).Save(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c_fresh", Ctx: ctx, Cancel: cancel}
	app.runTurn(run, &domain.Provider{ID: "p", Kind: domain.ProviderChat}, "key", "model", "", "a1", false, ModelCapabilities{})

	saved, err := app.Conversations.Get("c_fresh")
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Messages) < 3 {
		t.Fatalf("len(Messages) = %d, want user + hydration + assistant", len(saved.Messages))
	}
	if saved.Messages[0].ID != "u1" || !domain.IsHydrationMessage(saved.Messages[1]) || saved.Messages[2].ID != "a1" {
		t.Fatalf("fresh transcript order = %s %v %s, want user, hydration, a1",
			saved.Messages[0].ID, domain.IsHydrationMessage(saved.Messages[1]), saved.Messages[2].ID)
	}
	if saved.Messages[2].Content != "hello" {
		t.Fatalf("assistant content = %q", saved.Messages[2].Content)
	}
	if adapter.first == nil || !coreHasHydration(adapter.first.Messages) {
		t.Fatal("first stream must include the hydration checkpoint")
	}
	msgs := adapter.first.Messages
	start := 0
	if len(msgs) > 0 && msgs[0].Role == core.RoleSystem {
		start = 1
	}
	if len(msgs) < start+2 || msgs[start].Role != core.RoleUser || msgs[start+1].Role != core.RoleAssistant {
		t.Fatalf("first stream prefix after system = %+v, want user then hydration assistant", msgs[start:])
	}
	hyd := false
	for _, b := range msgs[start+1].Blocks {
		if tc, ok := b.(core.ToolUseBlock); ok && domain.IsHydrationCallID(tc.ID) {
			hyd = true
			break
		}
	}
	if !hyd {
		t.Fatal("message after the user must be the hydration assistant")
	}
}

// TestExecuteTurnToolsKeepsHydrationOnBriefChange pins the cache-poison fix:
// a todo tool call that changes the brief must NOT strip the persisted
// hydration checkpoint. The checkpoint's todo_list brief is frozen until the
// next compaction epoch; the agent can call todo/todo_list live. Stripping +
// rebuilding relocated the checkpoint after whatever user was last (the
// poison dump's "." follow-up), breaking the prompt-cache prefix.
func TestExecuteTurnToolsKeepsHydrationOnBriefChange(t *testing.T) {
	todos := &fakeTodoPort{briefs: map[string]string{"c1": "old brief"}}
	box := &briefMutatingToolbox{todos: todos, newBrief: "## Objective\nnew\n## Done when\nnew"}
	conv := &domain.Conversation{
		ID: "c1",
		Messages: []domain.Message{
			hydrationCheckpointMessage(),
			{ID: "m1", Role: domain.RoleAssistant, ToolCalls: []domain.ToolCall{{ID: "t1", Name: "todo", Args: `{}`}}},
		},
	}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}},
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		Toolbox:       box,
		Todos:         todos,
	}
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: WithConversationID(context.Background(), "c1"), Cancel: func() {}}

	if err := app.executeTurnTools(run, "m1", conv.Messages[1].ToolCalls, ModelCapabilities{Vision: true}, domain.Settings{}, 1); err != nil {
		t.Fatalf("executeTurnTools: %v", err)
	}
	// The hydration checkpoint must still be present.
	found := false
	for _, m := range conv.Messages {
		for _, tc := range m.ToolCalls {
			if domain.IsHydrationCallID(tc.ID) {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("hydration checkpoint was stripped on brief change; it must stay frozen until compaction")
	}
}

// TestPersistCompactedConversationInsertsHydrationAfterHandover pins the
// post-compaction epoch: persistCompactedConversation rebuilds the checkpoint
// in the SAME Save as Compact, immediately after the handover user — not after
// a later steer user that may live in the retained suffix. The old
// last-user-before-placeholder index parked the checkpoint after the steer,
// relocating a volatile prefix into the middle of the cached transcript.
func TestPersistCompactedConversationInsertsHydrationAfterHandover(t *testing.T) {
	// Retained suffix contains a steer user after prior assistant work. The
	// handover is the epoch anchor; hydration must follow it, not the steer.
	conv := &domain.Conversation{
		ID: "c_compact",
		Messages: []domain.Message{
			{ID: "u1", Role: domain.RoleUser, Content: "first question", Status: domain.StatusDone},
			{ID: "a1", Role: domain.RoleAssistant, Content: "doing work", Status: domain.StatusDone},
			{ID: "steer", Role: domain.RoleUser, Content: "now do X", Status: domain.StatusDone},
			{ID: "a2", Role: domain.RoleAssistant, Content: "ok", Status: domain.StatusDone},
			{ID: "pending", Role: domain.RoleAssistant},
		},
	}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c_compact": conv}},
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		Toolbox:       &recordingToolbox{},
	}

	if err := app.persistCompactedConversation(conv, "summary text", 1_000_000); err != nil {
		t.Fatalf("persistCompactedConversation: %v", err)
	}

	// Compact put the handover user at messages[0]; hydration must be at [1].
	if len(conv.Messages) < 3 {
		t.Fatalf("len(Messages) = %d, want handover + hydration + retained", len(conv.Messages))
	}
	if conv.Messages[0].Role != domain.RoleUser {
		t.Fatalf("Messages[0] role = %s, want user (handover)", conv.Messages[0].Role)
	}
	if !domain.IsHydrationMessage(conv.Messages[1]) {
		t.Fatalf("Messages[1] must be the hydration checkpoint, got %+v", conv.Messages[1])
	}
	// Exactly one checkpoint — no second one parked after the steer user.
	count := 0
	hydIdx := -1
	steerIdx := -1
	for i, m := range conv.Messages {
		if domain.IsHydrationMessage(m) {
			count++
			hydIdx = i
		}
		if m.ID == "steer" {
			steerIdx = i
		}
	}
	if count != 1 {
		t.Fatalf("want exactly 1 hydration checkpoint, got %d", count)
	}
	if steerIdx >= 0 && steerIdx < hydIdx {
		t.Fatalf("steer user at %d must not precede hydration at %d (would park checkpoint after the steer)", steerIdx, hydIdx)
	}
}

// TestFollowUpUserTurnDoesNotRelocateHydration pins the prompt-cache invariant
// on a follow-up turn: a transcript that already has a checkpoint after user1
// (user1 → hydration → work → user2 → placeholder) must keep it there. The
// turn loop no longer touches hydration, so the first Stream reads the frozen
// transcript and the cache prefix up to the checkpoint stays intact.
func TestFollowUpUserTurnDoesNotRelocateHydration(t *testing.T) {
	conv := &domain.Conversation{
		ID: "c_followup",
		Messages: []domain.Message{
			{ID: "u1", Role: domain.RoleUser, Content: "halo", Status: domain.StatusDone},
			hydrationCheckpointMessage(),
			{ID: "a1", Role: domain.RoleAssistant, Content: "work", Status: domain.StatusDone},
			{ID: "u2", Role: domain.RoleUser, Content: "follow up", Status: domain.StatusDone},
			{ID: "a2", Role: domain.RoleAssistant},
		},
	}
	adapter := &freshTurnStreamAdapter{}
	settings := domain.DefaultSettings()
	settings.CompactionEnabled = false
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c_followup": conv}},
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		Toolbox:       &recordingToolbox{},
		Settings:      &fakeSettingsStore{settings: settings},
		Factory: func(context.Context, *domain.Provider, string) (AIProvider, error) {
			return adapter, nil
		},
		runs: map[string]*TurnRun{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c_followup", Ctx: ctx, Cancel: cancel}
	app.runTurn(run, &domain.Provider{ID: "p", Kind: domain.ProviderChat}, "key", "model", "", "a2", false, ModelCapabilities{})

	// The checkpoint must still sit immediately after u1, not after u2.
	count := 0
	hydIdx := -1
	u2Idx := -1
	for i, m := range conv.Messages {
		if domain.IsHydrationMessage(m) {
			count++
			hydIdx = i
		}
		if m.ID == "u2" {
			u2Idx = i
		}
	}
	if count != 1 {
		t.Fatalf("want exactly 1 hydration checkpoint, got %d (follow-up must not add another)", count)
	}
	if u2Idx >= 0 && hydIdx > u2Idx {
		t.Fatalf("hydration at %d relocated after u2 at %d; it must stay after u1", hydIdx, u2Idx)
	}
}

// TestFreshTurnRepairsHydrationLeadingTheUser is the live shape from an
// older empty-room workspace pick: checkpoint at index 0, then the first
// user. Formed IDs stay put (append-only). chatMessages relocates the
// checkpoint so the first Stream still sends user → hydration.
func TestFreshTurnRepairsHydrationLeadingTheUser(t *testing.T) {
	conv := &domain.Conversation{
		ID: "c_lead_turn",
		Messages: []domain.Message{
			hydrationCheckpointMessage(),
			{ID: "u1", Role: domain.RoleUser, Content: "halo", Status: domain.StatusDone},
			{ID: "a1", Role: domain.RoleAssistant},
		},
	}
	adapter := &freshTurnStreamAdapter{}
	settings := domain.DefaultSettings()
	settings.CompactionEnabled = false
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c_lead_turn": conv}},
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		Toolbox:       &recordingToolbox{},
		Settings:      &fakeSettingsStore{settings: settings},
		Factory: func(context.Context, *domain.Provider, string) (AIProvider, error) {
			return adapter, nil
		},
		runs: map[string]*TurnRun{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c_lead_turn", Ctx: ctx, Cancel: cancel}
	app.runTurn(run, &domain.Provider{ID: "p", Kind: domain.ProviderChat}, "key", "model", "", "a1", false, ModelCapabilities{})

	saved, err := app.Conversations.Get("c_lead_turn")
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Messages) < 3 {
		t.Fatalf("len(Messages) = %d, want hydration + user + assistant", len(saved.Messages))
	}
	if !domain.IsHydrationMessage(saved.Messages[0]) || saved.Messages[1].ID != "u1" || saved.Messages[2].ID != "a1" {
		t.Fatalf("persisted order = hyd=%v %s %s, want leading hydration, u1, a1",
			domain.IsHydrationMessage(saved.Messages[0]), saved.Messages[1].ID, saved.Messages[2].ID)
	}
	if saved.Messages[2].Content != "hello" {
		t.Fatalf("assistant content = %q", saved.Messages[2].Content)
	}
	if adapter.first == nil {
		t.Fatal("first stream was not captured")
	}
	msgs := adapter.first.Messages
	start := 0
	if len(msgs) > 0 && msgs[0].Role == core.RoleSystem {
		start = 1
	}
	if len(msgs) < start+2 || msgs[start].Role != core.RoleUser || msgs[start+1].Role != core.RoleAssistant {
		t.Fatalf("first stream prefix after system = %+v, want user then hydration assistant", msgs[start:])
	}
	hyd := false
	for _, b := range msgs[start+1].Blocks {
		if tc, ok := b.(core.ToolUseBlock); ok && domain.IsHydrationCallID(tc.ID) {
			hyd = true
			break
		}
	}
	if !hyd {
		t.Fatal("message after the user must be the hydration assistant")
	}
}

// --- from hydration_skillcreator_test.go ---

func TestHydrationSkillCreatorSlot(t *testing.T) {
	b := NewHydrationBuilder(HydrationSource{
		SkillCreatorPath:    "/data/skills/skill-creator/SKILL.md",
		SkillCreatorContent: "# Create an agent skill\n\nAuthoring reference body.",
	})
	res := b.Build()
	found := false
	for _, m := range res.Messages {
		for _, tc := range m.ToolCalls {
			if tc.Name == "file_read" && strings.Contains(tc.Args, "skill-creator/SKILL.md") {
				found = true
			}
		}
		if m.ToolResult != nil && strings.Contains(m.ToolResult.Content, "Authoring reference body.") {
			found = found && true
		}
	}
	if !found {
		t.Fatalf("hydration must attach the skill-creator file_read slot: %+v", res.Messages)
	}
}

func TestHydrationSkillCreatorSlotHiddenWhenEmpty(t *testing.T) {
	b := NewHydrationBuilder(HydrationSource{
		SkillCreatorPath:    "/data/skills/skill-creator/SKILL.md",
		SkillCreatorContent: "   ",
	})
	res := b.Build()
	for _, m := range res.Messages {
		for _, tc := range m.ToolCalls {
			if strings.Contains(tc.Args, "skill-creator") {
				t.Fatalf("empty skill-creator content must hide the slot: %+v", tc)
			}
		}
	}
}

func TestBuildHydrationInjectsSkillCreatorForBackgroundLearner(t *testing.T) {
	skills := &fakeSkillStore{items: map[string]*domain.Skill{
		"skill-creator": {
			ID: "skill-creator", Name: "skill-creator", Origin: domain.SkillOriginBuiltin,
			Content: "# Create an agent skill\n\nHydration copy.",
		},
	}}
	app := &App{DataDir: "/home/u/.config/nusashell", Skills: skills}

	bg := &domain.Conversation{ID: "c_bg", Type: domain.ConversationTypeBackground}
	msgs := app.buildHydration(bg)
	blob := messagesBlob(msgs)
	if !strings.Contains(blob, "skill-creator/SKILL.md") || !strings.Contains(blob, "Hydration copy.") {
		t.Fatalf("background learner hydration must carry the skill-creator file_read slot:\n%s", blob)
	}

	// Interactive rooms and pipeline turns must not carry the slot.
	chat := &domain.Conversation{ID: "c_chat"}
	if blob := messagesBlob(app.buildHydration(chat)); strings.Contains(blob, "skill-creator") {
		t.Fatalf("interactive conversation must not get the learner slot:\n%s", blob)
	}
	pipe := &domain.Conversation{ID: "c_pipe", Type: domain.ConversationTypeAutomation}
	if blob := messagesBlob(app.buildHydration(pipe)); strings.Contains(blob, "skill-creator") {
		t.Fatalf("automation pipeline must not get the learner slot:\n%s", blob)
	}
}

func messagesBlob(msgs []ChatMessage) string {
	var sb strings.Builder
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			sb.WriteString(tc.Name)
			sb.WriteString(" ")
			sb.WriteString(tc.Args)
			sb.WriteString(" ")
		}
		if m.ToolResult != nil {
			sb.WriteString(m.ToolResult.Content)
			sb.WriteString(" ")
		}
	}
	return sb.String()
}

// --- from hydration_file_list_test.go ---

var errFakeListing = errors.New("fake listing failure")

// TestHydrationFileListRealTool pins the workspace-map slot: when a
// workspace is set, the hydration checkpoint includes a REAL file_list call
// against the workspace root, attached verbatim, positioned right after the
// AGENTS.md file_read so the project map leads before memory and catalogs.
func TestHydrationFileListRealTool(t *testing.T) {
	agentsOut := "---\nbytes: 42\n---\n\n# Project rules\nUse Go, keep it simple."
	listingOut := "---\nbytes: 120\ncount: 3\ntotal: 1.2 KB\n---\n" +
		"drwxr-xr-x application\n" +
		"-rw-r--r-- go.mod\n" +
		"-rw-r--r-- README.md\n"
	exec := &stubHydrationExecutor{fn: func(name string, _ []byte) (string, error) {
		switch name {
		case "file_read":
			return agentsOut, nil
		case "file_list":
			return listingOut, nil
		default:
			return "", nil
		}
	}}
	b := NewHydrationBuilder(HydrationSource{
		Executor:       exec,
		RuntimeContext: RuntimeContextSnapshot{Workspace: "/ws/proj"},
	})
	result := b.Build()

	// file_list is the third call: runtime_context, AGENTS.md file_read,
	// then the workspace listing.
	if len(result.Messages[0].ToolCalls) < 3 {
		t.Fatalf("expected at least 3 hydration calls, got %d", len(result.Messages[0].ToolCalls))
	}
	call := result.Messages[0].ToolCalls[2]
	if call.Name != "file_list" {
		t.Fatalf("third hydration call = %s, want file_list", call.Name)
	}
	if call.Args != `{"path":"/ws/proj"}` {
		t.Fatalf("file_list args = %s, want workspace root", call.Args)
	}
	slot := findHydrationTool(result.Messages, "file_list")
	if slot == nil {
		t.Fatal("file_list hydration slot missing")
	}
	// Verbatim real tool output.
	if slot.Content != listingOut {
		t.Fatalf("file_list content not verbatim:\n got %q\nwant %q", slot.Content, listingOut)
	}
}

// TestHydrationFileListHiddenWithoutWorkspace pins the fail-soft rules: no
// executor, no workspace, a failing listing, or an empty directory all hide
// the slot (dynamic hydration — no stubs).
func TestHydrationFileListHiddenWithoutWorkspace(t *testing.T) {
	// No executor → hidden even with a workspace.
	result := NewHydrationBuilder(HydrationSource{
		RuntimeContext: RuntimeContextSnapshot{Workspace: "/ws/proj"},
	}).Build()
	for _, c := range result.Messages[0].ToolCalls {
		if c.Name == "file_list" {
			t.Fatal("file_list must be hidden without an executor")
		}
	}

	// No workspace → hidden even with an executor.
	exec := &stubHydrationExecutor{fn: func(name string, _ []byte) (string, error) {
		if name == "file_list" {
			return "---\nbytes: 3\ncount: 0\n---\n<listing>", nil
		}
		return "", nil
	}}
	result = NewHydrationBuilder(HydrationSource{
		Executor: exec,
	}).Build()
	for _, c := range result.Messages[0].ToolCalls {
		if c.Name == "file_list" {
			t.Fatal("file_list must be hidden without a workspace")
		}
	}

	// Failing listing → hidden.
	failing := &stubHydrationExecutor{fn: func(name string, _ []byte) (string, error) {
		if name == "file_list" {
			return "", errFakeListing
		}
		return "", nil
	}}
	result = NewHydrationBuilder(HydrationSource{
		Executor:       failing,
		RuntimeContext: RuntimeContextSnapshot{Workspace: "/ws/proj"},
	}).Build()
	assertNoFileListSlot(t, result, "failing listing")

	// Empty directory (meta block, no body) → hidden.
	empty := &stubHydrationExecutor{fn: func(name string, _ []byte) (string, error) {
		if name == "file_list" {
			return "---\nbytes: 21\ncount: 0\ntotal: 0 B\n---\n", nil
		}
		return "", nil
	}}
	result = NewHydrationBuilder(HydrationSource{
		Executor:       empty,
		RuntimeContext: RuntimeContextSnapshot{Workspace: "/ws/proj"},
	}).Build()
	assertNoFileListSlot(t, result, "empty listing")

	// Oversized listing (e.g. a home-directory workspace root) → hidden,
	// so the prompt never carries a multi-thousand-entry listing.
	bigListing := "---\nbytes: 10000\ncount: 900\ntotal: 1.2 GB\n---\n" +
		strings.Repeat("-rw-r--r-- some-entry.txt\n", 700)
	if len(bigListing) <= hydrationFileListMaxBytes {
		t.Fatalf("test fixture too small: %d bytes", len(bigListing))
	}
	oversized := &stubHydrationExecutor{fn: func(name string, _ []byte) (string, error) {
		if name == "file_list" {
			return bigListing, nil
		}
		return "", nil
	}}
	result = NewHydrationBuilder(HydrationSource{
		Executor:       oversized,
		RuntimeContext: RuntimeContextSnapshot{Workspace: "/ws/proj"},
	}).Build()
	assertNoFileListSlot(t, result, "oversized listing")
}

func assertNoFileListSlot(t *testing.T, result HydrationResult, why string) {
	t.Helper()
	for _, c := range result.Messages[0].ToolCalls {
		if c.Name == "file_list" {
			t.Fatalf("file_list must be hidden for %s", why)
		}
	}
	if slot := findHydrationTool(result.Messages, "file_list"); slot != nil {
		t.Fatalf("file_list result must be hidden for %s", why)
	}
}
