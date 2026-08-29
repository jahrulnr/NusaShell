package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"nusashell/domain"
)

// RuntimeContextSnapshot is the read-only runtime context payload for the
// synthetic `runtime_context` hydration call.
type RuntimeContextSnapshot struct {
	CurrentDate string `json:"currentDate"`
	Environment string `json:"environment"`
	RuntimeOS   string `json:"runtimeOs"`
	Workspace   string `json:"workspace,omitempty"`
	DataDir     string `json:"dataDir,omitempty"`
}

// HydrationSource assembles the read-only sources of truth the builder draws
// from. Slots whose content is empty are omitted from the transcript
// (dynamic hydration).
type HydrationSource struct {
	RuntimeContext RuntimeContextSnapshot
	// Executor runs the REAL meta-tools — mcp_list, tool_list (per running
	// server), skill op=list, file_read (primary.md) — so the checkpoint
	// contains genuine tool output, the same tools the agent itself calls.
	// The builder never mutates, clones, or mirrors a tool: it calls the
	// real tool, processes the result, and attaches it to the conversation.
	// When nil, the tool-backed slots fail soft (hidden).
	Executor ToolExecutor
	// PrimaryPath is the absolute filesystem path of memory/primary.md.
	// When set, the memory slot reads the file via file_read. When empty,
	// the memory slot is hidden.
	PrimaryPath string
	// Todos is the per-conversation todo checklist. When nil, no todo_list
	// slot is injected.
	Todos  ConversationTodoPort
	ConvID string
	// Journal supplies workspace change state for the workspace_state slot.
	// When nil, that slot is hidden.
	Journal ChangeJournal
	// ProjectMemory is the per-workspace project-memory store. When nil,
	// the memory_project hydration slot is hidden.
	ProjectMemory ProjectMemoryStore
}

// HydrationBuilder produces an ephemeral synthetic tool transcript
// (assistant toolCalls + matching tool results) representing a snapshot of
// the shell runtime. Every tool-backed slot executes its REAL tool through
// the Executor — the same implementation the agent calls — and attaches the
// genuine output (processed only where the checkpoint adds value, e.g.
// primary-memory usage stats). The transcript is DYNAMIC: slots with empty
// content are omitted entirely, so the call count varies per epoch.
//
// The transcript is placed AFTER a real user message (or compaction summary)
// and BEFORE the model's own output, so the model sees fresh runtime facts
// (date, workspace, memory, skills, MCP catalog, tool catalog) without those
// volatile values being baked into the stable system prompt prefix (which
// would break prompt-cache hits).
type HydrationBuilder struct {
	source HydrationSource
	// runningServerIDs caches the ids of running MCP servers parsed from the
	// real mcp_list output, consumed by readToolList for the per-server loop.
	runningServerIDs []string
}

// NewHydrationBuilder creates a builder from the given read-only sources.
func NewHydrationBuilder(source HydrationSource) *HydrationBuilder {
	return &HydrationBuilder{source: source}
}

// HydrationResult is the output of Build — the synthetic messages plus the
// nonce used for call IDs (so callers can identify and filter them later).
type HydrationResult struct {
	Messages  []ChatMessage
	Nonce     string
	CallCount int
}

// Build assembles the synthetic hydration transcript. Each slot is fail-soft:
// a read error yields a structured error JSON for that one tool result while
// the other results still contribute.
func (b *HydrationBuilder) Build() HydrationResult {
	nonce := randomNonce()
	var slots []hydrationSlot
	for _, slot := range []hydrationSlot{
		b.readRuntimeContext(),
		b.readAgentsMD(),
		b.readMemory(),
		b.readProjectMemory(),
		b.readSkills(),
		b.readMcpList(),
	} {
		if slot.content != "" {
			slots = append(slots, slot)
		}
	}
	slots = append(slots, b.readToolList()...)
	if slot := b.readTodoList(); slot.content != "" {
		slots = append(slots, slot)
	}
	if slot := b.readWorkspaceState(); slot.content != "" {
		slots = append(slots, slot)
	}
	calls := make([]domain.ToolCall, 0, len(slots))
	for i, slot := range slots {
		args := slot.args
		if args == "" {
			args = "{}"
		}
		calls = append(calls, domain.ToolCall{
			ID:   fmt.Sprintf("%s%s_%d", domain.HydrateToolCallPrefix, nonce, i),
			Name: slot.name,
			Args: args,
		})
	}
	messages := make([]ChatMessage, 0, len(slots)+1)
	messages = append(messages, ChatMessage{
		Role:      "assistant",
		ToolCalls: calls,
	})
	for i, slot := range slots {
		messages = append(messages, ChatMessage{
			Role: "tool",
			ToolResult: &ToolResult{
				ToolCallID: calls[i].ID,
				Name:       slot.name,
				Content:    slot.content,
			},
		})
	}
	return HydrationResult{Messages: messages, Nonce: nonce, CallCount: len(calls)}
}

type hydrationSlot struct {
	name    string
	args    string // tool-call arguments rendered in the synthetic transcript ("{}" when empty)
	content string
}

func (b *HydrationBuilder) readRuntimeContext() hydrationSlot {
	ctx := b.source.RuntimeContext
	if ctx.CurrentDate == "" {
		ctx.CurrentDate = time.Now().UTC().Format(time.RFC3339)
	}
	if ctx.Environment == "" {
		ctx.Environment = "nusashell"
	}
	if ctx.RuntimeOS == "" {
		ctx.RuntimeOS = runtime.GOOS + "/" + runtime.GOARCH
	}
	content, _ := json.Marshal(ctx)
	return hydrationSlot{name: "runtime_context", content: string(content)}
}

// readAgentsMD loads the active workspace's AGENTS.md through the REAL
// file_read tool and attaches the genuine output verbatim, so the agent
// receives the project's agent instructions without having to read the file
// itself (agents frequently skip AGENTS.md when left to their own devices —
// this forces it via hydration). The slot is a real file_read call: same
// tool, same args, same output the agent would get from a direct call.
// Fail-soft: no workspace, a missing file, or an empty body hides the slot.
func (b *HydrationBuilder) readAgentsMD() hydrationSlot {
	if b.source.Executor == nil {
		return hydrationSlot{name: "file_read", content: ""}
	}
	ws := strings.TrimSpace(b.source.RuntimeContext.Workspace)
	if ws == "" {
		return hydrationSlot{name: "file_read", content: ""}
	}
	path := filepath.Join(ws, "AGENTS.md")
	args := fmt.Sprintf(`{"path":%q}`, path)
	out, err := b.source.Executor.Execute(context.Background(), "file_read", []byte(args))
	if err != nil {
		return hydrationSlot{name: "file_read", content: ""}
	}
	// file_read returns yamlMD (bytes meta + body). Hide the slot when the
	// file body is empty so we don't inject a bare meta block.
	if strings.TrimSpace(stripYAMLFrontmatter(out)) == "" {
		return hydrationSlot{name: "file_read", content: ""}
	}
	return hydrationSlot{name: "file_read", args: args, content: out}
}

// readMemory reads the primary.md file via file_read and attaches its body
// as a single entry, enriched with usage stats. An empty or missing primary
// document hides the slot.
func (b *HydrationBuilder) readMemory() hydrationSlot {
	if b.source.Executor == nil || b.source.PrimaryPath == "" {
		return hydrationSlot{name: "memory", content: ""}
	}
	args := fmt.Sprintf(`{"path":%q}`, b.source.PrimaryPath)
	out, err := b.source.Executor.Execute(context.Background(), "file_read", []byte(args))
	if err != nil {
		return hydrationSlot{name: "memory", content: ""}
	}
	// file_read returns yamlMD: a YAML frontmatter block (bytes meta)
	// followed by the file body. The primary.md file itself also starts with
	// a YAML frontmatter block (last_updated/version). Strip both so the
	// hydration slot carries only the prose body, matching the previous
	// memory-list output.
	body := stripYAMLFrontmatter(stripYAMLFrontmatter(out))
	body = strings.TrimSpace(body)
	if body == "" {
		return hydrationSlot{name: "memory", content: ""}
	}
	chars := len(body)
	limit := domain.PrimaryCharCap
	pct := 0
	if limit > 0 {
		pct = chars * 100 / limit
	}
	content, _ := json.Marshal(map[string]any{
		"entries": []map[string]any{{"content": body}},
		"count":   1,
		"usage":   map[string]any{"chars": chars, "limit": limit, "pct": pct},
	})
	return hydrationSlot{name: "memory", args: args, content: string(content)}
}

// readProjectMemory injects a compact IDX-project extract (PURPOSE, LOCKS,
// CURRENT_STATE, ROUTES). Hidden when there is no workspace, no store, or
// no index.md snapshot.
func (b *HydrationBuilder) readProjectMemory() hydrationSlot {
	if b.source.ProjectMemory == nil {
		return hydrationSlot{name: "memory_project", content: ""}
	}
	ws := strings.TrimSpace(b.source.RuntimeContext.Workspace)
	if ws == "" {
		return hydrationSlot{name: "memory_project", content: ""}
	}
	extract, ok, err := b.source.ProjectMemory.IndexExtract(ws)
	if err != nil || !ok {
		return hydrationSlot{name: "memory_project", content: ""}
	}
	if extract.Purpose == "" && extract.Locks == "" && extract.CurrentState == "" && extract.Routes == "" {
		return hydrationSlot{name: "memory_project", content: ""}
	}
	content, _ := json.Marshal(extract)
	args := `{"op":"query","kind":"index","full":true}`
	return hydrationSlot{name: "memory_project", args: args, content: string(content)}
}

// stripYAMLFrontmatter removes a leading "---\n...\n---" block from text.
func stripYAMLFrontmatter(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "---") {
		return s
	}
	rest := strings.TrimPrefix(s, "---")
	// Find the closing ---
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return s
	}
	return strings.TrimSpace(rest[idx+4:])
}

// readSkills runs the real `skill` tool (op=list) and attaches its output
// verbatim. An empty skill library hides the slot.
func (b *HydrationBuilder) readSkills() hydrationSlot {
	if b.source.Executor == nil {
		return hydrationSlot{name: "skill", content: ""}
	}
	out, err := b.source.Executor.Execute(context.Background(), "skill", []byte(`{"op":"list"}`))
	if err != nil || !hasJSONLLines(out) {
		return hydrationSlot{name: "skill", content: ""}
	}
	return hydrationSlot{name: "skill", args: `{"op":"list"}`, content: out}
}

// hasJSONLLines reports whether a yamlJSONL tool output contains at least
// one JSON record, i.e. the tool had something to report.
func hasJSONLLines(out string) bool {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "{") {
			return true
		}
	}
	return false
}

// readMcpList executes the real `mcp_list` tool so the checkpoint contains
// the genuine output the agent would see from a direct call. The running
// server ids parsed from that output drive the per-server tool_list loop.
// A runtime with no plugins hides the slot.
func (b *HydrationBuilder) readMcpList() hydrationSlot {
	if b.source.Executor == nil {
		return hydrationSlot{name: "mcp_list", content: ""}
	}
	out, err := b.source.Executor.Execute(context.Background(), "mcp_list", []byte("{}"))
	if err != nil || !hasJSONLLines(out) {
		return hydrationSlot{name: "mcp_list", content: ""}
	}
	b.runningServerIDs = parseRunningServerIDs(out)
	return hydrationSlot{name: "mcp_list", args: "{}", content: out}
}

// parseRunningServerIDs extracts the ids of running plugins from the real
// mcp_list tool output (YAML frontmatter followed by one JSON object per
// line), so the tool_list loop follows exactly what the mcp_list result
// shows the model.
func parseRunningServerIDs(out string) []string {
	var ids []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var srv struct {
			ID      string `json:"id"`
			Running bool   `json:"running"`
		}
		if json.Unmarshal([]byte(line), &srv) != nil || !srv.Running || srv.ID == "" {
			continue
		}
		ids = append(ids, srv.ID)
	}
	return ids
}

// readToolList executes the real `tool_list` tool once per running MCP
// server — ids taken from the real mcp_list result — producing one synthetic
// `tool_list` call+result per server, the same discovery workflow the agent
// itself would run. The built-in tool catalog is intentionally NOT injected:
// the provider request already carries it with schemas in `tools[]` every
// round, and a synthetic built-in `tool_list` slot gave the name a different
// meaning than the real tool (built-in catalog vs MCP tools), which confused
// agents.
func (b *HydrationBuilder) readToolList() []hydrationSlot {
	var slots []hydrationSlot
	if b.source.Executor == nil {
		return slots
	}
	for _, id := range b.runningServerIDs {
		args, _ := json.Marshal(map[string]string{"server": id})
		out, err := b.source.Executor.Execute(context.Background(), "tool_list", args)
		if err != nil || !hasJSONLLines(out) {
			continue
		}
		slots = append(slots, hydrationSlot{name: "tool_list", args: string(args), content: out})
	}
	return slots
}

// readTodoList injects the current conversation's todo checklist into a
// synthetic `todo_list` checkpoint. Only incomplete items are included
// (completed items are noise for the model — the UI strip still shows them).
// The checkpoint is reused while history remains intact; after compaction, a
// fresh checkpoint restores the goal and open items from the todo store.
// There is no real `todo_list` tool (the `todo` tool is a full-replace
// writer), so the slot reads the todo store directly. Empty content (no
// goal and no open items) hides the slot.
func (b *HydrationBuilder) readTodoList() hydrationSlot {
	if b.source.Todos == nil || b.source.ConvID == "" {
		return hydrationSlot{name: "todo_list", content: ""}
	}
	var sections []string
	// Brief: the living planning document, set via the `todo` tool's
	// `brief` argument and restored in a fresh checkpoint after compaction.
	brief := b.source.Todos.GetBrief(b.source.ConvID)
	if brief != "" {
		sections = append(sections, "USER BRIEF (survives compaction — do not drift from this)\n"+brief)
	}
	// Items: only incomplete ones (completed items are noise for the model).
	// IDs are included so the agent can patch item status by ID after
	// compaction without re-emitting the full list.
	items := b.source.Todos.Get(b.source.ConvID)
	var lines []string
	for _, item := range items {
		if item.Status == domain.TodoCompleted {
			continue
		}
		glyph := "[ ]"
		if item.Status == domain.TodoInProgress {
			glyph = "[~]"
		}
		lines = append(lines, glyph+" ("+item.ID+") "+item.Content)
	}
	if len(lines) > 0 {
		sections = append(sections, "CURRENT TASKS (agent-owned checklist — user may delete items)\n"+strings.Join(lines, "\n"))
	}
	if len(sections) == 0 {
		return hydrationSlot{name: "todo_list", content: ""}
	}
	return hydrationSlot{name: "todo_list", content: strings.Join(sections, "\n\n")}
}

// readWorkspaceState injects accumulated workspace file changes for the
// conversation so post-compaction context retains what the agent modified.
func (b *HydrationBuilder) readWorkspaceState() hydrationSlot {
	if b.source.Journal == nil {
		return hydrationSlot{name: "workspace_state", content: ""}
	}
	workspace := strings.TrimSpace(b.source.RuntimeContext.Workspace)
	if workspace == "" || b.source.ConvID == "" {
		return hydrationSlot{name: "workspace_state", content: ""}
	}
	state, err := b.source.Journal.SessionState(context.Background(), b.source.ConvID, workspace)
	if err != nil || state == nil || len(state.Changes) == 0 {
		return hydrationSlot{name: "workspace_state", content: ""}
	}
	content, err := json.Marshal(state)
	if err != nil {
		return hydrationSlot{name: "workspace_state", content: ""}
	}
	args, err := json.Marshal(map[string]string{
		"conversation_id": b.source.ConvID,
		"workspace":       workspace,
	})
	if err != nil {
		return hydrationSlot{name: "workspace_state", content: ""}
	}
	return hydrationSlot{name: "workspace_state", args: string(args), content: string(content)}
}

// FilterHydration drops the synthetic runtime-hydration exchange (assistant
// toolCalls + tool results with "hydrate-" ids) from a message list before
// summarization so the summary only reflects durable conversation content.
// It also strips hydration tool calls from assistant messages that have both
// real and hydration calls.
func FilterHydration(messages []ChatMessage) []ChatMessage {
	out := make([]ChatMessage, 0, len(messages))
	for _, m := range messages {
		if m.Role == "tool" && m.ToolResult != nil && domain.IsHydrationCallID(m.ToolResult.ToolCallID) {
			continue
		}
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			realCalls := make([]domain.ToolCall, 0, len(m.ToolCalls))
			for _, c := range m.ToolCalls {
				if !domain.IsHydrationCallID(c.ID) {
					realCalls = append(realCalls, c)
				}
			}
			if len(realCalls) == len(m.ToolCalls) {
				out = append(out, m)
				continue
			}
			// All calls were hydration and no content/reasoning — drop entirely.
			if len(realCalls) == 0 && visibleText(m.Content) == "" && visibleText(m.Reasoning) == "" {
				continue
			}
			m.ToolCalls = realCalls
			out = append(out, m)
			continue
		}
		out = append(out, m)
	}
	return out
}

// HasHydration returns true when the current conversation history contains a
// hydration exchange (assistant with hydration toolCalls + matching tool
// results). The checkpoint is reused until compaction removes it.
func HasHydration(messages []ChatMessage) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m.Role != "assistant" || len(m.ToolCalls) == 0 {
			continue
		}
		allHydration := true
		expected := map[string]bool{}
		for _, c := range m.ToolCalls {
			if !domain.IsHydrationCallID(c.ID) {
				allHydration = false
				break
			}
			expected[c.ID] = false
		}
		if !allHydration {
			continue
		}
		// Check for matching tool results.
		for j := i + 1; j < len(messages); j++ {
			t := messages[j]
			if t.Role != "tool" || t.ToolResult == nil {
				break
			}
			if _, ok := expected[t.ToolResult.ToolCallID]; ok {
				expected[t.ToolResult.ToolCallID] = true
			}
		}
		if len(expected) > 0 {
			for _, v := range expected {
				if v {
					return true
				}
			}
		}
	}
	return false
}

// randomNonce generates a random 8-byte hex nonce for hydration call IDs.
func randomNonce() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fallback: timestamp-based nonce (non-crypto, but unique enough).
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// DefaultRuntimeContext builds a RuntimeContextSnapshot from the current
// process environment and the given workspace path.
func DefaultRuntimeContext(workspace string) RuntimeContextSnapshot {
	env := os.Getenv("NUSASHELL_ENV")
	if env == "" {
		env = "nusashell"
	}
	return RuntimeContextSnapshot{
		CurrentDate: time.Now().UTC().Format(time.RFC3339),
		Environment: env,
		RuntimeOS:   runtime.GOOS + "/" + runtime.GOARCH,
		Workspace:   strings.TrimSpace(workspace),
	}
}
