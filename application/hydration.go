package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
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
}

// HydrationSource assembles the read-only sources of truth the builder draws
// from. Slots whose content is empty are omitted from the transcript
// (dynamic hydration).
type HydrationSource struct {
	RuntimeContext RuntimeContextSnapshot
	// Executor runs the REAL meta-tools — mcp_list, tool_list (per running
	// server), skill_list, memory_list target=primary — so the checkpoint
	// contains genuine tool output, the same tools the agent itself calls.
	// The builder never mutates, clones, or mirrors a tool: it calls the
	// real tool, processes the result, and attaches it to the conversation.
	// When nil, the tool-backed slots fail soft (hidden).
	Executor ToolExecutor
	// Todos is the per-conversation todo checklist. When nil, no todo_list
	// slot is injected.
	Todos  ConversationTodoPort
	ConvID string
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
	slots := b.collectSlots()
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

func (b *HydrationBuilder) collectSlots() []hydrationSlot {
	var slots []hydrationSlot
	for _, slot := range []hydrationSlot{
		b.readRuntimeContext(),
		b.readMemory(),
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
	return slots
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

// readMemory runs the real `memory_list` tool with target=primary and
// attaches its entries, enriched with usage stats computed from the output
// (the checkpoint's value-add; the tool itself returns bare entries). An
// empty primary document hides the slot.
func (b *HydrationBuilder) readMemory() hydrationSlot {
	if b.source.Executor == nil {
		return hydrationSlot{name: "memory", content: ""}
	}
	out, err := b.source.Executor.Execute(context.Background(), "memory_list", []byte(`{"target":"primary"}`))
	if err != nil {
		return hydrationSlot{name: "memory", content: ""}
	}
	type primaryEntry struct {
		ID      string `json:"id"`
		Content string `json:"content"`
	}
	type usage struct {
		Chars int `json:"chars"`
		Limit int `json:"limit"`
		Pct   int `json:"pct"`
	}
	var entries []primaryEntry
	chars := 0
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var e primaryEntry
		if json.Unmarshal([]byte(line), &e) != nil || e.Content == "" {
			continue
		}
		entries = append(entries, e)
		chars += len(e.Content)
	}
	if len(entries) == 0 {
		return hydrationSlot{name: "memory", content: ""}
	}
	limit := domain.PrimaryCharCap
	pct := 0
	if limit > 0 {
		pct = chars * 100 / limit
	}
	content, _ := json.Marshal(map[string]any{
		"entries": entries,
		"count":   len(entries),
		"usage":   usage{Chars: chars, Limit: limit, Pct: pct},
	})
	return hydrationSlot{name: "memory", args: `{"target":"primary"}`, content: string(content)}
}

// readSkills runs the real `skill_list` tool and attaches its output
// verbatim. An empty skill library hides the slot.
func (b *HydrationBuilder) readSkills() hydrationSlot {
	if b.source.Executor == nil {
		return hydrationSlot{name: "skill_list", content: ""}
	}
	out, err := b.source.Executor.Execute(context.Background(), "skill_list", []byte("{}"))
	if err != nil || !hasJSONLLines(out) {
		return hydrationSlot{name: "skill_list", content: ""}
	}
	return hydrationSlot{name: "skill_list", args: "{}", content: out}
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
		lines = append(lines, glyph+" "+item.Content)
	}
	if len(lines) > 0 {
		sections = append(sections, "CURRENT TASKS (agent-owned checklist — user may delete items)\n"+strings.Join(lines, "\n"))
	}
	if len(sections) == 0 {
		return hydrationSlot{name: "todo_list", content: ""}
	}
	return hydrationSlot{name: "todo_list", content: strings.Join(sections, "\n\n")}
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
			if len(realCalls) == 0 && m.Content == "" && m.Reasoning == "" {
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
