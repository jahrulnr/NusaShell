package domain

import "encoding/json"

// Subagent completion delivery: when an async ACP subagent finishes, the
// original `subagent` tool call is updated to a brief terminal status and
// a synthetic `subagent_result` tool call carrying the full result is
// injected into the conversation (see acp_storage.go for the tool name,
// call-ID prefix, and IsSubagentResultCallID). The synthetic call keeps
// the result in fresh context (the model processes it like any tool
// output it just received) instead of silently rewriting a tool call
// buried in old history — and it never touches the cache-stable system
// prompt.

// SubagentResultArgs builds the JSON args for a synthetic subagent_result
// tool call: the run ID so the parent can correlate it with the original
// spawn, especially for parallel count>1 spawns.
func SubagentResultArgs(runID string) string {
	b, _ := json.Marshal(map[string]string{"id": runID})
	return string(b)
}

// SubagentBriefResult is the short terminal output written to the
// original `subagent` tool call when a run finishes: status + pointer to
// the synthetic subagent_result tool call that carries the full output.
func SubagentBriefResult(run *AcpRun) string {
	switch run.Status {
	case AcpRunCompleted:
		return "Subagent run " + run.ID + " completed. Full result delivered in the subagent_result tool call."
	case AcpRunFailed:
		return "Subagent run " + run.ID + " failed. Error details in the subagent_result tool call."
	case AcpRunCancelled:
		return "Subagent run " + run.ID + " was cancelled."
	default:
		return "Subagent run " + run.ID + " ended (status: " + string(run.Status) + "). Full result in the subagent_result tool call."
	}
}
