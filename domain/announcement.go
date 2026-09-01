package domain

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Harness announcements: synthetic tool calls injected by the NusaShell
// harness into a conversation's history to deliver runtime facts and chain
// steering to the model. Announcements always travel on the single
// `announcement` tool channel — one concept for the model to learn — and
// are differentiated by their self-describing args type and result text:
//
//   - restart:            the backend restarted (MCP plugins, tool availability)
//   - auto_continue:      the todo-driven chain continues into a new turn
//   - interrupted:        a transient upstream failure cut the response; continue it
//   - workspace_changed:  the user picked a new workspace; file tools now run there
//
// The model processes an announcement like any tool output — as runtime
// state, never as user speech. This is the deliberate alternative to
// injecting harness notices as synthetic user messages: models attribute
// user-role messages to the human regardless of prompt wording, and that
// attribution cannot be tuned away.

// AnnouncementToolName is the synthetic tool name carrying every harness
// announcement. It is never advertised to the model as a callable tool.
const AnnouncementToolName = "announcement"

// AnnouncementToolCallPrefix is the reserved call-ID namespace for injected
// announcements. Uses only characters allowed by strict provider ID patterns
// (same constraint as HydrateToolCallPrefix).
const AnnouncementToolCallPrefix = "announce-"

// AnnouncementMessage is the restart tool result text the agent receives
// after a backend restart.
const AnnouncementMessage = "Backend restarted. Some MCP plugins may need to be re-enabled (mcp_enable), and tool availability may have changed."

// AnnouncementInterruptedMessage is the tool result text injected when a
// transient upstream failure cut the assistant response mid-round. The model
// continues the interrupted response from exactly where it stopped.
const AnnouncementInterruptedMessage = "The immediately preceding assistant response was interrupted by a transient upstream failure. Continue it from exactly where it stopped. Do not repeat prior text."

// AutoContinueAnnouncementArgs builds the self-describing args payload for an
// auto-continue announcement: the notice type plus the chain state (rounds
// used, open todos) so the model reads the state from the data itself instead
// of guessing it from conversation context.
func AutoContinueAnnouncementArgs(continuesUsed, openTodos int) string {
	b, err := json.Marshal(struct {
		Type          string `json:"type"`
		ContinuesUsed int    `json:"continues_used"`
		OpenTodos     int    `json:"open_todos"`
	}{Type: "auto_continue", ContinuesUsed: continuesUsed, OpenTodos: openTodos})
	if err != nil {
		return "{}"
	}
	return string(b)
}

// WorkspaceChangedAnnouncementArgs builds the self-describing args payload
// for a workspace-switch announcement: type plus the previous and new
// absolute paths so the model reads the change from the data itself.
func WorkspaceChangedAnnouncementArgs(from, to string) string {
	b, err := json.Marshal(struct {
		Type string `json:"type"`
		From string `json:"from,omitempty"`
		To   string `json:"to"`
	}{Type: "workspace_changed", From: from, To: to})
	if err != nil {
		return "{}"
	}
	return string(b)
}

// WorkspaceChangedAnnouncementMessage is the announcement tool result text
// for a workspace switch. An empty from means the conversation had no
// workspace yet (first pick on a room that already has history).
func WorkspaceChangedAnnouncementMessage(from, to string) string {
	if strings.TrimSpace(from) == "" {
		return fmt.Sprintf("Workspace set to %s. File tools now run against this workspace.", to)
	}
	return fmt.Sprintf("Workspace changed from %s to %s. File tools now run against the new workspace.", from, to)
}

// AnnouncementConfigChangedArgs builds the self-describing args payload for
// a config-change announcement: the notice type plus the changed surfaces
// (e.g. "subagent", "user_prompt", "provider"). The model reads the change
// from the data itself; the new system prompt / tool descriptions already
// travel in the same request, so the announcement stays implicit.
func AnnouncementConfigChangedArgs(changed []string) string {
	if changed == nil {
		changed = []string{}
	}
	b, err := json.Marshal(struct {
		Type    string   `json:"type"`
		Changed []string `json:"changed"`
	}{Type: "config_changed", Changed: changed})
	if err != nil {
		return "{}"
	}
	return string(b)
}

// AnnouncementConfigChangedMessage is the announcement tool result text for
// a config change. It flags the change and points at the refresh path
// without dumping content — the model re-reads the affected surfaces from
// the request itself.
func AnnouncementConfigChangedMessage(changed []string) string {
	if len(changed) == 0 {
		return "Tool/system configuration changed since your last turn. Re-read the affected tool descriptions and instructions."
	}
	return fmt.Sprintf("Tool/system configuration changed since your last turn: %s. Re-read the affected tool descriptions and instructions.", strings.Join(changed, ", "))
}

// AnnouncementMemoryChangedArgs builds the self-describing args payload for
// a memory-change announcement: the notice type plus the affected tier
// (primary|fragment) and mutation op (save|replace|delete).
func AnnouncementMemoryChangedArgs(tier, op string) string {
	b, err := json.Marshal(struct {
		Type string `json:"type"`
		Tier string `json:"tier,omitempty"`
		Op   string `json:"op"`
	}{Type: "memory_changed", Tier: tier, Op: op})
	if err != nil {
		return "{}"
	}
	return string(b)
}

// AnnouncementMemoryChangedMessage is the announcement tool result text for
// a memory change. The model refreshes via the real `memory` tool instead of
// waiting for the next hydration epoch.
func AnnouncementMemoryChangedMessage() string {
	return "Memory was updated outside this conversation. Call `memory` op=list to refresh."
}

// AnnouncementSkillsChangedArgs builds the self-describing args payload for
// a skill-library change: the notice type plus the mutation op
// (save|delete|install).
func AnnouncementSkillsChangedArgs(op string) string {
	b, err := json.Marshal(struct {
		Type string `json:"type"`
		Op   string `json:"op"`
	}{Type: "skills_changed", Op: op})
	if err != nil {
		return "{}"
	}
	return string(b)
}

// AnnouncementSkillsChangedMessage is the announcement tool result text for
// a skill-library change. The model refreshes via the real `skill` tool.
func AnnouncementSkillsChangedMessage() string {
	return "The skill library changed. Call `skill` op=list to refresh."
}

// IsAnnouncementCallID returns true when a tool call ID belongs to an
// injected announcement (prefix "announce-").
func IsAnnouncementCallID(id string) bool {
	return len(id) >= len(AnnouncementToolCallPrefix) && id[:len(AnnouncementToolCallPrefix)] == AnnouncementToolCallPrefix
}
