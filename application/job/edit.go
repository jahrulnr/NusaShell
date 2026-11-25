package job

import (
	"context"
	"errors"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
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

func (j *Edit) SteerInput(in valueobject.UserInput, before func() *entity.Message) bool {
	_, err := j.steers.SubmitBefore(in, j.Deps.Config.MailboxCap, before)
	return err == nil
}

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
		if conv.Runtime() == enum.RuntimeAgent {
			// The agent forgets its session with the dropped turns and
			// answers the edited line in a fresh one, with a recap.
			if err := d.Convs.RewriteFrom(ctx, convID, j.MsgID, j.Content); err != nil {
				return err
			}
			if sealed {
				if conv, err = d.Convs.Load(ctx, convID); err != nil {
					return err
				}
				conv.ForgetAgentSession()
				// The reload starts from the record, which does not carry the
				// parked pick: applying it again is the same rule, and the file
				// is still there (it is cleared only after the turn's save).
				if _, _, err := applyPendingSelection(ctx, d, conv, onEvent); err != nil {
					return err
				}
			}
			err := d.runAgentTurn(ctx, convID, conv, driver, *conv.Last(), onEvent, &j.steers, j.Opts.Origin)
			// Even a failed turn: the room's record forgot the old session
			// when the rewrite was stored, and the turn saves the new one.
			d.retireAgentSession(convID, prevAgent, prevSession, conv)
			if err != nil {
				return err
			}
			return d.Convs.Save(ctx, conv)
		}
		prov, model, svc, err := d.resolveProvider(ctx, convID, conv, driver, j.ProviderID, j.ModelID)
		if err != nil {
			return err
		}
		providerID, modelID := prov.ID(), model.ID()
		// Per-turn selections override the catalog entry in memory —
		// the same contract Send applies, so a retry can switch the
		// model that just failed. An override workspace is validated
		// here but bound after the rewrite: the sealed reload swaps in
		// a fresh aggregate, and the room's workspace rebinds the same
		// way providerID/modelID do below.
		useSelection(&model, conv, j.Opts)
		workspace := ""
		if j.Opts.Workspace != "" {
			if driver.Workspace() == nil {
				return apperror.New(500, "environment workspace unavailable", nil)
			}
			root, err := driver.Workspace().CheckRoot(j.Opts.Workspace)
			if err != nil {
				return apperror.New(400, "invalid conversation workspace", err)
			}
			workspace = root
		}
		if err := d.Convs.RewriteFrom(ctx, convID, j.MsgID, j.Content); err != nil {
			return err
		}
		if sealed {
			conv, err = d.Convs.Load(ctx, convID)
			if err != nil {
				return err
			}
			if conv.SystemPrompt() == "" {
				conv.SetSystemPrompt(prompt.SystemPrompt("conversation"))
			}
		}
		if workspace != "" {
			conv.SetWorkspace(workspace)
		}
		extra, err := d.applyProject(ctx, conv)
		if err != nil {
			return err
		}
		// File tools reach the project's other folders by absolute path.
		ctx = dservice.WithExtraFolders(ctx, extra)
		if err := d.runTurn(ctx, convID, conv, driver, prov, model, svc, onEvent, &j.steers, j.Opts.Origin); err != nil {
			return err
		}
		conv.SetProviderID(providerID) // existing rooms re-bind only on success —
		conv.SetModelID(modelID)       // a failed override keeps the old binding
		rememberSelection(conv, j.Opts)
		return d.Convs.Save(ctx, conv)
	}()
	return err
}
