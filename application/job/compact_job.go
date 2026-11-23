package job

import (
	"context"
	"fmt"
	"strings"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
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
	err = func() error {
		if err := ctx.Err(); err != nil {
			return apperror.New(408, "compaction canceled", err)
		}
		conv, err := d.Convs.Load(ctx, convID)
		if err != nil {
			return err // 404 — no room, nothing to fold
		}
		keepTail := d.Config.CompactKeepTail
		if conv.Runtime() == enum.RuntimeAgent {
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
// without a turn: an external agent (with the model it reports), or a
// provider and one of its models.
//
// It exists because a room's runtime is otherwise only ever written by a
// turn. A picker that kept its choice in the browser therefore changed
// nothing a chat-driven turn could see: switch the model on the web, send
// the next message from WhatsApp, and the room answered on what it was
// bound to last.
type RuntimeBinding struct {
	AgentID string
	// AgentModel is the model the agent reports; agents pick their own,
	// so it is optional and only meaningful with AgentID.
	AgentModel string
	ProviderID string
	ModelID    string
}

// bindRuntime applies a binding with the same rules a turn applies: an
// agent wins over a provider, and naming a provider route releases the
// agent. Both BindAgent and UnbindAgent clear the provider/model pair, so
// the pair is set after them, never before.
func (d Deps) bindRuntime(ctx context.Context, conv *aggregate.Conversation, b RuntimeBinding) error {
	switch {
	case b.AgentID != "":
		if d.Agents == nil {
			return apperror.New(503, "agents unavailable", nil)
		}
		// The picker may name an agent that was deleted meanwhile; a stale
		// id must be refused here rather than fail on the next turn.
		if _, err := d.Agents.Load(ctx, b.AgentID); err != nil {
			if apperror.As(err).StatusCode() == apperror.StatusNotFound {
				return apperror.New(400, "unknown agent", err)
			}
			return err
		}
		if conv.Environment() != enum.EnvironmentLocal {
			return apperror.New(400, "agents run only in the local environment", nil)
		}
		if err := conv.BindAgent(b.AgentID); err != nil {
			return apperror.New(400, "invalid agent", err)
		}
		if b.AgentModel != "" {
			conv.SetModelID(b.AgentModel)
		}
	case b.ProviderID != "" || b.ModelID != "":
		if b.ProviderID == "" || b.ModelID == "" {
			return apperror.New(400, "a provider binding needs both providerId and modelId", nil)
		}
		if d.Providers == nil {
			return apperror.New(503, "providers unavailable", nil)
		}
		prov, err := d.Providers.Load(ctx, b.ProviderID)
		if err != nil {
			if apperror.As(err).StatusCode() == apperror.StatusNotFound {
				return apperror.New(400, "unknown provider", err)
			}
			return err
		}
		if _, ok := prov.Model(b.ModelID); !ok {
			return apperror.New(400, "unknown model",
				fmt.Errorf("provider %s catalog has no model %s", b.ProviderID, b.ModelID))
		}
		conv.UnbindAgent()
		conv.SetProviderID(b.ProviderID)
		conv.SetModelID(b.ModelID)
	default:
		return apperror.New(400, "a runtime binding needs an agentId, or a providerId and modelId", nil)
	}
	return nil
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
			if err := d.bindRuntime(ctx, conv, *j.Runtime); err != nil {
				return err
			}
		}
		if j.Effort != nil {
			conv.SetEffort(*j.Effort)
		}
		if j.Upstream != nil {
			conv.SetUpstream(strings.TrimSpace(*j.Upstream))
		}
		return d.Convs.Save(ctx, conv)
	}()
	return err
}
