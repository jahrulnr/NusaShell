package domain

import "time"

// AcpRunRecord is the persistent JSON representation of a completed (or
// failed) ACP subagent run. It is written to storage as JSONL — one line
// per run — so the transcript can be replayed or audited without holding
// every run in memory.
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

// AcpRunStorage persists completed ACP run records as JSONL. The runtime
// writes a record when a run finishes (done, failed, or cancelled). The
// application layer reads records back to restore state after a restart
// or to serve the UI's transcript drawer for historical runs.
type AcpRunStorage interface {
	Save(record AcpRunRecord) error
	Load(runID string) (AcpRunRecord, bool)
	List(conversationID string) []AcpRunRecord
}

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
