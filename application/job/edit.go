package job

import (
	"context"
	"errors"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	"github.com/jahrulnr/nusashell/infrastructure/prompt"
)

// Edit rewrites one user message and regenerates the turn from it —
// everything recorded after the target is dropped. It preempts: the
// running job is interrupted and the edit cuts ahead of queued sends.
// 400 for a non-user target, 404 when the id exists nowhere in history —
// a sealed target is repromoted by the repository.
type Edit struct {
	Base
	Deps    Deps
	Ctx     context.Context
	MsgID   string
	Content string
	// ProviderID/ModelID/Opts are the caller's per-turn selections — the same
	// override contract as Send: "" keeps the room's stored binding, a
	// successful regenerated turn re-binds the overrides to the room.
	// Opts.Environment and Opts.Steering are send-only: the room's bound
	// environment always applies, and steering rides the job's mailbox.
	ProviderID string
	ModelID    string
	Opts       SendOptions
	OnEvent    event.TurnEventHandler

	steers SteeringInput
}

// Steer buffers user input for the regenerated turn — same contract as
// Send.Steer.
func (j *Edit) Steer(text string) { j.steers.push(text) }

func (j *Edit) SteerInput(in valueobject.UserInput, record *entity.Message) (any, bool) {
	r, err := j.steers.SubmitBefore(in, j.Deps.Config.MailboxCap, record)
	if err != nil {
		return nil, false
	}
	return r, true
}

// RecallInput withdraws the pending input submitted under inputID.
func (j *Edit) RecallInput(inputID string) bool { return j.steers.RecallID(inputID) }

// SteerInputAvailable reports whether the mailbox has room right now.
func (j *Edit) SteerInputAvailable() bool { return j.steers.available(j.Deps.Config.MailboxCap) }

// RecallSteer withdraws the newest un-fired steering input.
func (j *Edit) RecallSteer() (string, bool) { return j.steers.popLast() }

// IsPreempt marks edit as a control job — it cuts the queue and
// interrupts whatever is running.
func (j *Edit) IsPreempt() bool { return true }

// Execute applies the rewrite on the worker's goroutine, then resends.
// IsTurn marks this job as a turn: a runtime pick must not be applied under it,
// because a turn fixes its runtime when it starts.
func (*Edit) IsTurn() bool { return true }

func (j *Edit) Execute(execCtx context.Context) (err error) {
	defer j.finish(&err)
	ctx := j.Cancellable(execCtx, j.Ctx)
	d := j.Deps
	convID := j.ConvID()
	applied := false
	recorded := false
	var consumedAt time.Time
	// The room is claimed for the whole turn, like every other turn job: a pick
	// taken now waits for the next one, and a surface reads the room as working.
	if d.Turns != nil {
		d.Turns.BeginTurn(convID)
		defer d.Turns.EndTurn(convID)
	}
	// A turn no socket admitted still has an audience — its room.
	onEvent, sealLive := liveTurn(d, convID, "edit", j.Opts.Origin, j.OnEvent)
	defer func() { sealLive(false, err) }()
	// Released after the closure returned nil only — after the turn's save — so
	// a failed save leaves both the file and the projection honest.
	defer func() {
		if err == nil {
			clearConsumedPending(ctx, d, convID, consumedAt, applied)
		}
	}()
	err = func() error {
		if err := ctx.Err(); err != nil {
			return apperror.New(408, "edit canceled", err)
		}
		conv, err := d.Convs.Load(ctx, convID)
		if err != nil {
			return err // 404 — a room that isn't there has nothing to edit
		}
		// The session the agent holds for the room goes with the rewrite;
		// remember it to remove once the agent has a new one.
		prevAgent, prevSession := conv.AgentID(), conv.AgentSession()
		sealed := false
		// The evidence is read before the rewrite drops the tail and handed
		// over before the rewrite runs: RewriteFrom applies the same refusals
		// as EditCapture, so a recorded edit always applies.
		if d.EditEvidence != nil {
			if previous, tail, ok := conv.EditCapture(j.MsgID); ok {
				d.EditEvidence.RecordEdit(convID, j.MsgID, previous, tail)
				recorded = true
			}
		}
		switch err := conv.RewriteFrom(j.MsgID, j.Content); {
		case errors.Is(err, aggregate.ErrNotUserMessage):
			return apperror.New(400, "only user messages are editable", err)
		case errors.Is(err, aggregate.ErrMessageNotFound):
			// Not in the live tail — maybe sealed. Validate routing
			// before repromotion so a dead provider never truncates history.
			sealed = true
		case err != nil:
			return err
		}

		// Same embedded-prompt contract as send: applied per execution,
		// never persisted — an edit turn runs under the same rules.
		if conv.SystemPrompt() == "" {
			conv.SetSystemPrompt(prompt.SystemPrompt("conversation"))
		}
		driver, err := d.environment(conv)
		if err != nil {
			return err
		}
		// An edit/regeneration is a turn: the pick parked for the next turn is
		// applied here, before anything reads the room's runtime.
		consumedAt, applied, err = applyPendingSelection(ctx, d, conv, onEvent)
		if err != nil {
			return err
		}
		// Refused before the rewrite below is stored, the same rule Send applies.
		ex, err := executorFor(conv.Runtime())
		if err != nil {
			return err
		}
		if err := ex.refuse(&j.Opts.Effort, &j.Opts.Upstream); err != nil {
			return err
		}
		return ex.edit(ctx, d, j, conv, driver, sealed, prevAgent, prevSession, onEvent)
	}()
	if err != nil && recorded {
		d.EditEvidence.DiscardEdit(convID, j.MsgID)
	}
	return err
}
