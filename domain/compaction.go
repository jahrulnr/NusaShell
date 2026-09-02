package domain

// Compaction budget policy constants.
//
// These are domain policy: they govern how the compaction summarization
// pass allocates the model's context window across retained messages,
// the summary output budget, system framing overhead, and the quality
// guard. The application compaction path reads them when building the
// per-pass request; lifting them here keeps the policy visible at the
// layer that owns the conversation model.
const (
	// CompactionKeepTokenBudget is the retained recent-messages token
	// budget after compaction.
	CompactionKeepTokenBudget = 64000
	// CompactionSummaryMaxOut is the default max_output_tokens for the
	// compaction summarization request.
	CompactionSummaryMaxOut = 64000
	// CompactionSystemReserve is the token reserve for the system prompt
	// and framing overhead when computing the per-pass content budget.
	CompactionSystemReserve = 300
	// CompactionSummaryMinChars is the minimum summary length for the
	// quality guard. Summaries shorter than this are considered failed
	// and retried.
	CompactionSummaryMinChars = 200
	// CompactionSummaryMaxRetries is the max number of retry attempts
	// when the summary is too short. Each retry doubles the
	// max_output_tokens budget.
	CompactionSummaryMaxRetries = 2
	// CompactionMaxToolCallChars caps a single tool call's args/output
	// when building the compaction input. Tool results can be unbounded
	// (grep over huge lines, mcp_call, file_write content), and one
	// oversized call must still fit inside the compaction model's
	// context window — otherwise the summarization pass overflows and
	// compaction fails (the turn then dies with a context-overflow 400).
	// Truncated payloads keep an omission marker so the summary model
	// knows content was dropped.
	CompactionMaxToolCallChars = 200_000
)

// CompactionTrigger identifies why a compaction was run. Persisted with the
// journal compaction event so the audit trail distinguishes turn-start
// compaction, mid-turn proactive compaction, and stream-overflow recovery.
type CompactionTrigger string

const (
	// CompactionTriggerInitial runs at turn start when the estimated
	// context already exceeds the trigger watermark.
	CompactionTriggerInitial CompactionTrigger = "initial"
	// CompactionTriggerProactive runs between rounds (pre-API) when tool
	// results have grown the context past the watermark.
	CompactionTriggerProactive CompactionTrigger = "proactive"
	// CompactionTriggerEmergency recovers from a context/TPM overflow
	// raised by the provider during the stream.
	CompactionTriggerEmergency CompactionTrigger = "emergency"
	// CompactionTriggerMidTool runs at the tool-request boundary: the model
	// has requested a tool round, the round was persisted, and the estimated
	// context (including the requested calls but before any tool output)
	// already exceeds the trigger. Compact the prefix now — before the tool
	// outputs exist — so the summarizer never sees the tool-result explosion.
	// The in-flight assistant message is preserved verbatim (see
	// IsInFlightToolMessage) so the round's outputs land in the live tail.
	CompactionTriggerMidTool CompactionTrigger = "mid_tool"
)

// CompactionEvent is the durable audit record written to the conversation
// journal after a compaction succeeds: when it ran, which model produced the
// handoff, the retention budget used, and the resulting summary. The journal
// keeps the full-fidelity history while this record makes the compaction
// lifecycle itself auditable and resumable across restarts.
type CompactionEvent struct {
	Trigger    CompactionTrigger `json:"trigger"`
	Model      string            `json:"model,omitempty"`
	KeepBudget int               `json:"keepBudget,omitempty"`
	Summary    string            `json:"summary,omitempty"`
}

// CompactionTriggerTokens is the estimated-token watermark that starts
// compaction. When CompactionThreshold is 0 (auto, the default), compaction
// triggers at 80% of the model's available input budget (contextWindow minus
// maxOutput) — so a 256k model with 64k output compacts at ~122k input, not
// ~205k, which would overflow once the output budget is added. When
// CompactionThreshold is non-zero, it is used as the trigger but still capped
// at 80% of the available budget so a high threshold cannot wait until the
// next turn already overflows.
func CompactionTriggerTokens(contextWindow, maxOutput int, settings Settings) int {
	available := contextWindow
	if maxOutput > 0 && maxOutput < contextWindow {
		available = contextWindow - maxOutput
	}
	trigger := settings.CompactionThreshold
	budgetCap := available * 4 / 5
	if trigger <= 0 {
		// Auto: use 80% of the available input budget.
		if budgetCap > 0 {
			return budgetCap
		}
		return DefaultSettings().CompactionThreshold
	}
	if budgetCap > 0 && budgetCap < trigger {
		return budgetCap
	}
	return trigger
}

// TakeCompactionChunk takes the longest prefix of msgs whose token
// estimate fits in available. A single oversized message is still taken
// so compaction cannot stall. System markers should already have been
// stripped by the caller.
func TakeCompactionChunk(msgs []Message, available int) (chunk, rest []Message) {
	var current []Message
	currentTokens := 0
	for i, m := range msgs {
		mt := m.EstimateTokens()
		if currentTokens+mt > available && len(current) > 0 {
			return current, msgs[i:]
		}
		current = append(current, m)
		currentTokens += mt
	}
	return current, nil
}
