package job

import (
	"context"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	"github.com/jahrulnr/nusashell/infrastructure/prompt"
)

// Resume continues a conversation's turn from its current history —
// unanswered tool calls finish first, then the model is called again.
// Unlike Edit it never rewrites or truncates: prior tool results and
// partial replies stay so a retry does not erase work the failed turn
// already paid for. It preempts: the running job is interrupted and the
// resume cuts ahead of queued sends.
//
// ProviderID/ModelID/Opts are the same per-turn overrides Send takes — ""
// keeps the room's stored binding, and a successful resume re-binds
// them. Opts.Environment and Opts.Steering are send-only.
type Resume struct {
	Base
	Deps       Deps
	Ctx        context.Context
	ProviderID string
	ModelID    string
	Opts       SendOptions
	OnEvent    event.TurnEventHandler

	steers SteeringInput
}

// Steer buffers user input for the resumed turn — same contract as
// Send.Steer.
func (j *Resume) Steer(text string) { j.steers.push(text) }

func (j *Resume) SteerInput(in valueobject.UserInput, record *entity.Message) (any, bool) {
	r, err := j.steers.SubmitBefore(in, j.Deps.Config.MailboxCap, record)
	if err != nil {
		return nil, false
	}
	return r, true
}

// RecallInput withdraws the pending input submitted under inputID.
func (j *Resume) RecallInput(inputID string) bool { return j.steers.RecallID(inputID) }

// SteerInputAvailable reports whether the mailbox has room right now.
func (j *Resume) SteerInputAvailable() bool { return j.steers.available(j.Deps.Config.MailboxCap) }

// RecallSteer withdraws the newest un-fired steering input.
func (j *Resume) RecallSteer() (string, bool) { return j.steers.popLast() }

// IsPreempt marks resume as a control job — it cuts the queue and
// interrupts whatever is running.
func (j *Resume) IsPreempt() bool { return true }

// Execute continues the turn on the worker's goroutine.
// IsTurn marks this job as a turn: a runtime pick must not be applied under it,
// because a turn fixes its runtime when it starts.
func (*Resume) IsTurn() bool { return true }

func (j *Resume) Execute(execCtx context.Context) (err error) {
	defer j.finish(&err)
	ctx := j.Cancellable(execCtx, j.Ctx)
	d := j.Deps
	convID := j.ConvID()
	applied := false
	var consumedAt time.Time
	// The room is claimed for the whole turn, like every other turn job: a pick
	// taken now waits for the next one, and a surface reads the room as working.
	if d.Turns != nil {
		d.Turns.BeginTurn(convID)
		defer d.Turns.EndTurn(convID)
	}
	// A turn no socket admitted still has an audience — its room.
	onEvent, sealLive := liveTurn(d, convID, "resume", j.Opts.Origin, j.OnEvent)
	defer func() { sealLive(false, err) }()
	// Released after the closure returned nil only — i.e. after the turn's save —
	// so a failed save leaves both the file and the projection honest.
	defer func() {
		if err == nil {
			clearConsumedPending(ctx, d, convID, consumedAt, applied)
		}
	}()
	err = func() error {
		if err := ctx.Err(); err != nil {
			return apperror.New(408, "resume canceled", err)
		}
		conv, err := d.Convs.Load(ctx, convID)
		if err != nil {
			return err // 404 — nothing to resume
		}
		if len(conv.Messages()) == 0 {
			return apperror.New(400, "nothing to resume", nil)
		}
		// A retry is a turn: the pick parked for the next turn belongs to it,
		// applied before anything reads the room's runtime.
		consumedAt, applied, err = applyPendingSelection(ctx, d, conv, onEvent)
		if err != nil {
			return err
		}
		if conv.SystemPrompt() == "" {
			conv.SetSystemPrompt(prompt.SystemPrompt("conversation"))
		}
		driver, err := d.environment(conv)
		if err != nil {
			return err
		}
		if _, err := d.applyProject(ctx, conv); err != nil {
			return err
		}
		ex, err := executorFor(conv.Runtime())
		if err != nil {
			return err
		}
		if err := ex.refuse(&j.Opts.Effort, &j.Opts.Upstream); err != nil {
			return err
		}
		return ex.resume(ctx, d, j, conv, driver, onEvent)
	}()
	return err
}
