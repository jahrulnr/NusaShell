package application

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	"nusashell/contracts"
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

// MCPToolReader is the minimal read-only interface the hydration builder needs
// to enumerate tools from running MCP servers without executing the gateway.
type MCPToolReader interface {
	ToolsFor(serverID string) ([]contracts.MCPToolDTO, bool)
}

// HydrationSource assembles the read-only sources of truth the builder draws
// from. A nil store means that snapshot slot is skipped (fail-soft).
type HydrationSource struct {
	Primary        PrimaryStore // always-injected working set (primary.md)
	Memory         MemoryStore  // legacy — used by lifecycle/learning subsystems
	Skills         SkillStore
	Plugins        PluginStore
	MCP            MCPToolReader
	RuntimeContext RuntimeContextSnapshot
	// Tools is the built-in tool catalog from Toolbox.ListTools(). Hydration keeps
	// only names and descriptions because the provider already receives schemas.
	Tools []ToolInfo
	// Todos is the per-conversation todo checklist. When nil, no todo_list
	// slot is injected.
	Todos  ConversationTodoPort
	ConvID string
}

// HydrationBuilder produces an ephemeral synthetic tool transcript
// (assistant toolCalls + matching tool results) representing a read-only
// snapshot of the shell runtime. It NEVER executes the gateway — results are
// precomputed from the read-only sources of truth.
//
// The transcript is placed AFTER a real user message (or compaction summary)
// and BEFORE the model's own output, so the model sees fresh runtime facts
// (date, workspace, memory, skills, MCP catalog, tool catalog) without those
// volatile values being baked into the stable system prompt prefix (which
// would break prompt-cache hits).
type HydrationBuilder struct {
	source HydrationSource
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
		calls = append(calls, domain.ToolCall{
			ID:   fmt.Sprintf("%s%s_%d", domain.HydrateToolCallPrefix, nonce, i),
			Name: slot.name,
			Args: "{}",
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
	content string
}

func (b *HydrationBuilder) collectSlots() []hydrationSlot {
	var slots []hydrationSlot
	slots = append(slots, b.readRuntimeContext())
	slots = append(slots, b.readMemory())
	slots = append(slots, b.readSkills())
	slots = append(slots, b.readMcpList())
	slots = append(slots, b.readToolList())
	slots = append(slots, b.readTodoList())
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

func (b *HydrationBuilder) readMemory() hydrationSlot {
	if b.source.Primary == nil {
		return hydrationSlot{name: "memory", content: "{}"}
	}
	mem := b.source.Primary.Load()
	type primaryEntry struct {
		ID      string `json:"id"`
		Content string `json:"content"`
	}
	type usage struct {
		Chars int `json:"chars"`
		Limit int `json:"limit"`
		Pct   int `json:"pct"`
	}
	out := make([]primaryEntry, 0, len(mem.Entries))
	chars := 0
	for _, e := range mem.Entries {
		out = append(out, primaryEntry{ID: e.ID, Content: e.Content})
		chars += len(e.Content)
	}
	limit := domain.PrimaryCharCap
	pct := 0
	if limit > 0 {
		pct = chars * 100 / limit
	}
	content, _ := json.Marshal(map[string]any{
		"entries": out,
		"count":   len(out),
		"usage":   usage{Chars: chars, Limit: limit, Pct: pct},
	})
	return hydrationSlot{name: "memory", content: string(content)}
}

func (b *HydrationBuilder) readSkills() hydrationSlot {
	if b.source.Skills == nil {
		return hydrationSlot{name: "skill_list", content: "[]"}
	}
	skills := b.source.Skills.List()
	type skillEntry struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
	}
	out := make([]skillEntry, 0, len(skills))
	for _, s := range skills {
		out = append(out, skillEntry{Name: s.Name, Description: s.Description})
	}
	// Sort by name for stable output (prompt-cache friendly).
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	content, _ := json.Marshal(out)
	return hydrationSlot{name: "skill_list", content: string(content)}
}

func (b *HydrationBuilder) readMcpList() hydrationSlot {
	if b.source.Plugins == nil {
		return hydrationSlot{name: "mcp_list", content: `{"plugins":[]}`}
	}
	plugins, err := b.source.Plugins.List()
	if err != nil {
		return hydrationSlot{name: "mcp_list", content: `{"plugins":[]}`}
	}
	type srvInfo struct {
		Name    string `json:"name"`
		ID      string `json:"id"`
		Running bool   `json:"running"`
		Tools   int    `json:"tools"`
		UI      bool   `json:"ui,omitempty"`
	}
	out := make([]srvInfo, 0, len(plugins))
	for _, p := range plugins {
		toolCount := 0
		isRunning := false
		if b.source.MCP != nil {
			if tools, ok := b.source.MCP.ToolsFor(p.Manifest.MCPServerID()); ok {
				isRunning = true
				toolCount = len(tools)
			}
		}
		out = append(out, srvInfo{
			Name:    p.Manifest.Name,
			ID:      p.Manifest.ID,
			Running: isRunning,
			Tools:   toolCount,
			UI:      p.HasUI,
		})
	}
	// Stable order for prompt-cache friendliness.
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	content, _ := json.Marshal(map[string]any{"plugins": out, "count": len(out)})
	return hydrationSlot{name: "mcp_list", content: string(content)}
}

func (b *HydrationBuilder) readToolList() hydrationSlot {
	tools := b.source.Tools
	type toolEntry struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
	}
	out := make([]toolEntry, 0, len(tools))
	for _, t := range tools {
		out = append(out, toolEntry{
			Name:        t.Name,
			Description: t.Description,
		})
	}
	// Sort by name for stable output (prompt-cache friendly).
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	content, _ := json.Marshal(map[string]any{"count": len(out), "tools": out})
	return hydrationSlot{name: "tool_list", content: string(content)}
}

// readTodoList injects the current conversation's todo checklist into a
// synthetic `todo_list` checkpoint. Only incomplete items are included
// (completed items are noise for the model — the UI strip still shows them).
// The checkpoint is reused while history remains intact; after compaction, a
// fresh checkpoint restores the goal and open items from the todo store.
// Returns an empty-content slot when no todo port or no conversation id is
// configured so the slot is harmless but present for call-ID alignment.
func (b *HydrationBuilder) readTodoList() hydrationSlot {
	if b.source.Todos == nil || b.source.ConvID == "" {
		return hydrationSlot{name: "todo_list", content: ""}
	}
	var sections []string
	// Goal brief: the user's original intent, set once via the `todo` tool's
	// `goal` argument and restored in a fresh checkpoint after compaction.
	goal := b.source.Todos.GetGoal(b.source.ConvID)
	if goal != "" {
		sections = append(sections, "USER GOAL (survives compaction — do not drift from this)\n"+goal)
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
