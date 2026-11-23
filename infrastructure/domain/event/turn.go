package event

import (
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// TurnEvent is a domain event emitted while Send streams a response.
// Exactly one payload field is populated per Kind.
type TurnEvent struct {
	kind             enum.TurnEventKind
	delta            string
	toolCall         entity.ToolCall
	finishReason     enum.FinishReason
	usage            valueobject.Usage
	err              error
	status           enum.TurnStatus
	wait             time.Duration
	approval         valueobject.ApprovalRequest
	elicitation      valueobject.Elicitation
	agentContext     valueobject.AgentContext
	notice           enum.NoticeSeverity
	title            string
	media            valueobject.Attachment
	toolFailed       bool
	steerAttachments []valueobject.Attachment
}

// NewStatusEvent reports a harness-side wait while no provider output is
// moving. wait is the pause length, zero when the status has no deadline.
func NewStatusEvent(status enum.TurnStatus, wait time.Duration) TurnEvent {
	return TurnEvent{kind: enum.TurnEventStatus, status: status, wait: wait}
}

func NewDeltaEvent(delta string) TurnEvent {
	return TurnEvent{kind: enum.TurnEventDelta, delta: delta}
}

// NewReasoningEvent carries a provider reasoning chunk separately from the
// assistant's answer text so callers can preserve and display its position.
func NewReasoningEvent(delta string) TurnEvent {
	return TurnEvent{kind: enum.TurnEventReasoning, delta: delta}
}

// NewReasoningSummaryEvent carries a provider-supplied reasoning summary
// separately from the assistant's answer text.
func NewReasoningSummaryEvent(delta string) TurnEvent {
	return TurnEvent{kind: enum.TurnEventReasoningSummary, delta: delta}
}

func NewToolCallEvent(tc entity.ToolCall) TurnEvent {
	return TurnEvent{kind: enum.TurnEventToolCall, toolCall: tc}
}

// NewToolOutputEvent carries a live chunk from a running tool — a preview
// for the frontend, not the persisted output. The call's id rides in
// ToolCall so parallel calls stay attributable.
func NewToolOutputEvent(call entity.ToolCall, chunk string) TurnEvent {
	return TurnEvent{kind: enum.TurnEventToolOutput, toolCall: call, delta: chunk}
}

// NewToolResultEvent reports a tool call's terminal outcome. The call id
// rides in ToolCall; failed tells the owner-facing frontend whether the
// execution succeeded, independently of the output's own wording.
func NewToolResultEvent(call entity.ToolCall, failed bool) TurnEvent {
	return TurnEvent{kind: enum.TurnEventToolResult, toolCall: call, toolFailed: failed}
}

// NewSteerEvent reports a user steering message folded into the turn —
// emitted at the point it lands in history, so a frontend renders it in
// position rather than at submit time. The text rides in Delta(); any
// uploads it carried ride in SteerAttachments().
func NewSteerEvent(text string, attachments ...valueobject.Attachment) TurnEvent {
	return TurnEvent{kind: enum.TurnEventSteer, delta: text, steerAttachments: attachments}
}

func NewFinishedEvent(reason enum.FinishReason, usage valueobject.Usage) TurnEvent {
	return TurnEvent{kind: enum.TurnEventFinished, finishReason: reason, usage: usage}
}

func NewErrorEvent(err error) TurnEvent {
	return TurnEvent{kind: enum.TurnEventError, err: err}
}

// NewApprovalEvent asks the owner to decide a permission request the
// running agent raised.
func NewApprovalEvent(req valueobject.ApprovalRequest) TurnEvent {
	return TurnEvent{kind: enum.TurnEventApproval, approval: req}
}

// NewApprovalResolvedEvent reports how an approval request ended. optionID
// is the chosen option, "" when the request was withdrawn unanswered.
func NewApprovalResolvedEvent(req valueobject.ApprovalRequest, optionID string) TurnEvent {
	return TurnEvent{kind: enum.TurnEventApprovalResolved, approval: req, delta: optionID}
}

// NewElicitationEvent asks the owner for input.
func NewElicitationEvent(e valueobject.Elicitation) TurnEvent {
	return TurnEvent{kind: enum.TurnEventElicitation, elicitation: e}
}

// NewElicitationResolvedEvent reports the answer's action ("" withdrawn).
func NewElicitationResolvedEvent(e valueobject.Elicitation, action string) TurnEvent {
	return TurnEvent{kind: enum.TurnEventElicitationResolved, elicitation: e, delta: action}
}

// Elicitation is the request of an elicitation event.
func (e TurnEvent) Elicitation() valueobject.Elicitation { return e.elicitation }

// NewPlanEvent carries an agent's current plan as JSON entries.
func NewPlanEvent(entries string) TurnEvent {
	return TurnEvent{kind: enum.TurnEventPlan, delta: entries}
}

// NewContextEvent carries an agent's latest report about its session's
// context window.
func NewContextEvent(a valueobject.AgentContext) TurnEvent {
	return TurnEvent{kind: enum.TurnEventContext, agentContext: a}
}

// NewNoticeEvent tells the owner something about the turn. title may be
// empty; text is the message and rides in Delta().
func NewNoticeEvent(severity enum.NoticeSeverity, title, text string) TurnEvent {
	return TurnEvent{kind: enum.TurnEventNotice, notice: severity, title: title, delta: text}
}

// NewTitleEvent carries the title an agent suggests for the room; it rides
// in Delta().
func NewTitleEvent(title string) TurnEvent {
	return TurnEvent{kind: enum.TurnEventTitle, delta: title}
}

// NewMediaEvent carries a file the agent put in its reply.
func NewMediaEvent(a valueobject.Attachment) TurnEvent {
	return TurnEvent{kind: enum.TurnEventMedia, media: a}
}

// NoticeSeverity and NoticeTitle describe a notice event.
func (e TurnEvent) NoticeSeverity() enum.NoticeSeverity { return e.notice }
func (e TurnEvent) NoticeTitle() string                 { return e.title }

// Media is the attachment of a media event.
func (e TurnEvent) Media() valueobject.Attachment { return e.media }

// AgentContext is the report of a context event.
func (e TurnEvent) AgentContext() valueobject.AgentContext { return e.agentContext }

func (e TurnEvent) Kind() enum.TurnEventKind { return e.kind }
func (e TurnEvent) Delta() string            { return e.delta }

// SteerAttachments lists the uploads a steering input carried; empty
// for any other event or a text-only steer.
func (e TurnEvent) SteerAttachments() []valueobject.Attachment {
	return e.steerAttachments
}
func (e TurnEvent) ToolCall() entity.ToolCall       { return e.toolCall }
func (e TurnEvent) ToolFailed() bool                { return e.toolFailed }
func (e TurnEvent) FinishReason() enum.FinishReason { return e.finishReason }
func (e TurnEvent) Usage() valueobject.Usage        { return e.usage }
func (e TurnEvent) Err() error                      { return e.err }
func (e TurnEvent) Status() enum.TurnStatus         { return e.status }
func (e TurnEvent) Wait() time.Duration             { return e.wait }
func (e TurnEvent) Approval() valueobject.ApprovalRequest {
	return e.approval
}

// TurnEventHandler consumes the events of one turn — one Send call.
// Slow handlers apply backpressure on the read loop; implementations
// must not invoke it concurrently.
type TurnEventHandler func(TurnEvent)
