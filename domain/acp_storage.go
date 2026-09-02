package domain

import "time"

// AcpRunRecord is the persistent JSON representation of a terminal
// background-agent run. ACP completion callbacks, internal delegates, and
// subagent_wait may write the same final snapshot before surfacing its path.
//
// Unlike the in-memory AcpRun, the record is self-contained: it carries
// the full transcript, agent identity, and parent linkage so it can be
// loaded independently of the runtime.
type AcpRunRecord struct {
	ID               string               `json:"id"`
	AgentID          string               `json:"agent_id"`
	AgentName        string               `json:"agent_name"`
	ConversationID   string               `json:"conversation_id"`
	ParentToolCallID string               `json:"parent_tool_call_id"`
	Workspace        string               `json:"workspace,omitempty"`
	Prompt           string               `json:"prompt"`
	Status           AcpRunStatus         `json:"status"`
	ModelID          string               `json:"model_id,omitempty"`
	RiskTier         RiskTier             `json:"risk_tier,omitempty"`
	StopReason       string               `json:"stop_reason,omitempty"`
	Error            string               `json:"error,omitempty"`
	Transcript       []AcpTranscriptChunk `json:"transcript"`
	StartedAt        time.Time            `json:"started_at"`
	EndedAt          time.Time            `json:"ended_at"`
}

// AcpRunStorage persists terminal background-agent run records as one document per run,
// linked to the parent conversation. Saving the same run replaces its earlier
// snapshot. The application layer reads records back to restore state after a
// restart or to serve the UI's transcript drawer for historical runs.
type AcpRunStorage interface {
	Save(record AcpRunRecord) error
	Load(runID string) (AcpRunRecord, bool)
	List(conversationID string) []AcpRunRecord
	// Path returns the on-disk location where Save will write the run's
	// record, or "" when the store cannot resolve one. The application
	// layer surfaces it as the tool result's `output_path` so the parent
	// agent can read the persisted transcript directly.
	Path(conversationID, runID string) string
}

// SubagentToolName is the provider-facing name of the async ACP subagent
// tool. The spawn call is persisted with role assistant; its result arrives
// later as a synthetic subagent_result tool call.
const SubagentToolName = "subagent"

// SubagentResultToolName is the synthetic tool name injected into the
// parent agent's message history when a subagent completes. The tool
// call ID uses the SubagentResultPrefix so it can be filtered from the
// UI (like hydration calls) if needed, but unlike hydration the output
// is a real text summary the parent agent reads and acts on.
const SubagentResultToolName = "subagent_result"

// SubagentResultPrefix is the call-ID namespace for injected subagent
// completion tool calls. Uses only characters allowed by strict provider
// ID patterns (same constraint as HydrateToolCallPrefix).
const SubagentResultPrefix = "subagent-result-"

// IsSubagentResultCallID returns true when a tool call ID belongs to an
// injected subagent completion (prefix "subagent-result-").
func IsSubagentResultCallID(id string) bool {
	return len(id) >= len(SubagentResultPrefix) && id[:len(SubagentResultPrefix)] == SubagentResultPrefix
}
