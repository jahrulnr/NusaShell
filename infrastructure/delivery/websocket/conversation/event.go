package wsconversation

import (
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	appjob "github.com/jahrulnr/nusashell/application/job"
	dtransport "github.com/jahrulnr/nusashell/infrastructure/delivery/transport"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
)

// onEvent projects one application turn event onto the turn's
// subscriber set under h.mu — the lock keeps a turn's frames totally
// ordered and blocks post-terminal emission. Content-bearing events
// (delta, tool input, tool output, reasoning) are chunked for outbound
// bounding: every nonempty provider chunk crosses in order, split at
// the byte bound. Reasoning also gets one phase marker per stretch.
// Mid-turn errors are the sender's retry noise — the job's own return
// produces the single terminal frame.
func (h *Hub) onEvent(t *turn, ev event.TurnEvent) {
	wait := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	if w := time.Since(wait); w > 50*time.Millisecond {
		slog.Warn("turn event waited on the hub lock",
			"conversation", t.conversationID, "turn", t.id,
			"kind", ev.Kind().String(), "waited", w)
	}
	if t.terminal || t.overflowed {
		return
	}
	switch ev.Kind() {
	case enum.TurnEventDelta:
		t.inReasoning = false
		t.live.text("text", ev.Delta())
		t.live.phase = nil
		for _, chunk := range splitDelta(ev.Delta(), outboundChunkBytes) {
			h.emitLocked(t, "turn.delta", deltaPayload{Text: chunk}, false)
		}
	case enum.TurnEventReasoning, enum.TurnEventReasoningSummary:
		// Both raw reasoning and provider-supplied summaries remain in
		// arrival order on the owner-facing transcript.
		if !t.inReasoning {
			t.inReasoning = true
			t.live.phase = &phasePayload{Phase: "reasoning"}
			if !h.emitLocked(t, "turn.phase", phasePayload{Phase: "reasoning"}, false) {
				h.markOverflowedLocked(t)
			}
		}
		t.live.text("reasoning", ev.Delta())
		for _, chunk := range splitDelta(ev.Delta(), outboundChunkBytes) {
			if !h.emitLocked(t, "turn.reasoning", reasoningPayload{Text: chunk}, false) {
				h.markOverflowedLocked(t)
				break
			}
		}
	case enum.TurnEventToolCall:
		t.inReasoning = false
		tc := ev.ToolCall()
		p := &livePart{Kind: "tool", CallID: tc.ID(), Name: tc.Name(), Status: "requested"}
		t.live.tools[tc.ID()] = p
		t.live.push(p)
		t.live.toolInput(tc.ID(), tc.Input())
		t.live.phase = nil
		if !h.emitLocked(t, "turn.tool_call", toolCallPayload{
			CallID: tc.ID(),
			Name:   tc.Name(),
		}, false) {
			// Provider-originated metadata exceeded the frame bound —
			// terminate this turn only, sanitized.
			h.markOverflowedLocked(t)
			break
		}
		// The exact argument string is streamed separately so a large
		// input can never turn the call metadata into an oversized frame.
		for _, chunk := range splitDelta(tc.Input(), outboundChunkBytes) {
			if !h.emitLocked(t, "turn.tool_input", toolInputPayload{
				CallID: tc.ID(),
				Text:   chunk,
			}, false) {
				h.markOverflowedLocked(t)
				break
			}
		}
	case enum.TurnEventToolOutput:
		// Live stream content for the local owner — every nonempty
		// chunk crosses in order at the outbound byte bound. It is
		// progress, not proof the call finished.
		t.inReasoning = false
		callID := ev.ToolCall().ID()
		t.live.toolOutput(callID, ev.Delta())
		t.live.clearPhase()
		for _, chunk := range splitDelta(ev.Delta(), outboundChunkBytes) {
			if !h.emitLocked(t, "turn.tool_output", toolOutputPayload{
				CallID: callID,
				Text:   chunk,
			}, false) {
				h.markOverflowedLocked(t)
				break
			}
		}
	case enum.TurnEventToolResult:
		t.inReasoning = false
		t.live.clearPhase()
		payload := toolResultPayload{CallID: ev.ToolCall().ID(), Failed: ev.ToolFailed()}
		for _, a := range ev.ToolCall().OutputAttachments() {
			payload.Attachments = append(payload.Attachments, attachmentPayload{
				MediaType: a.MediaType(), Modality: a.Modality().String(), URI: a.URI(), Name: a.Name(),
			})
		}
		if !h.emitLocked(t, "turn.tool_result", payload, false) {
			h.markOverflowedLocked(t)
		}
		if p := t.live.tools[ev.ToolCall().ID()]; p != nil {
			if ev.ToolFailed() {
				p.Status = "failed"
			} else {
				p.Status = "success"
			}
			p.Attachments = payload.Attachments
		}
	case enum.TurnEventSteer:
		// A folded steering line is user input landing mid-turn —
		// subscribers see it in position rather than on a history
		// re-read after the terminal. The fold claims one submitted
		// record: only a persisted, not-yet-projected receipt carrying
		// exactly this text matches — a same-text CLI line never adopts
		// a web receipt that has not consumed, and each record projects
		// once so identical consecutive inputs keep their own segment.
		t.inReasoning = false
		inputID := ""
		for _, rec := range t.steerRecords {
			if rec.projected || rec.receipt.State() != appjob.SteeringPersisted || rec.content != ev.Delta() {
				continue
			}
			rec.projected = true
			inputID = rec.inputID
			break
		}
		chunks := splitDelta(ev.Delta(), outboundChunkBytes)
		if len(chunks) == 0 {
			// An attachment-only steer has no text — still emit one
			// frame so watchers see the input land.
			chunks = []string{""}
		}
		var atts []attachmentPayload
		for _, a := range ev.SteerAttachments() {
			atts = append(atts, attachmentPayload{
				MediaType: a.MediaType(), Modality: a.Modality().String(), URI: a.URI(), Name: a.Name(),
			})
		}
		t.live.steer(inputID, ev.Delta(), atts)
		// The job persisted this line before announcing it: a retry later in
		// the turn must keep it in the live document.
		t.live.markCommitted()
		t.live.phase = nil
		for i, chunk := range chunks {
			p := steerPayload{
				Text:    chunk,
				InputID: inputID,
				Final:   i == len(chunks)-1,
			}
			if i == 0 {
				p.Attachments = atts
			}
			if !h.emitLocked(t, "turn.steer", p, false) {
				h.markOverflowedLocked(t)
				break
			}
		}
	case enum.TurnEventFinished:
		t.inReasoning = false
		t.live.clearPhase()
		// The attempt that reported this finish is the one the job keeps:
		// its output is durable history now, so a later step's retry cannot
		// withdraw it.
		t.live.markCommitted()
		if !h.emitLocked(t, "turn.step_finished", stepFinishedPayload{
			FinishReason: ev.FinishReason().String(),
			Usage:        dtransport.MarshalUsage(ev.Usage()),
		}, false) {
			h.markOverflowedLocked(t)
		}
	case enum.TurnEventStatus:
		// A harness-side wait — a retry pause or a context fold. Nothing
		// streams while it runs, so the owner is told why. The pause
		// length is information, not a deadline the client enforces.
		t.inReasoning = false
		if ev.Status() == enum.TurnStatusRetryWait {
			// The attempt that just failed is being replaced: the document a
			// reconnecting tab receives must not carry its output, the same
			// segment a watching client drops on this status.
			t.live.truncateTail()
		}
		t.live.phase = &phasePayload{
			Phase:        ev.Status().String(),
			RetryAfterMs: ev.Wait().Milliseconds(),
		}
		if !h.emitLocked(t, "turn.phase", phasePayload{
			Phase:        ev.Status().String(),
			RetryAfterMs: ev.Wait().Milliseconds(),
		}, false) {
			h.markOverflowedLocked(t)
		}
	case enum.TurnEventApproval:
		t.inReasoning = false
		t.live.clearPhase()
		r := ev.Approval()
		// The call's input is shown for context only; a bounded head
		// keeps a file-sized argument from overflowing the frame and
		// ending the turn the owner is being asked about.
		input := r.Input()
		if len(input) > outboundChunkBytes {
			input = strings.ToValidUTF8(input[:outboundChunkBytes], "") + "…"
		}
		payload := approvalPayload{ID: r.ID(), CallID: r.CallID(), Title: r.Title(), Input: input, Action: r.Action().String()}
		for i, o := range r.Options() {
			if i >= 64 {
				// A runaway options list caps like the input does — the
				// request stays answerable without its tail.
				break
			}
			payload.Options = append(payload.Options, approvalOptionPayload{ID: o.ID, Name: o.Name, Choice: o.Choice.String()})
		}
		if !h.emitLocked(t, "turn.approval", payload, false) {
			// The request stays answerable over HTTP; only this turn's
			// stream is over its bound.
			h.markOverflowedLocked(t)
		}
		p := &livePart{Kind: "approval", Request: payload, Status: "pending"}
		t.live.approvals[r.ID()] = p
		t.live.push(p)
	case enum.TurnEventApprovalResolved:
		if !h.emitLocked(t, "turn.approval_resolved", approvalResolvedPayload{ID: ev.Approval().ID(), OptionID: ev.Delta()}, false) {
			h.markOverflowedLocked(t)
		}
		if p := t.live.approvals[ev.Approval().ID()]; p != nil {
			p.OptionID = ev.Delta()
			if ev.Delta() != "" {
				p.Status = "answered"
			} else {
				p.Status = "withdrawn"
			}
		}
	case enum.TurnEventElicitation:
		t.inReasoning = false
		t.live.clearPhase()
		e := ev.Elicitation()
		payload := elicitationPayload{ID: e.ID(), Mode: e.Mode(), Message: e.Message(), Schema: e.Schema(), URL: e.URL()}
		if d := e.Deadline(); !d.IsZero() {
			payload.ExpiresAt = &d
		}
		if !h.emitLocked(t, "turn.elicitation", payload, false) {
			h.markOverflowedLocked(t)
		}
		// The wire carries the full request; the snapshot copy caps its
		// bulky fields so one form cannot crowd the whole document out —
		// a recovered card without a schema still names the request.
		docReq := payload
		if len(docReq.Schema) > outboundChunkBytes {
			docReq.Schema = nil
		}
		if len(docReq.Message) > outboundChunkBytes {
			docReq.Message = strings.ToValidUTF8(docReq.Message[:outboundChunkBytes], "") + "…"
		}
		if len(docReq.URL) > outboundChunkBytes {
			docReq.URL = ""
		}
		p := &livePart{Kind: "elicitation", Request: docReq, Status: "pending"}
		t.live.elicitations[e.ID()] = p
		t.live.push(p)
	case enum.TurnEventElicitationResolved:
		if !h.emitLocked(t, "turn.elicitation_resolved", elicitationResolvedPayload{ID: ev.Elicitation().ID(), Action: ev.Delta()}, false) {
			h.markOverflowedLocked(t)
		}
		if p := t.live.elicitations[ev.Elicitation().ID()]; p != nil {
			p.Action = ev.Delta()
			if ev.Delta() != "" {
				p.Status = "answered"
			} else {
				p.Status = "withdrawn"
			}
		}
	case enum.TurnEventPlan:
		t.inReasoning = false
		t.live.clearPhase()
		raw := json.RawMessage(ev.Delta())
		if t.live.plan != nil {
			t.live.bytes += len(raw) - len(t.live.plan.Entries)
			t.live.plan.Entries = raw
			t.live.trim()
		} else {
			p := &livePart{Kind: "plan", Entries: raw}
			t.live.plan = p
			t.live.push(p)
		}
		if !h.emitLocked(t, "turn.plan", planPayload{Entries: json.RawMessage(ev.Delta())}, false) {
			h.markOverflowedLocked(t)
		}
	case enum.TurnEventNotice:
		// Something the agent or the runtime wants the owner to know; it
		// shows as a notice, not as part of the reply.
		if !h.emitLocked(t, "turn.notice", noticePayload{Severity: ev.NoticeSeverity().String(), Title: ev.NoticeTitle(), Text: ev.Delta()}, false) {
			h.markOverflowedLocked(t)
		}
	case enum.TurnEventMedia:
		t.inReasoning = false
		t.live.clearPhase()
		a := ev.Media()
		att := attachmentPayload{MediaType: a.MediaType(), Modality: a.Modality().String(), URI: a.URI(), Name: a.Name()}
		if !h.emitLocked(t, "turn.media", att, false) {
			h.markOverflowedLocked(t)
		}
		t.live.push(&livePart{Kind: "media", Attachments: []attachmentPayload{att}})
	case enum.TurnEventTitle:
		// The room's title changes with the turn's save; the list and
		// header read it from the record.
	case enum.TurnEventContext:
		// The agent's own account of its context window; it shows in the
		// composer footer and does not belong in the transcript.
		a := ev.AgentContext()
		payload := contextPayload{Used: a.Used(), Size: a.Size()}
		if amount, currency, ok := a.Cost(); ok {
			payload.Cost = &costPayload{Amount: amount, Currency: currency}
		}
		if !h.emitLocked(t, "turn.context", payload, false) {
			h.markOverflowedLocked(t)
		}
	case enum.TurnEventError:
		// Intermediate provider errors never cross; the terminal frame
		// is the send's single reported outcome.
	}
}

// markOverflowedLocked flags a turn whose own frames can't be
// serialized inside the bound — provider-controlled strings (call ids,
// tool names) can exceed it. The context cancels so the job surfaces
// its real receipt; runTurn then emits the sanitized 413 terminal.
// No further stream events emit while overflowed. Callers hold h.mu.
func (h *Hub) markOverflowedLocked(t *turn) {
	if t.overflowed {
		return
	}
	t.overflowed = true
	t.turnCancel(nil) // a frame bound, not an actor: the turn surfaces its own 413
}

// classifyError maps a send failure through the shared delivery
// classification — upstream 429s report upstream_error, a full
// conversation mailbox reports queue_full, causes never cross.
func classifyError(err error) (int, string, string) {
	return dtransport.ClassifyError(err)
}

// noticePayload is a notice for the owner: how much it matters, an optional
// heading, and the text.
type noticePayload struct {
	Severity string `json:"severity"`
	Title    string `json:"title,omitempty"`
	Text     string `json:"text"`
}

// contextPayload is an agent's latest report about its session's context
// window: tokens held, window size, and the session's cost so far.
type contextPayload struct {
	Used int          `json:"used"`
	Size int          `json:"size"`
	Cost *costPayload `json:"cost,omitempty"`
}

type costPayload struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

// planPayload is an agent's whole current plan.
type planPayload struct {
	Entries json.RawMessage `json:"entries"`
}
