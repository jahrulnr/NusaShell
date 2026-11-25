package appconversation

import (
	"context"
	"fmt"
	"slices"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"

	appjob "github.com/jahrulnr/nusashell/application/job"
	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Info returns the lightweight projection for one conversation.
func (app *ConversationApp) Info(ctx context.Context, id string) (drepo.ConversationInfo, error) {
	deps := app.depsSnapshot()
	return deps.Convs.Info(ctx, id)
}

// CanCompact reports whether manual compaction would fold new live history.
func (app *ConversationApp) CanCompact(ctx context.Context, id string) (bool, error) {
	deps := app.depsSnapshot()
	conv, err := deps.Convs.Load(ctx, id)
	if err != nil {
		return false, err
	}
	return conv.CanManualCompact(deps.Config.CompactKeepTail), nil
}

// List returns conversation projections ordered by recent activity.
func (app *ConversationApp) List(ctx context.Context) ([]drepo.ConversationInfo, error) {
	deps := app.depsSnapshot()
	return deps.Convs.List(ctx)
}

// History returns a ranged slice of the conversation's global message history.
func (app *ConversationApp) History(ctx context.Context, id string, start, end int) ([]entity.Message, error) {
	deps := app.depsSnapshot()
	return deps.Convs.LoadAt(ctx, id, start, end)
}

// ToolSchemas resolves the catalog for a persisted room. An empty or
// not-yet-persisted room uses the local environment's starting catalog.
func (app *ConversationApp) ToolSchemas(ctx context.Context, convID string) ([]valueobject.ToolSchema, error) {
	deps := app.depsSnapshot()
	kind := aggregate.DefaultEnvironment
	if convID != "" {
		conv, err := deps.Convs.Load(ctx, convID)
		if err == nil {
			kind = conv.Environment()
		} else if apperror.As(err).StatusCode() != apperror.StatusNotFound {
			return nil, err
		}
	}
	driver := deps.Environments[kind]
	if driver == nil {
		return nil, apperror.New(400, "conversation environment unavailable", nil)
	}
	return driver.ToolSchemas(), nil
}

// Compact folds the live tail into a checkpoint on the room's worker —
// queued like a send so a running turn finishes first.
func (app *ConversationApp) Compact(ctx context.Context, convID string) error {
	res := make(chan error, 1)
	j := &appjob.Compact{Ctx: ctx}
	j.Result = res
	app.post(convID, j)
	select {
	case err := <-res:
		return err
	case <-ctx.Done():
		return apperror.New(408, "compact request canceled", ctx.Err())
	}
}

// Annotation is a partial change to a room's own metadata; a nil field
// keeps its value.
type Annotation struct {
	Title *string
	// IfTitle makes the title change conditional: it applies only while
	// the room still has this title, else 409.
	IfTitle   *string
	ProjectID *string // "" takes the room out of its project
	// FromProjectID makes the project move conditional: it applies only
	// while the room is in this project ("" = none), else 409.
	FromProjectID *string
	Pinned        *bool
	// MemoryOff keeps memory out of the room (true) or lets it back.
	MemoryOff *bool
	Label     *string // "" clears the @handle
	// Routing overrides where replies go; ResetRouting returns to default.
	Routing      *valueobject.Routing
	ResetRouting bool
	// AgentOption is {id, value} for one of the bound agent's options.
	AgentOption *[2]string
	// Runtime rebinds the room without running a turn: the picker's
	// choice, applied at once so every ingress — a chat, an automation,
	// another browser — runs what the owner selected.
	Runtime *RuntimeBinding
	// Effort and Upstream are the picker's reasoning effort and aggregator
	// pin, kept on the room for the same reason.
	Effort   *enum.Effort
	Upstream *string
}

// RuntimeBinding is the picker's selection. It is the job's type: the
// application passes it through untouched.
type RuntimeBinding = appjob.RuntimeBinding

// Annotate applies an Annotation. The write is queued on the room's worker
// so it cannot race a turn's metadata saves. A room that actually changed is
// announced — the same notice a title or a fork sends — so every open tab
// re-reads it instead of keeping the record it loaded.
func (app *ConversationApp) Annotate(ctx context.Context, convID string, a Annotation) error {
	res := make(chan error, 1)
	j := &appjob.Annotate{Ctx: ctx, Title: a.Title, IfTitle: a.IfTitle, ProjectID: a.ProjectID, FromProjectID: a.FromProjectID, Pinned: a.Pinned, MemoryOff: a.MemoryOff,
		Label: a.Label, Routing: a.Routing, ResetRouting: a.ResetRouting, AgentOption: a.AgentOption, Runtime: a.Runtime, Effort: a.Effort, Upstream: a.Upstream}
	j.Result = res
	app.post(convID, j)
	select {
	case err := <-res:
		if err != nil {
			return err
		}
		app.notifyChanged(convID)
		return nil
	case <-ctx.Done():
		return apperror.New(408, "update request canceled", ctx.Err())
	}
}

// AgentOptions lists the session options of the room's agent, with the
// owner's picks as their current values — a stored pick the option no longer
// offers is not one it can call current.
func (app *ConversationApp) AgentOptions(ctx context.Context, convID string) ([]dservice.AgentOption, error) {
	deps := app.depsSnapshot()
	info, err := deps.Convs.Info(ctx, convID)
	if err != nil {
		return nil, err
	}
	opts, err := app.optionReport(ctx, deps, info)
	if err != nil {
		return nil, err
	}
	if opts == nil {
		return []dservice.AgentOption{}, nil
	}
	for i, o := range opts {
		// A stored value this option does not offer is not a current one: the
		// session would refuse it, and the picker must not draw it as applied.
		if v, ok := info.AgentOptions.Value(o.ID); ok && o.Offers(v) {
			opts[i].Current = v
		}
	}
	return opts, nil
}

// optionReport reads the room's option catalog — the report the picker draws:
// the live session's when one runs, the agent's own defaults otherwise. It
// answers nil, nil when the room runs no agent, and 503 when a room that does
// cannot be asked: a caller must not treat "cannot tell" as "anything goes".
func (app *ConversationApp) optionReport(ctx context.Context, deps appjob.Deps, info drepo.ConversationInfo) ([]dservice.AgentOption, error) {
	if info.AgentID == "" {
		return nil, nil
	}
	if deps.AgentRuntime == nil || deps.Agents == nil {
		return nil, apperror.New(503, "agent options are unavailable", nil)
	}
	agent, err := deps.Agents.Load(ctx, info.AgentID)
	if err != nil {
		return nil, err
	}
	return deps.AgentRuntime.Options(ctx, agent, info.ID)
}

// SetAgentOption validates the owner's pick against the agent's catalog,
// applies it to the room's live session, and records it only once the agent
// takes it. Two paths can refuse it and both must: a value the agent rejects
// would make every later turn re-apply it and fail, and a room with no live
// session — every room after a restart — has nothing to apply the pick to, so
// the catalog is the only thing standing between the store and a value the
// agent never took.
func (app *ConversationApp) SetAgentOption(ctx context.Context, convID, id, value string) error {
	deps := app.depsSnapshot()
	if deps.AgentRuntime != nil && value != "" {
		if err := deps.AgentRuntime.SetOption(ctx, convID, id, value); err != nil {
			return err
		}
	}
	if value != "" {
		info, err := deps.Convs.Info(ctx, convID)
		if err != nil {
			return err
		}
		opts, err := app.optionReport(ctx, deps, info)
		if err != nil {
			return err
		}
		if opts != nil {
			i := slices.IndexFunc(opts, func(o dservice.AgentOption) bool { return o.ID == id })
			if i < 0 {
				return apperror.New(400, fmt.Sprintf("the agent offers no option %q", id), nil)
			}
			if !opts[i].Offers(value) {
				return apperror.New(400, fmt.Sprintf("option %q does not offer %q", id, value), nil)
			}
		}
	}
	return app.Annotate(ctx, convID, Annotation{AgentOption: &[2]string{id, value}})
}

// AgentCommands lists the slash commands the room's agent session offers.
func (app *ConversationApp) AgentCommands(convID string) []dservice.AgentCommand {
	deps := app.depsSnapshot()
	if deps.AgentRuntime == nil {
		return []dservice.AgentCommand{}
	}
	return deps.AgentRuntime.Commands(convID)
}
