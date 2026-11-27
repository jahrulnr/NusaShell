package wsconversation

import (
	"encoding/json"
	"hash/fnv"
	"strings"

	appjob "github.com/jahrulnr/nusashell/application/job"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// turnOutcome records the emitted terminal verdict so a late
// turn.resync_required can replay the stored outcome instead of a bare
// frame.
type turnOutcome struct {
	typ     string // "completed" or "failed"
	payload terminalPayload
}

// steerAckFrame is the unsequenced command reply for turn.steer — it
// answers the issuing socket only. The journaled sequenced ack stays
// the watcher-visible record; retries and unattached issuers get this
// reply so an accepted submission is never indistinguishable from
// silence, and re-acks can never burn the bounded journal.
func steerAckFrame(requestID, convID, turnID, inputID string) serverFrame {
	return serverFrame{
		Type:           "turn.steer_ack",
		RequestID:      requestID,
		ConversationID: convID,
		TurnID:         turnID,
		Payload:        steerInputPayload{InputID: inputID},
	}
}

// steerRecalledFrame is the unsequenced command reply for
// turn.steer_recall — same contract as steerAckFrame.
func steerRecalledFrame(requestID, convID, turnID, inputID string) serverFrame {
	return serverFrame{
		Type:           "turn.steer_recalled",
		RequestID:      requestID,
		ConversationID: convID,
		TurnID:         turnID,
		Payload:        steerInputPayload{InputID: inputID},
	}
}

// steerFingerprint is what a retried requestId is compared against: the same
// complete input re-acks, a different one is refused. The hub does not keep
// the input — the mailbox that holds it does.
func steerFingerprint(p messageInputPayload) uint64 {
	raw, err := json.Marshal(p)
	if err != nil {
		return 0
	}
	h := fnv.New64a()
	h.Write(raw)
	return h.Sum64()
}

// steeringSnapshotLocked reads the turn's accepted submissions in order — the
// bounded snapshot terminal and resync frames carry. It is a projection of
// what this turn reported (ack, fold, recall), not a mailbox: the inputs live
// with the worker that folds them. Callers hold h.mu.
func (t *turn) steeringSnapshotLocked() []steerStatePayload {
	if t.live == nil {
		return nil
	}
	return t.live.steerSnapshot()
}

// resyncForLocked builds the resync_required body for a found turn:
// "active" while it runs, the stored terminal verdict once finished —
// the retained outcome replays instead of a bare frame. Callers hold
// h.mu.
func (h *Hub) resyncForLocked(t *turn) resyncPayload {
	p := resyncPayload{Steering: t.steeringSnapshotLocked()}
	if t.terminal && t.outcome != nil {
		p.Turn = t.outcome.typ
		p.Terminal = &t.outcome.payload
	} else {
		// An active turn replays its clock and the bounded live
		// snapshot — a watcher that lost the journal still sees every
		// part produced so far, marked when content had to drop.
		p.Turn = "active"
		p.StartedAt = t.startedAt.UnixMilli()
		p.Steerable = h.steerableLocked(t)
		if t.clock != "" {
			p.Clock = t.clock
		}
		if t.promptedAt > 0 {
			at := t.promptedAt
			p.PromptedAt = &at
		}
		if t.lastUpdateAt > 0 {
			at := t.lastUpdateAt
			p.LastUpdateAt = &at
		}
		if t.live != nil {
			p.Parts = append([]*livePart{}, t.live.parts...)
			p.Dropped = t.live.dropped
			p.Phase = t.live.phase
			boundSnapshot(&p)
		}
	}
	return p
}

// boundSnapshot keeps the resync frame under the outbound cap. The
// doc's byte accounting already covers card payloads, but it is an
// estimate — the marshaled size of the parts is the last honest bound:
// drop the oldest until the snapshot fits, counting each release so the
// client states recovery was partial.
func boundSnapshot(p *resyncPayload) {
	// Envelope, steering snapshot, and terminal headroom — the payload
	// budget stays well under maxFrameBytes.
	const budget = maxFrameBytes - 32<<10
	sizes := make([]int, len(p.Parts))
	total := 0
	for i, part := range p.Parts {
		raw, err := json.Marshal(part)
		if err != nil {
			raw = []byte{'{', '}'}
		}
		sizes[i] = len(raw)
		total += sizes[i]
	}
	for total > budget && len(p.Parts) > 0 {
		total -= sizes[0]
		p.Dropped += sizes[0]
		p.Parts = p.Parts[1:]
		sizes = sizes[1:]
	}
}

// turnSteer submits one input into the exact send turn's mailbox. The
// command's requestId becomes the input's inputId. The hub keeps no mailbox:
// the input goes to the worker that owns the turn — by the turn id, so it
// lands on this turn whether it is queued or already running — and the ack
// journals that acceptance. The turn's own state decides the rest: claimed
// before the next model call folds it, and text still waiting when the turn
// answers becomes the next turn.
func (h *Hub) turnSteer(c *conn, cmd command) {
	if cmd.AfterSeq != nil {
		c.push(errorFrame(cmd.RequestID, "invalid_argument", "turn.steer accepts no afterSeq"))
		return
	}
	if len(cmd.RequestID) > maxControlRequestIDBytes {
		c.push(errorFrame(cmd.RequestID, "invalid_argument", "turn.steer requestId exceeds the control bound"))
		return
	}
	convID, ok := commandConvID(c, cmd)
	if !ok {
		return
	}
	if valueobject.ValidID(cmd.TurnID) != nil {
		c.push(errorFrame(cmd.RequestID, "invalid_argument", "turn.steer requires a valid turnId"))
		return
	}
	var payload messageInputPayload
	if cmd.Payload == nil || !decodeStrict(cmd.Payload, &payload) {
		c.push(errorFrame(cmd.RequestID, "invalid_argument", "turn.steer payload is invalid"))
		return
	}
	// A recording folds into its own message's context; attaching one to
	// a steer would bind it to whichever messages the fold produced.
	if payload.RecordingID != "" {
		c.push(errorFrame(cmd.RequestID, "invalid_argument", "a recording belongs to a new message; stop the turn and send it"))
		return
	}
	// Input resolution may read uploaded files — keep it outside h.mu.
	in, ok := h.resolveMessageInput(c, cmd, convID, payload)
	if !ok {
		return
	}
	if in.Empty() {
		c.push(errorFrame(cmd.RequestID, "invalid_argument", "turn.steer requires text or attachments"))
		return
	}
	fingerprint := steerFingerprint(payload)
	h.mu.Lock()
	t, refusal, done := h.steerTargetLocked(convID, cmd.TurnID)
	if done {
		h.mu.Unlock()
		c.push(refusal)
		return
	}
	if prev, dup := t.live.steerInput(cmd.RequestID); dup {
		h.mu.Unlock()
		h.answerSteerDup(c, cmd, convID, t.id, prev, fingerprint)
		return
	}
	if len(t.live.steers) >= maxSteerInputs {
		h.mu.Unlock()
		c.push(errorFrame(cmd.RequestID, "queue_full", "turn steering submission bound reached"))
		return
	}
	// Reserve the id before the forward so a concurrent retry cannot
	// submit the same input twice; the entry is dropped if the worker
	// refuses it.
	entry, _ := t.live.acceptSteer(cmd.RequestID, fingerprint)
	turnID := t.id
	steerJob := h.jobControlSteer
	h.mu.Unlock()

	// The input remembers which request submitted it: the user message the
	// fold writes carries the id, so a reloaded history and the journal's live
	// bubble name the same submission.
	in.InputID = cmd.RequestID
	receipt, accepted := steerJob(convID, turnID, in)

	h.mu.Lock()
	t, found := h.turns[turnID]
	gone := !found || t.terminal || t.conversationID != convID
	h.mu.Unlock()

	if gone {
		if accepted {
			if recall := h.jobControlRecall; recall != nil {
				recall(convID, turnID, cmd.RequestID)
			}
		}
		if entry != nil {
			h.forgetSteer(turnID, cmd.RequestID)
		}
		c.push(errorFrame(cmd.RequestID, "not_found", "no live turn with that id"))
		return
	}
	if !accepted || receipt == nil {
		h.forgetSteer(turnID, cmd.RequestID)
		c.push(errorFrame(cmd.RequestID, "invalid_argument", "that turn does not take steering"))
		return
	}
	h.mu.Lock()
	if t = h.turns[turnID]; t == nil || t.terminal {
		h.mu.Unlock()
		if recall := h.jobControlRecall; recall != nil {
			recall(convID, turnID, cmd.RequestID)
		}
		h.forgetSteer(turnID, cmd.RequestID)
		c.push(errorFrame(cmd.RequestID, "not_found", "no live turn with that id"))
		return
	}
	h.emitLocked(t, "turn.steer_ack", steerInputPayload{InputID: cmd.RequestID}, false)
	// The journaled ack reaches subscribers only — an issuer that never
	// watched the turn would hear silence for an accepted submission.
	// Answer it directly; a subscribed issuer already has the sequenced
	// copy.
	if _, subscribed := t.subscribed[c]; !subscribed {
		c.push(steerAckFrame(cmd.RequestID, convID, turnID, cmd.RequestID))
	}
	h.mu.Unlock()
}

// steerTargetLocked resolves the turn a steering command names: a live send
// turn of this room that the worker can still reach. Callers hold h.mu.
func (h *Hub) steerTargetLocked(convID, turnID string) (*turn, serverFrame, bool) {
	t, found := h.turns[turnID]
	if !found || t.terminal || t.conversationID != convID {
		return nil, errorFrame("", "not_found", "no live turn with that id"), true
	}
	if t.kind != turnSend || h.jobControlSteer == nil {
		return nil, errorFrame("", "invalid_argument", "that turn does not take steering"), true
	}
	return t, serverFrame{}, false
}

// forgetSteer drops a reservation the worker never took, so the id is free
// again and the snapshot does not claim a submission that never happened.
func (h *Hub) forgetSteer(turnID, inputID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	t := h.turns[turnID]
	if t == nil || t.live == nil {
		return
	}
	for i, s := range t.live.steers {
		if s.InputID == inputID {
			t.live.steers = append(t.live.steers[:i], t.live.steers[i+1:]...)
			return
		}
	}
}

// answerSteerDup answers a retried submission from what the turn already
// accepted: the same input re-acks without a second submission, a different
// input under the same id is refused, and a recalled input is gone.
func (h *Hub) answerSteerDup(c *conn, cmd command, convID, turnID string, prev *steerInput, fingerprint uint64) {
	if prev.fingerprint != fingerprint {
		c.push(errorFrame(cmd.RequestID, "invalid_argument", "steering requestId reused with different input"))
		return
	}
	if prev.State == string(appjob.SteeringRecalled) {
		c.push(errorFrame(cmd.RequestID, "gone", "steering input was recalled"))
		return
	}
	// Idempotent re-ack — the verdict is already journaled; the retry is a
	// reply, not new turn state. Re-journaling each one would let a retrying
	// client burn the bounded journal and force honest watchers into resync.
	c.push(steerAckFrame(cmd.RequestID, convID, turnID, prev.InputID))
}

// turnSteerRecall withdraws one exact submitted input while it is still
// pending — claimed or persisted input answers not_found and is never
// restored, while a withdrawn input re-emits the journaled
// turn.steer_recalled so a lost ack reconciles idempotently. The worker owns
// the input; the hub only journals the answer.
func (h *Hub) turnSteerRecall(c *conn, cmd command) {
	if cmd.AfterSeq != nil {
		c.push(errorFrame(cmd.RequestID, "invalid_argument", "turn.steer_recall accepts no afterSeq"))
		return
	}
	if len(cmd.RequestID) > maxControlRequestIDBytes {
		c.push(errorFrame(cmd.RequestID, "invalid_argument", "turn.steer_recall requestId exceeds the control bound"))
		return
	}
	convID, ok := commandConvID(c, cmd)
	if !ok {
		return
	}
	if valueobject.ValidID(cmd.TurnID) != nil {
		c.push(errorFrame(cmd.RequestID, "invalid_argument", "turn.steer_recall requires a valid turnId"))
		return
	}
	var payload steerRecallCommandPayload
	if cmd.Payload == nil || !decodeStrict(cmd.Payload, &payload) || strings.TrimSpace(payload.InputID) == "" {
		c.push(errorFrame(cmd.RequestID, "invalid_argument", "turn.steer_recall requires a payload.inputId"))
		return
	}
	h.mu.Lock()
	t, refusal, done := h.steerTargetLocked(convID, cmd.TurnID)
	if done {
		h.mu.Unlock()
		refusal.RequestID = cmd.RequestID
		c.push(refusal)
		return
	}
	recall := h.jobControlRecall
	if recall == nil {
		h.mu.Unlock()
		c.push(errorFrame(cmd.RequestID, "invalid_argument", "that turn does not take steering"))
		return
	}
	if prev, known := t.live.steerInput(payload.InputID); known && prev.State == string(appjob.SteeringRecalled) {
		h.mu.Unlock()
		// Withdrawn already — dedupe the success idempotently: the recall
		// journals once; retries answer the issuer only.
		c.push(steerRecalledFrame(cmd.RequestID, convID, cmd.TurnID, payload.InputID))
		return
	} else if !known {
		h.mu.Unlock()
		// This turn never accepted that id: nothing to withdraw, and the
		// worker is not asked about an input the journal never saw.
		c.push(errorFrame(cmd.RequestID, "not_found", "no steering input with that id"))
		return
	}
	turnID := t.id
	h.mu.Unlock()

	if !recall(convID, turnID, payload.InputID) {
		c.push(errorFrame(cmd.RequestID, "not_found", "steering input already consumed"))
		return
	}

	h.mu.Lock()
	t, found := h.turns[turnID]
	if !found || t.terminal || t.conversationID != convID {
		h.mu.Unlock()
		c.push(errorFrame(cmd.RequestID, "not_found", "no live turn with that id"))
		return
	}
	t.live.steerState(payload.InputID, string(appjob.SteeringRecalled))
	h.emitLocked(t, "turn.steer_recalled", steerInputPayload{InputID: payload.InputID}, false)
	if _, subscribed := t.subscribed[c]; !subscribed {
		c.push(steerRecalledFrame(cmd.RequestID, convID, turnID, payload.InputID))
	}
	h.mu.Unlock()
}
