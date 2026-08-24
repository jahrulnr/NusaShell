package domain

// Continue_stream delivery: when a turn is retried after a transient
// upstream failure with partial assistant content, the continuation
// instruction is injected as an ephemeral synthetic tool call + result
// in the provider request. The model processes it like any tool output
// and continues from where it stopped — without mutating the
// cache-stable system prompt.

// ContinueStreamToolName is the synthetic tool name carrying the
// continuation instruction. It is never advertised to the model as a
// callable tool.
const ContinueStreamToolName = "continue_stream"

// ContinueStreamToolCallPrefix is the reserved call-ID namespace for
// injected continuation instructions. Uses only characters allowed by
// strict provider ID patterns (same constraint as
// HydrateToolCallPrefix).
const ContinueStreamToolCallPrefix = "cont-"

// ContinueStreamMessage is the tool result text the model receives on a
// continuation round.
const ContinueStreamMessage = "The immediately preceding assistant response was interrupted by a transient upstream failure. Continue it from exactly where it stopped. Do not repeat prior text."

// IsContinueStreamCallID returns true when a tool call ID belongs to an
// injected continuation instruction (prefix "cont-").
func IsContinueStreamCallID(id string) bool {
	return len(id) >= len(ContinueStreamToolCallPrefix) && id[:len(ContinueStreamToolCallPrefix)] == ContinueStreamToolCallPrefix
}
