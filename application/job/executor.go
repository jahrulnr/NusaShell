package job

import (
	"context"
	"fmt"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	"github.com/jahrulnr/nusashell/infrastructure/prompt"
)

// ActiveBinding is the kind and identifiers a turn is using while it runs.
// A projection reads this instead of the binding a later turn will store.
type ActiveBinding struct {
	Kind       enum.Runtime
	AgentID    string
	ProviderID string
	ModelID    string
}

// resolvedRoute is a harness turn's provider call. An agent turn leaves it empty.
type resolvedRoute struct {
	prov                *aggregate.Provider
	model               entity.Model
	svc                 dservice.TextModelService
	providerID, modelID string
}

// turnExecutor is one runtime's resolution, execution, and session rescue.
// Send, Edit, and Resume ask it. They do not switch on the kind.
type turnExecutor interface {
	refuse(effort *enum.Effort, upstream *string) error
	rejectEnvironment(conv *aggregate.Conversation) error
	resolve(ctx context.Context, d Deps, convID string, conv *aggregate.Conversation, driver EnvironmentDriver, providerID, modelID string, opts *SendOptions) (resolvedRoute, error)
	run(ctx context.Context, d Deps, convID string, conv *aggregate.Conversation, driver EnvironmentDriver, user entity.Message, rt resolvedRoute, onEvent event.TurnEventHandler, box *SteeringInput, origin valueobject.TurnOrigin, askedModel string) error
	commit(conv *aggregate.Conversation, rt resolvedRoute, opts SendOptions)
	rememberFresh(conv *aggregate.Conversation, opts SendOptions)
	retire(d Deps, convID, prevAgent, prevSession string, conv *aggregate.Conversation)
	retireUnconditionally() bool
	followUpRelay() bool
	snapshot(conv *aggregate.Conversation, askedProvider, askedModel string) ActiveBinding
	edit(ctx context.Context, d Deps, j *Edit, conv *aggregate.Conversation, driver EnvironmentDriver, sealed bool, prevAgent, prevSession string, onEvent event.TurnEventHandler) error
	resume(ctx context.Context, d Deps, j *Resume, conv *aggregate.Conversation, driver EnvironmentDriver, onEvent event.TurnEventHandler) error
}

func executorFor(kind enum.Runtime) (turnExecutor, error) {
	switch kind {
	case enum.RuntimeHarness:
		return harnessExec{}, nil
	case enum.RuntimeAgent:
		return agentExec{}, nil
	default:
		return nil, apperror.New(500, "unknown runtime", fmt.Errorf("%s", kind))
	}
}

type harnessExec struct{}

func (harnessExec) refuse(effort *enum.Effort, upstream *string) error {
	return AgentPickRefusal(enum.RuntimeHarness, effort, upstream)
}

func (harnessExec) rejectEnvironment(*aggregate.Conversation) error { return nil }

func (harnessExec) resolve(ctx context.Context, d Deps, convID string, conv *aggregate.Conversation, driver EnvironmentDriver, providerID, modelID string, opts *SendOptions) (resolvedRoute, error) {
	prov, model, svc, err := d.resolveProvider(ctx, convID, conv, driver, providerID, modelID)
	if err != nil {
		return resolvedRoute{}, err
	}
	if opts == nil {
		opts = &SendOptions{}
	}
	if len(conv.Messages()) == 0 && opts.Effort == "" && conv.Effort() == "" && !d.ThreadsEffort.IsUnset() {
		opts.Effort = d.ThreadsEffort
	}
	useSelection(&model, conv, *opts)
	return resolvedRoute{prov: prov, model: model, svc: svc, providerID: prov.ID(), modelID: model.ID()}, nil
}

func (harnessExec) run(ctx context.Context, d Deps, convID string, conv *aggregate.Conversation, driver EnvironmentDriver, user entity.Message, rt resolvedRoute, onEvent event.TurnEventHandler, box *SteeringInput, origin valueobject.TurnOrigin, _ string) error {
	return d.runTurn(ctx, convID, conv, driver, rt.prov, rt.model, rt.svc, onEvent, box, origin)
}

func (harnessExec) commit(conv *aggregate.Conversation, rt resolvedRoute, opts SendOptions) {
	conv.SetProviderID(rt.providerID)
	conv.SetModelID(rt.modelID)
	rememberSelection(conv, opts)
}

func (harnessExec) rememberFresh(conv *aggregate.Conversation, opts SendOptions) {
	rememberSelection(conv, opts)
}

func (harnessExec) retire(d Deps, convID, prevAgent, prevSession string, conv *aggregate.Conversation) {
	d.retireAgentSession(convID, prevAgent, prevSession, conv)
}

func (harnessExec) retireUnconditionally() bool { return false }

func (harnessExec) followUpRelay() bool { return false }

func (harnessExec) snapshot(conv *aggregate.Conversation, askedProvider, askedModel string) ActiveBinding {
	if askedProvider == "" {
		askedProvider = conv.ProviderID()
	}
	if askedModel == "" {
		askedModel = conv.ModelID()
	}
	return ActiveBinding{Kind: enum.RuntimeHarness, ProviderID: askedProvider, ModelID: askedModel}
}

func (harnessExec) edit(ctx context.Context, d Deps, j *Edit, conv *aggregate.Conversation, driver EnvironmentDriver, sealed bool, _, _ string, onEvent event.TurnEventHandler) error {
	convID := j.ConvID()
	prov, model, svc, err := d.resolveProvider(ctx, convID, conv, driver, j.ProviderID, j.ModelID)
	if err != nil {
		return err
	}
	providerID, modelID := prov.ID(), model.ID()
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
	ctx = dservice.WithExtraFolders(ctx, extra)
	if err := d.runTurn(ctx, convID, conv, driver, prov, model, svc, onEvent, &j.steers, j.Opts.Origin); err != nil {
		return err
	}
	conv.SetProviderID(providerID)
	conv.SetModelID(modelID)
	rememberSelection(conv, j.Opts)
	return d.Convs.Save(ctx, conv)
}

func (harnessExec) resume(ctx context.Context, d Deps, j *Resume, conv *aggregate.Conversation, driver EnvironmentDriver, onEvent event.TurnEventHandler) error {
	convID := j.ConvID()
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
	ctx = dservice.WithExtraFolders(ctx, extra)
	if err := d.runTurn(ctx, convID, conv, driver, prov, model, svc, onEvent, &j.steers, j.Opts.Origin); err != nil {
		return err
	}
	conv.SetProviderID(providerID)
	conv.SetModelID(modelID)
	rememberSelection(conv, j.Opts)
	return d.Convs.Save(ctx, conv)
}

type agentExec struct{}

func (agentExec) refuse(effort *enum.Effort, upstream *string) error {
	return AgentPickRefusal(enum.RuntimeAgent, effort, upstream)
}

func (agentExec) rejectEnvironment(conv *aggregate.Conversation) error {
	if conv.Environment() != enum.EnvironmentLocal {
		return apperror.New(400, "agents run only in the local environment", nil)
	}
	return nil
}

func (agentExec) resolve(context.Context, Deps, string, *aggregate.Conversation, EnvironmentDriver, string, string, *SendOptions) (resolvedRoute, error) {
	return resolvedRoute{}, nil
}

func (agentExec) run(ctx context.Context, d Deps, convID string, conv *aggregate.Conversation, driver EnvironmentDriver, user entity.Message, _ resolvedRoute, onEvent event.TurnEventHandler, box *SteeringInput, origin valueobject.TurnOrigin, askedModel string) error {
	return d.runAgentTurn(ctx, convID, conv, driver, user, onEvent, box, origin, askedModel)
}

func (agentExec) commit(*aggregate.Conversation, resolvedRoute, SendOptions) {}

func (agentExec) rememberFresh(*aggregate.Conversation, SendOptions) {}

func (agentExec) retire(d Deps, convID, prevAgent, prevSession string, conv *aggregate.Conversation) {
	d.retireAgentSession(convID, prevAgent, prevSession, conv)
}

func (agentExec) retireUnconditionally() bool { return true }

func (agentExec) followUpRelay() bool { return true }

func (agentExec) snapshot(conv *aggregate.Conversation, _, askedModel string) ActiveBinding {
	if askedModel == "" {
		askedModel = conv.ModelID()
	}
	return ActiveBinding{Kind: enum.RuntimeAgent, AgentID: conv.AgentID(), ModelID: askedModel}
}

func (agentExec) edit(ctx context.Context, d Deps, j *Edit, conv *aggregate.Conversation, driver EnvironmentDriver, sealed bool, prevAgent, prevSession string, onEvent event.TurnEventHandler) error {
	convID := j.ConvID()
	if err := d.Convs.RewriteFrom(ctx, convID, j.MsgID, j.Content); err != nil {
		return err
	}
	if sealed {
		var err error
		if conv, err = d.Convs.Load(ctx, convID); err != nil {
			return err
		}
		conv.ForgetAgentSession()
		if _, _, err := applyPendingSelection(ctx, d, conv, onEvent); err != nil {
			return err
		}
	}
	err := d.runAgentTurn(ctx, convID, conv, driver, *conv.Last(), onEvent, &j.steers, j.Opts.Origin, "")
	d.retireAgentSession(convID, prevAgent, prevSession, conv)
	if err != nil {
		return err
	}
	return d.Convs.Save(ctx, conv)
}

func (agentExec) resume(ctx context.Context, d Deps, j *Resume, conv *aggregate.Conversation, driver EnvironmentDriver, onEvent event.TurnEventHandler) error {
	last := conv.Last()
	if last.Role() != enum.RoleUser || !last.Editable() {
		return apperror.New(409, "nothing to retry — send a new message to the agent", nil)
	}
	if err := d.runAgentTurn(releasePause(ctx), j.ConvID(), conv, driver, *last, onEvent, &j.steers, j.Opts.Origin, ""); err != nil {
		return err
	}
	return d.Convs.Save(ctx, conv)
}
