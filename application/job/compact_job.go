package job

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	repo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Compact folds the live tail into a checkpoint on demand — the manual
// counterpart to the budget-triggered path in send. It queues like a
// send (never preempts): folding while a turn is mid-flight would race
// the mutations the worker is writing.
type Compact struct {
	Base
	Deps Deps
	Ctx  context.Context
}

// Execute runs the checkpoint dialogue on the worker's goroutine.
func (j *Compact) Execute(execCtx context.Context) (err error) {
	defer j.finish(&err)
	ctx := j.Cancellable(execCtx, j.Ctx)
	d := j.Deps
	convID := j.ConvID()
	// The room is claimed while the fold runs, so a runtime pick taken now is
	// answered (it waits for the next turn) instead of queuing behind a job that
	// can take minutes. Nothing about the fold's own runtime changes.
	//
	// A fold is not a turn, so it does NOT apply a parked pick: the pick belongs
	// to the room's next turn, never to the model that writes the summary.
	if d.Turns != nil {
		d.Turns.BeginTurn(convID)
		defer d.Turns.EndTurn(convID)
	}
	err = func() error {
		if err := ctx.Err(); err != nil {
			return apperror.New(408, "compaction canceled", err)
		}
		conv, err := d.Convs.Load(ctx, convID)
		if err != nil {
			return err // 404 — no room, nothing to fold
		}
		keepTail := d.Config.CompactKeepTail
		if !conv.Runtime().HarnessOwnsContext() {
			return apperror.New(409, "agent conversations manage their own context", nil)
		}
		if !conv.CanManualCompact(keepTail) {
			return apperror.New(400, "nothing to compact",
				fmt.Errorf("conversation %s has no new foldable history (messages %d, keep-tail %d)", convID, len(conv.Messages()), keepTail))
		}
		driver, err := d.environment(conv)
		if err != nil {
			return err
		}
		prov, model, svc, err := d.resolveProvider(ctx, convID, conv, driver, "", "")
		if err != nil {
			return err
		}
		return d.compact(ctx, convID, conv, prov, model.ApplyUnknownContextDefaults(), svc, keepTail, nil)
	}()
	return err
}

// Annotate changes a room's own metadata — title, project, pin — as a
// queued job, so it cannot race the metadata a running turn saves. A nil
// field keeps its value; an empty ProjectID takes the room out of its
// project.
type Annotate struct {
	Base
	Deps  Deps
	Ctx   context.Context
	Title *string
	// IfTitle, when set, makes the title change a compare-and-set: a room
	// whose title is no longer this (the owner renamed it) is refused
	// with 409.
	IfTitle   *string
	ProjectID *string
	// FromProjectID, when set, makes the project move a compare-and-set:
	// a room no longer in that project ("" = none) is refused with 409.
	FromProjectID *string
	Pinned        *bool
	MemoryOff     *bool
	// Label sets the room's @handle ("" clears); it must be free.
	Label *string
	// Routing overrides where replies go; ResetRouting returns the room
	// to the server default.
	Routing      *valueobject.Routing
	ResetRouting bool
	// AgentOption sets one of the bound agent's session options; an empty
	// value returns it to the agent's default.
	AgentOption *[2]string
	// Runtime rebinds the room without running a turn — what the model
	// picker does.
	Runtime *RuntimeBinding
	// Effort and Upstream are the picker's reasoning effort and aggregator
	// pin for the room; "default" and "" return them to the model's and the
	// provider's own.
	Effort   *enum.Effort
	Upstream *string
}

// RuntimeBinding is the picker's selection applied to a room on its own,
// without a turn. The value object refuses an empty, partial, or mixed
// target, so a caller cannot assemble one by filling fields.
type RuntimeBinding = valueobject.RuntimeBinding

// BindRuntime applies a binding the constructors already made whole. The
// kind says which existence check and which room method run. It is the
// one applier: a parked pick is validated through it when it arrives and
// applied through it when a turn starts.
func BindRuntime(ctx context.Context, d Deps, conv *aggregate.Conversation, b RuntimeBinding) error {
	if b.IsZero() {
		return apperror.New(400, "a runtime binding needs an agentId, or a providerId and modelId", nil)
	}
	if b.Kind().RequiresLocalExecution() {
		if d.Agents == nil {
			return apperror.New(503, "agents unavailable", nil)
		}
		if _, err := d.Agents.Load(ctx, b.AgentID()); err != nil {
			if apperror.As(err).StatusCode() == apperror.StatusNotFound {
				return apperror.New(400, "unknown agent", err)
			}
			return err
		}
		if err := conv.BindAgent(b.AgentID()); err != nil {
			if errors.Is(err, aggregate.ErrAgentNeedsLocal) {
				return apperror.New(400, err.Error(), err)
			}
			return apperror.New(400, "invalid agent", err)
		}
		if b.AgentModel() != "" {
			conv.SetModelID(b.AgentModel())
		}
		return nil
	}
	if b.Kind().UsesProviderRoute() {
		if d.Providers == nil {
			return apperror.New(503, "providers unavailable", nil)
		}
		prov, err := d.Providers.Load(ctx, b.ProviderID())
		if err != nil {
			if apperror.As(err).StatusCode() == apperror.StatusNotFound {
				return apperror.New(400, "unknown provider", err)
			}
			return err
		}
		if _, ok := prov.Model(b.ModelID()); !ok {
			return apperror.New(400, "unknown model",
				fmt.Errorf("provider %s catalog has no model %s", b.ProviderID(), b.ModelID()))
		}
		conv.UnbindAgent()
		conv.SetProviderID(b.ProviderID())
		conv.SetModelID(b.ModelID())
		return nil
	}
	return apperror.New(500, "unknown runtime", fmt.Errorf("%s", b.Kind()))
}

// Execute applies the changes on the worker's goroutine.
func (j *Annotate) Execute(execCtx context.Context) (err error) {
	defer j.finish(&err)
	ctx := j.Cancellable(execCtx, j.Ctx)
	d := j.Deps
	err = func() error {
		if err := ctx.Err(); err != nil {
			return apperror.New(408, "update canceled", err)
		}
		conv, err := d.Convs.Load(ctx, j.ConvID())
		if err != nil {
			return err
		}
		if j.Title != nil {
			if j.IfTitle != nil && conv.Title() != *j.IfTitle {
				return apperror.New(409, "the thread was renamed meanwhile", nil)
			}
			conv.SetTitle(*j.Title)
		}
		if j.ProjectID != nil {
			if j.FromProjectID != nil && conv.ProjectID() != *j.FromProjectID {
				return apperror.New(409, "the thread moved to another project", nil)
			}
			if *j.ProjectID != "" {
				if d.Projects == nil {
					return apperror.New(503, "projects unavailable", nil)
				}
				if _, err := d.Projects.Get(ctx, *j.ProjectID); err != nil {
					if apperror.As(err).StatusCode() == apperror.StatusNotFound {
						return apperror.New(400, "unknown project", err)
					}
					return err
				}
			}
			if err := conv.SetProject(*j.ProjectID); err != nil {
				return apperror.New(400, "invalid project", err)
			}
		}
		if j.Pinned != nil {
			conv.SetPinned(*j.Pinned)
		}
		if j.MemoryOff != nil {
			conv.SetMemoryOff(*j.MemoryOff)
		}
		if j.Label != nil {
			label, err := d.checkLabel(ctx, conv.ID(), *j.Label)
			if err != nil {
				return err
			}
			if err := conv.SetLabel(label); err != nil {
				return apperror.New(400, "invalid label", err)
			}
		}
		switch {
		case j.ResetRouting:
			conv.SetRouting(nil)
		case j.Routing != nil:
			conv.SetRouting(j.Routing)
		}
		if j.AgentOption != nil {
			if err := conv.SetAgentOption(j.AgentOption[0], j.AgentOption[1]); err != nil {
				return apperror.New(400, err.Error(), err)
			}
		}
		if j.Runtime != nil {
			if err := BindRuntime(ctx, d, conv, *j.Runtime); err != nil {
				return err
			}
		}
		// Effort and upstream are picks of a provider call, judged on the runtime
		// this PATCH leaves the room on: after its own rebind, so a switch to a
		// provider keeps them, and a switch to an agent refuses them. The refusal
		// comes before anything is saved, so a refused PATCH changes nothing.
		if err := AgentPickRefusal(conv.Runtime(), j.Effort, j.Upstream); err != nil {
			return err
		}
		if j.Effort != nil {
			conv.SetEffort(*j.Effort)
		}
		if j.Upstream != nil {
			conv.SetUpstream(strings.TrimSpace(*j.Upstream))
		}
		// The explicit pick just became the room's value NOW — any parked
		// older generation of the same fields is superseded by it, and only
		// those fields drop without notice. Everything else stays parked:
		// its notice at the next turn is the one channel it has.
		cover := repo.Covered{
			Runtime:  j.Runtime != nil,
			Effort:   j.Effort != nil,
			Upstream: j.Upstream != nil,
			Options:  optionKeys(j.AgentOption),
		}
		// A write that supersedes a parked pick is refused before it saves when
		// that pick cannot be read: the save would land, and the unreadable
		// generation would survive it unseen.
		if cover.Runtime || cover.Effort || cover.Upstream || len(cover.Options) > 0 {
			if _, _, err := d.Convs.PendingSelection(ctx, conv.ID()); err != nil {
				return apperror.New(500, "the room's waiting pick cannot be read, so nothing was saved", err)
			}
		}
		if err := d.Convs.Save(ctx, conv); err != nil {
			return err
		}
		if err := d.Convs.SupersedePendingSelection(ctx, conv.ID(), cover); err != nil {
			// The pick IS the room's value now — the save above landed —
			// but the parked generation survives to fold over it at the
			// next turn. The error must name that consequence: nothing
			// about this failure reads as "the pick did not apply".
			return apperror.New(500, "the pick was applied but the parked generation could not be cleared", err)
		}
		return nil
	}()
	return err
}

// optionKeys names the session-option key one pick carries, none when the
// annotation is not an option pick.
func optionKeys(opt *[2]string) []string {
	if opt == nil {
		return nil
	}
	return []string{opt[0]}
}
