package domain

// BackgroundRunInfo is the hydration-slot description of one active
// background/async tool run: which tool spawned it, its run ID (used to
// correlate the later synthetic result call), and any available detail about
// the worker. Producers today are ACP subagents and internal delegates;
// future async tools that queue their completion through the shared
// pending-run registry appear here too.
type BackgroundRunInfo struct {
	// ID is the background run ID. The completion result correlates on it:
	// subagent_result and delegate_result carry the same ID in their args.
	ID string `json:"id"`
	// Tool is the tool name that spawned the run ("subagent", "delegate").
	Tool string `json:"tool"`
	// Status is the lifecycle status as known to the parent: "pending"
	// until the completion has been injected into the parent conversation.
	Status string `json:"status,omitempty"`
	// Agent is the worker agent name when known (delegate runs carry one).
	Agent string `json:"agent,omitempty"`
	// Model is the worker model when known.
	Model string `json:"model,omitempty"`
	// Workspace is the worker workspace when known.
	Workspace string `json:"workspace,omitempty"`
}
