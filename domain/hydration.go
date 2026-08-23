package domain

// HydrateToolCallPrefix is the reserved call-ID namespace for the hidden
// runtime-hydration synthetic transcript. Tool calls with this prefix are
// precomputed snapshots (runtime_context, memory, skill, mcp_list,
// tool_list), never real gateway executions. They are filtered from compaction
// summaries and UI rendering.
//
// The prefix uses only characters allowed by strict provider ID patterns
// (e.g. Bedrock requires ^[a-zA-Z0-9_-]+$). The previous "hydrate:" prefix
// contained a colon which Bedrock rejected.
const HydrateToolCallPrefix = "hydrate-"

// IsHydrationCallID returns true when a tool call ID belongs to the hidden
// hydration transcript (prefix "hydrate-").
func IsHydrationCallID(id string) bool {
	return len(id) >= len(HydrateToolCallPrefix) && id[:len(HydrateToolCallPrefix)] == HydrateToolCallPrefix
}
