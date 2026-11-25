package job

import (
	"context"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
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
		if conv.Runtime() == enum.RuntimeAgent {
			// The agent keeps its own record of the dialogue, so only a
			// turn that never got a reply can be handed to it again.
			last := conv.Last()
			if last.Role() != enum.RoleUser || !last.Editable() {
				return apperror.New(409, "nothing to retry — send a new message to the agent", nil)
			}
			if err := d.runAgentTurn(ctx, convID, conv, driver, *last, j.OnEvent, &j.steers, j.Opts.Origin); err != nil {
				return err
			}
			return d.Convs.Save(ctx, conv)
		}
		prov, model, svc, err := d.resolveProvider(ctx, convID, conv, driver, j.ProviderID, j.ModelID)
		if err != nil {
			return err
		}
		providerID, modelID := prov.ID(), model.ID()
		useSelection(&model, conv, j.Opts)
		if j.Opts.Workspace != "" {
			if driver.Workspace() == nil {
				return apperror.New(500, "environment workspace unavailable", nil)
			}
			root, err := driver.Workspace().CheckRoot(j.Opts.Workspace)
			if err != nil {
				return apperror.New(400, "invalid conversation workspace", err)
			}
			conv.SetWorkspace(root)
		}
		extra, err := d.applyProject(ctx, conv)
		if err != nil {
			return err
		}
		// File tools reach the project's other folders by absolute path.
		ctx = dservice.WithExtraFolders(ctx, extra)
		if err := d.runTurn(ctx, convID, conv, driver, prov, model, svc, j.OnEvent, &j.steers, j.Opts.Origin); err != nil {
			return err
		}
		conv.SetProviderID(providerID)
		conv.SetModelID(modelID)
		rememberSelection(conv, j.Opts)
		return d.Convs.Save(ctx, conv)
	}()
	return err
}
