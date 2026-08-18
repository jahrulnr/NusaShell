package domain

// ShouldContinueFailedTurn reports whether a retry should freeze partial
// output and ask the new model to continue. Tool-bearing failures are always
// restarted from scratch so a leftover continuation flag cannot skip tool
// work or consume the mid-stream continuation budget.
func ShouldContinueFailedTurn(failed Message) bool {
	return (failed.Content != "" || failed.Reasoning != "") && len(failed.ToolCalls) == 0
}
