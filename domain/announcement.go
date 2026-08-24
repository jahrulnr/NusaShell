package domain

// Restart announcement: a synthetic tool call injected into a persisted
// conversation's history when the backend restarts and the user sends
// the first message afterwards. The model sees a real tool result telling
// it the runtime came back up (MCP plugins may need re-enabling, tool
// availability may have changed) without those facts being baked into
// the cache-stable system prompt.

// AnnouncementToolName is the synthetic tool name of the restart
// announcement. It is never advertised to the model as a callable tool.
const AnnouncementToolName = "announcement"

// AnnouncementToolCallPrefix is the reserved call-ID namespace for
// restart announcements. Uses only characters allowed by strict provider
// ID patterns (same constraint as HydrateToolCallPrefix).
const AnnouncementToolCallPrefix = "announce-"

// AnnouncementMessage is the tool result text the agent receives after a
// backend restart.
const AnnouncementMessage = "Backend restarted. Some MCP plugins may need to be re-enabled (mcp_enable), and tool availability may have changed."

// IsAnnouncementCallID returns true when a tool call ID belongs to an
// injected restart announcement (prefix "announce-").
func IsAnnouncementCallID(id string) bool {
	return len(id) >= len(AnnouncementToolCallPrefix) && id[:len(AnnouncementToolCallPrefix)] == AnnouncementToolCallPrefix
}
