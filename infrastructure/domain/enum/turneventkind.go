package enum

// TurnEventKind identifies which payload a TurnEvent carries.
type TurnEventKind string

const (
	// TurnEventDelta carries an incremental text chunk.
	TurnEventDelta TurnEventKind = "delta"
	// TurnEventReasoning carries an incremental provider reasoning chunk.
	TurnEventReasoning TurnEventKind = "reasoning"
	// TurnEventReasoningSummary carries an incremental provider reasoning
	// summary chunk.
	TurnEventReasoningSummary TurnEventKind = "reasoning_summary"
	// TurnEventToolCall carries a completed tool call.
	TurnEventToolCall TurnEventKind = "tool_call"
	// TurnEventToolOutput carries a live output chunk from a running tool
	// call — the call id rides in ToolCall(), the chunk in Delta().
	TurnEventToolOutput TurnEventKind = "tool_output"
	// TurnEventToolResult reports a tool call's terminal outcome — the call
	// id rides in ToolCall(), Failed() says whether it failed.
	TurnEventToolResult TurnEventKind = "tool_result"
	// TurnEventSteer carries a user steering message folded into the
	// running turn — the text rides in Delta().
	TurnEventSteer TurnEventKind = "steer"
	// TurnEventFinished is the terminal event: finish reason plus usage.
	TurnEventFinished TurnEventKind = "finished"
	// TurnEventError reports a mid-stream failure.
	TurnEventError TurnEventKind = "error"
	// TurnEventStatus reports a harness-side wait while no provider output
	// moves — the TurnStatus rides in Status(), the pause in Wait().
	TurnEventStatus TurnEventKind = "status"
	// TurnEventApproval asks the owner to decide a permission request the
	// running agent raised — the request rides in Approval().
	TurnEventApproval TurnEventKind = "approval"
	// TurnEventApprovalResolved reports the decision on an earlier
	// approval request — Approval() carries the request, Delta() the
	// chosen option id ("" when the request was withdrawn).
	TurnEventApprovalResolved TurnEventKind = "approval_resolved"
	// TurnEventPlan carries an agent's whole current plan (JSON entries:
	// content, priority, status); each one replaces the last.
	TurnEventPlan TurnEventKind = "plan"
	// TurnEventElicitation asks the owner for input the agent needs;
	// TurnEventElicitationResolved reports the answer (Delta is the action,
	// "" when withdrawn).
	TurnEventElicitation         TurnEventKind = "elicitation"
	TurnEventElicitationResolved TurnEventKind = "elicitation_resolved"
	// TurnEventContext carries what an agent reports about its session's
	// context: tokens held, window size, and cost so far.
	TurnEventContext TurnEventKind = "context"
	// TurnEventNotice tells the owner something the agent or the runtime
	// wants known: a severity, an optional title, and the text (Delta).
	TurnEventNotice TurnEventKind = "notice"
	// TurnEventTitle carries the title an agent suggests for the room
	// (Delta).
	TurnEventTitle TurnEventKind = "title"
	// TurnEventMedia carries a picture, sound, or file the agent put in
	// its reply; the attachment says where it is kept.
	TurnEventMedia TurnEventKind = "media"
)

func (k TurnEventKind) String() string {
	return string(k)
}

func ParseTurnEventKind(v string) (TurnEventKind, bool) {
	switch k := TurnEventKind(v); k {
	case TurnEventDelta, TurnEventReasoning, TurnEventReasoningSummary, TurnEventToolCall, TurnEventToolOutput, TurnEventToolResult, TurnEventSteer, TurnEventFinished, TurnEventError, TurnEventStatus, TurnEventApproval, TurnEventApprovalResolved, TurnEventPlan, TurnEventElicitation, TurnEventElicitationResolved, TurnEventContext, TurnEventNotice, TurnEventTitle, TurnEventMedia:
		return k, true
	}
	return "", false
}
