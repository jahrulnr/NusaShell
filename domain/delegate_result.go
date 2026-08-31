package domain

import "encoding/json"

// Delegate completion delivery: when an internal NusaShell delegate agent
// finishes, the original `delegate` tool call is updated to a brief
// terminal status and a synthetic `delegate_result` tool call carrying the
// full output is injected into the parent conversation (the same
// push-completion pattern as subagent_result). The synthetic call keeps
// the result in fresh context — the model processes it like any tool
// output it just received — without touching the cache-stable system
// prompt.

const (
	// DelegateToolName is the provider-facing name of the delegation tool.
	DelegateToolName = "delegate"
	// DelegateResultToolName is the synthetic result tool call injected
	// when a delegate finishes.
	DelegateResultToolName = "delegate_result"
	// DelegateResultPrefix is the reserved call-ID namespace for
	// synthetic delegate_result calls.
	DelegateResultPrefix = "delegate-result-"
)

// DelegateResultArgs builds the JSON args for a synthetic delegate_result
// tool call: the delegate run ID and its hidden conversation ID so the
// parent can correlate and read details.
func DelegateResultArgs(runID, conversationID string) string {
	b, _ := json.Marshal(map[string]string{"id": runID, "conversation": conversationID})
	return string(b)
}

// IsDelegateResultCallID reports whether a call ID belongs to the
// synthetic delegate_result namespace.
func IsDelegateResultCallID(id string) bool {
	return len(id) >= len(DelegateResultPrefix) && id[:len(DelegateResultPrefix)] == DelegateResultPrefix
}

// DelegateBriefResult is the short terminal output written to the
// original `delegate` tool call when the run finishes: status + pointer
// to the synthetic delegate_result tool call that carries the full
// output.
func DelegateBriefResult(runID string, ok bool) string {
	if ok {
		return "Delegate run " + runID + " completed. Full result delivered in the delegate_result tool call."
	}
	return "Delegate run " + runID + " failed. Error details in the delegate_result tool call."
}
