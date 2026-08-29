package domain

import "encoding/json"

// Harness announcements: synthetic tool calls injected by the NusaShell
// harness into a conversation's history to deliver runtime facts and chain
// steering to the model. Announcements always travel on the single
// `announcement` tool channel — one concept for the model to learn — and
// are differentiated by their self-describing args type and result text:
//
//   - restart:       the backend restarted (MCP plugins, tool availability)
//   - auto_continue: the todo-driven chain continues into a new turn
//   - interrupted:   a transient upstream failure cut the response; continue it
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

// IsAnnouncementCallID returns true when a tool call ID belongs to an
// injected announcement (prefix "announce-").
func IsAnnouncementCallID(id string) bool {
	return len(id) >= len(AnnouncementToolCallPrefix) && id[:len(AnnouncementToolCallPrefix)] == AnnouncementToolCallPrefix
}
