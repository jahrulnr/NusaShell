package appconversation

import (
	"context"
	"fmt"
	"slices"
	"time"

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

// Annotate applies an Annotation. A runtime binding, a reasoning effort, an
// upstream pin, and an agent session-option pick are never applied now: they are
// parked for the room's next turn, because a running turn owns the record they
// would be written to and a turn's runtime is fixed when it starts. Everything
// else is queued on the room's worker as before, and a room that actually
// changed is announced — the same notice a title or a fork sends.
//
// parked reports that a pick waits for the next turn: the difference between
// "accepted, applies to the next turn" and "done", which a caller must not blur.
func (app *ConversationApp) Annotate(ctx context.Context, convID string, a Annotation) (parked bool, err error) {
	if sel, ok := pickOf(a); ok && app.running(convID) {
		if err := app.parkSelection(ctx, convID, a, sel); err != nil {
			return false, err
		}
		parked = true
		a.Runtime, a.Effort, a.Upstream, a.AgentOption = nil, nil, nil, nil
		if a.nothingElse() {
			return true, nil
		}
	}
	res := make(chan error, 1)
	j := &appjob.Annotate{Ctx: ctx, Title: a.Title, IfTitle: a.IfTitle, ProjectID: a.ProjectID, FromProjectID: a.FromProjectID, Pinned: a.Pinned, MemoryOff: a.MemoryOff,
		Label: a.Label, Routing: a.Routing, ResetRouting: a.ResetRouting, AgentOption: a.AgentOption, Runtime: a.Runtime,
		Effort: a.Effort, Upstream: a.Upstream}
	j.Result = res
	app.post(convID, j)
	select {
	case err := <-res:
		if err != nil {
			return parked, err
		}
		app.notifyChanged(convID)
		return parked, nil
	case <-ctx.Done():
		return parked, apperror.New(408, "update request canceled", ctx.Err())
	}
}

// pickOf answers the part of an annotation that waits for the next turn, if it
// carries one.
func pickOf(a Annotation) (drepo.PendingSelection, bool) {
	if a.Runtime == nil && a.Effort == nil && a.Upstream == nil && a.AgentOption == nil {
		return drepo.PendingSelection{}, false
	}
	sel := drepo.PendingSelection{}
	if a.Runtime != nil {
		sel.AgentID, sel.AgentModel = a.Runtime.AgentID, a.Runtime.AgentModel
		sel.ProviderID, sel.ModelID = a.Runtime.ProviderID, a.Runtime.ModelID
	}
	if a.Effort != nil {
		effort := *a.Effort
		sel.Effort = &effort
	}
	if a.Upstream != nil {
		upstream := *a.Upstream
		sel.Upstream = &upstream
	}
	if a.AgentOption != nil {
		sel.Options = map[string]string{a.AgentOption[0]: a.AgentOption[1]}
	}
	return sel, true
}

// nothingElse reports whether an annotation carries nothing but the pick that
// was just parked.
func (a Annotation) nothingElse() bool {
	return a.Title == nil && a.ProjectID == nil && a.Pinned == nil && a.MemoryOff == nil &&
		a.Label == nil && a.Routing == nil && !a.ResetRouting
}

// parkSelection writes the pick where the next turn will find it, validating a
// binding on the way: a deleted agent or a model the catalog dropped is refused
// while the owner is still here, not discovered at the turn.
func (app *ConversationApp) parkSelection(ctx context.Context, convID string, a Annotation, sel drepo.PendingSelection) error {
	deps := app.depsSnapshot()
	if a.Runtime != nil {
		conv, err := deps.Convs.Load(ctx, convID)
		if err != nil {
			return err
		}
		if err := appjob.BindRuntime(ctx, deps, conv, *a.Runtime); err != nil {
			return err
		}
	}
	sel.AcceptedAt = time.Now().UTC()
	return deps.Convs.SavePendingSelection(ctx, convID, sel)
}

// Pending answers what the room waits to apply on its next turn.
func (app *ConversationApp) Pending(ctx context.Context, convID string) (drepo.PendingSelection, bool, error) {
	return app.depsSnapshot().Convs.PendingSelection(ctx, convID)
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
	// A stored pick whose id the offered list does not carry is deferred —
	// a stored intent, retried when the option materializes, never the
	// active choice. It shows as its own marked entry, named from the
	// stored catalog when that remembers the id.
	deferred := make([]dservice.AgentOption, 0)
	for _, pick := range info.AgentOptions {
		if slices.ContainsFunc(opts, func(o dservice.AgentOption) bool { return o.ID == pick.ID }) {
			continue
		}
		entry := dservice.AgentOption{ID: pick.ID, Name: pick.ID, Type: "select", Deferred: pick.Value}
		if deps.AgentCatalog != nil {
			if cat, err := deps.AgentCatalog.Catalog(ctx, info.AgentID); err == nil {
				if j := slices.IndexFunc(cat.Options, func(o dservice.AgentOption) bool { return o.ID == pick.ID }); j >= 0 {
					stored := cat.Options[j]
					entry.Name, entry.Description, entry.Category, entry.Type, entry.Choices =
						stored.Name, stored.Description, stored.Category, stored.Type, stored.Choices
				}
			}
		}
		deferred = append(deferred, entry)
	}
	return append(opts, deferred...), nil
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
func (app *ConversationApp) SetAgentOption(ctx context.Context, convID, id, value string) (parked bool, err error) {
	deps := app.depsSnapshot()
	if value != "" {
		info, err := deps.Convs.Info(ctx, convID)
		if err != nil {
			return false, err
		}
		opts, err := app.optionReport(ctx, deps, info)
		if err != nil {
			return false, err
		}
		if opts != nil {
			i := slices.IndexFunc(opts, func(o dservice.AgentOption) bool { return o.ID == id })
			if i < 0 {
				// Not offered — storable only when some catalog knows the id
				// (a deferred pick retried on materialize). The stored
				// catalog is that memory; its read failure refuses the
				// write, and an id no catalog knows is refused outright.
				var stored drepo.AgentCatalog
				if deps.AgentCatalog != nil {
					if stored, err = deps.AgentCatalog.Catalog(ctx, info.AgentID); err != nil {
						return false, apperror.New(503, "the agent catalog could not be read", err)
					}
				}
				j := slices.IndexFunc(stored.Options, func(o dservice.AgentOption) bool { return o.ID == id })
				if j < 0 {
					return false, apperror.New(400, fmt.Sprintf("the agent offers no option %q", id), nil)
				}
				if !stored.Options[j].Offers(value) {
					return false, apperror.New(400, fmt.Sprintf("option %q does not offer %q", id, value), nil)
				}
			} else if !opts[i].Offers(value) {
				return false, apperror.New(400, fmt.Sprintf("option %q does not offer %q", id, value), nil)
			}
		}
		if app.running(convID) {
			// A turn fixes its runtime when it starts, so applying the pick to
			// the live session now would change a session under a running turn.
			// It waits for the next turn instead — no refusal, no half state.
			return true, app.parkSelection(ctx, convID, Annotation{}, drepo.PendingSelection{Options: map[string]string{id: value}})
		}
		if deps.AgentRuntime != nil {
			if err := deps.AgentRuntime.SetOption(ctx, convID, id, value); err != nil {
				return false, err
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
