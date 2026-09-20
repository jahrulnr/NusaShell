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
//   - config_changed:     a named runtime configuration changed
//   - memory_changed:     user.md or soul.md changed
//   - skills_changed:     a named skill changed
//   - peer_message:       another conversation sent a message
//   - task_memory:        relevant structured memory was found
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
// instructionFiles is the gitignore-aware AGENTS.md index for the new
// workspace (runtime_context is not rebuilt on a mid-conversation switch).
func WorkspaceChangedAnnouncementArgs(from, to string, instructionFiles []string) string {
	b, err := json.Marshal(struct {
		Type             string   `json:"type"`
		From             string   `json:"from,omitempty"`
		To               string   `json:"to"`
		InstructionFiles []string `json:"instruction_files,omitempty"`
	}{Type: "workspace_changed", From: from, To: to, InstructionFiles: instructionFiles})
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
// a config-change announcement: the notice type plus concrete changed
// surfaces, names, and actions (for example "subagent codex has disabled").
// The new system prompt / tool descriptions already travel in the same
// request, so the announcement does not duplicate their full contents.
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

// AnnouncementConfigChangedDetail formats one concrete runtime configuration
// change for both the announcement args and its result text. Enabled and
// disabled changes intentionally use the terse wording shown to the model,
// while deletion keeps the grammar explicit.
func AnnouncementConfigChangedDetail(scope, name, action string) string {
	scope = strings.TrimSpace(scope)
	name = strings.TrimSpace(name)
	action = strings.TrimSpace(strings.ToLower(action))
	subject := scope
	if name != "" {
		if subject != "" {
			subject += " "
		}
		subject += name
	}
	if subject == "" {
		subject = "configuration"
	}
	if action == "" {
		action = "changed"
	}
	if action == "deleted" {
		return fmt.Sprintf("%s has been deleted", subject)
	}
	return fmt.Sprintf("%s has %s", subject, action)
}

// AnnouncementConfigChangedMessage is the announcement tool result text for
// a config change. The changed entries are concrete enough to tell the model
// which named surface changed, while the new system prompt/tool descriptions
// travel in the same request.
func AnnouncementConfigChangedMessage(changed []string) string {
	if len(changed) == 0 {
		return "Configuration has changed. Re-read the affected tool descriptions and instructions."
	}
	return fmt.Sprintf("%s. Re-read the affected tool descriptions and instructions.", strings.Join(changed, "; "))
}

// AnnouncementMemoryChangedArgs builds the self-describing args payload for
// a memory-change announcement: the notice type plus the affected tier
// (user|agent|record) and mutation op (update|retire).
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
// a user.md or soul.md change. The absolute path points the model at the
// primary document instead of making it guess which memory surface changed.
func AnnouncementMemoryChangedMessage(tier, path string) string {
	filename := "user.md"
	if strings.EqualFold(strings.TrimSpace(tier), "agent") || strings.EqualFold(strings.TrimSpace(tier), "soul") {
		filename = "soul.md"
	}
	path = strings.TrimSpace(path)
	if path == "" {
		path = filename
	}
	return fmt.Sprintf("%s has changed, read %s to see primary memory", filename, path)
}

// AnnouncementSkillsChangedArgs builds the self-describing args payload for
// a skill-library change: the notice type, mutation op (save|delete|install),
// and affected skill name.
func AnnouncementSkillsChangedArgs(op, name string) string {
	b, err := json.Marshal(struct {
		Type string `json:"type"`
		Op   string `json:"op"`
		Name string `json:"name,omitempty"`
	}{Type: "skills_changed", Op: op, Name: strings.TrimSpace(name)})
	if err != nil {
		return "{}"
	}
	return string(b)
}

// AnnouncementSkillsChangedMessage is the announcement tool result text for
// a named skill-library change. The model can avoid rereading unrelated
// skills, while the skill body remains available through the real `skill` /
// `file_read` path.
func AnnouncementSkillsChangedMessage(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "skill has changed, re-read if you are using this skill"
	}
	return fmt.Sprintf("skill %s has changed, re-read if you are using this skill", name)
}

// AnnouncementPeerMessageArgs builds the self-describing args payload for a
// peer message from another conversation room.
func AnnouncementPeerMessageArgs(fromConvID string) string {
	b, err := json.Marshal(struct {
		Type string `json:"type"`
		From string `json:"from"`
	}{Type: "peer_message", From: fromConvID})
	if err != nil {
		return "{}"
	}
	return string(b)
}

// AnnouncementPeerMessageMessage builds the user-visible announcement result text
// for receiving a message from another conversation room.
func AnnouncementPeerMessageMessage(fromConvID, content string) string {
	return fmt.Sprintf("You received message from conversation `%s`, use `conversation(op=\"send\", id=\"%s\", content=\"...\")` to reply:\n> %s", fromConvID, fromConvID, strings.ReplaceAll(content, "\n", "\n> "))
}

// AnnouncementAutomationEventArgs builds the self-describing args payload for
// a message delivered by an automation workflow (`uses: conversation.wake`).
func AnnouncementAutomationEventArgs(source string) string {
	b, err := json.Marshal(struct {
		Type   string `json:"type"`
		Source string `json:"source"`
	}{Type: "automation_event", Source: source})
	if err != nil {
		return "{}"
	}
	return string(b)
}

// AnnouncementAutomationEventMessage builds the user-visible announcement
// result text for a workflow-delivered message.
func AnnouncementAutomationEventMessage(source, content string) string {
	return fmt.Sprintf("Automation `%s` delivered a message to this conversation:\n> %s", source, strings.ReplaceAll(content, "\n", "\n> "))
}

// IsAnnouncementCallID returns true when a tool call ID belongs to an
// injected announcement (prefix "announce-").
func IsAnnouncementCallID(id string) bool {
	return len(id) >= len(AnnouncementToolCallPrefix) && id[:len(AnnouncementToolCallPrefix)] == AnnouncementToolCallPrefix
}
