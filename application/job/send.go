package job

import (
	"context"
	"sync"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	"github.com/jahrulnr/nusashell/infrastructure/prompt"
)

// PersistenceReceipt records whether one Send appended its user message.
// The broker reads it even when cancellation wins the wait, while Send
// serializes the append and receipt update against that read.
type PersistenceReceipt struct {
	mu                   sync.Mutex
	userMessagePersisted bool
}

// NewPersistenceReceipt creates a receipt for one Send submission.
func NewPersistenceReceipt() *PersistenceReceipt { return &PersistenceReceipt{} }

// UserMessagePersisted reports whether this submission's user line committed.
func (r *PersistenceReceipt) UserMessagePersisted() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.userMessagePersisted
}

func (r *PersistenceReceipt) appendUserMessage(appendMessage func() (bool, error)) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	persisted, err := appendMessage()
	r.userMessagePersisted = persisted
	return err
}

// useSelection applies the effort and upstream pin a turn runs with to the
// model for that call: the turn's own choice, else the one stored on the
// room, so a turn started anywhere runs the way the owner set the room up.
// An explicit "default" effort asks for the model's configured one, which a
// stored effort must not override.
func useSelection(model *entity.Model, conv *aggregate.Conversation, opts SendOptions) {
	effort := opts.Effort
	if effort == "" {
		effort = conv.Effort()
	}
	if !effort.IsUnset() {
		model.SetEffort(effort)
	}
	upstream := opts.Upstream
	if upstream == "" {
		upstream = conv.Upstream()
	}
	model.SetUpstream(upstream)
}

// rememberSelection stores what the owner chose for a turn as the room's
// own, once the turn has run (or, for a new room, when it is bound): the
// next turn, wherever it starts, keeps the effort and the upstream pin.
func rememberSelection(conv *aggregate.Conversation, opts SendOptions) {
	if opts.Effort != "" {
		conv.SetEffort(opts.Effort)
	}
	if opts.Upstream != "" {
		conv.SetUpstream(opts.Upstream)
	}
}

// SendOptions carries the UI's per-turn selections — reasoning effort,
// an aggregator upstream pin, and the workspace the room's tools work
// in. Zero values keep the conversation's stored binding, effort and
// upstream included.
type SendOptions struct {
	Effort      enum.Effort      // unset/default = the model's configured effort
	Upstream    string           // aggregator backend name; "" = the provider decides
	Workspace   string           // absolute dir; "" = keep the room's binding
	Environment enum.Environment // "" = keep the room's binding (local for a new room)
	// Project opens a new room inside a project. Ignored for an existing
	// room, which moves between projects through its own metadata.
	Project string
	// Agent binds a new room to a configured external agent instead of a
	// provider. "" keeps the room's runtime.
	Agent string
	// Title names the room this send creates — a cross-thread spawn, for
	// one — instead of the first-message default. An existing room
	// ignores it.
	Title string
	// AgentOptions are the agent's session options chosen before the room
	// existed — its mode, for one. They apply to the room this send opens;
	// an existing room sets its own through the conversation.
	AgentOptions map[string]string
	// References are the selections the owner made in the message text.
	References []valueobject.Reference
	// Annotations are the marks the owner made on the live page; their
	// screenshots ride among the attachments.
	Annotations []valueobject.Annotation
	// Recording is the browser recording the owner attached; resolved
	// before the send so the message stores the ref read at send time.
	Recording *valueobject.RecordingRef
	// Steering binds an exact-turn input mailbox owned by the caller —
	// a transport can submit steering into a send that is still queued
	// on the room worker without touching whatever job is running.
	Steering *SteeringInput
	// Origin names where this turn started — the web UI, a chat channel,
	// an automation, or another thread. The turn records it as context,
	// never as part of the system prompt.
	Origin valueobject.TurnOrigin
}

// Send is one user turn as a queued command: it validates routing,
// persists the user message, then runs the turn loop until the model
// stops asking for tools or the step cap is hit. Ctx is the caller's —
// a queued send abandons itself via it; Interrupt cancels the run.
type Send struct {
	Base
	Deps    Deps
	Ctx     context.Context
	Receipt *PersistenceReceipt // per-submission append state shared with the broker

	ProviderID  string
	ModelID     string
	Message     string
	Attachments []valueobject.Attachment
	Opts        SendOptions
	OnEvent     event.TurnEventHandler

	steers SteeringInput
}

// mailbox is the turn's input mailbox — the caller-supplied
// Opts.Steering when bound, else the job's internal one.
func (j *Send) mailbox() *SteeringInput {
	if j.Opts.Steering != nil {
		return j.Opts.Steering
	}
	return &j.steers
}

// Steer buffers user input for the running turn — the worker's
// out-of-band control path. The turn loop folds it at the next safe
// point; it never interrupts the in-flight provider call.
func (j *Send) Steer(text string) { j.mailbox().push(text) }

// RecallSteer withdraws the newest un-fired steering input.
func (j *Send) RecallSteer() (string, bool) { return j.mailbox().popLast() }

// Execute runs the whole turn end-to-end on the worker's goroutine.
func (j *Send) Execute(execCtx context.Context) (err error) {
	defer j.finish(&err)
	ctx := j.Cancellable(execCtx, j.Ctx)
	d := j.Deps
	convID := j.ConvID()
	err = func() error {
		if err := ctx.Err(); err != nil {
			return apperror.New(408, "send canceled", err)
		}

		conv, err := d.Convs.Load(ctx, convID)
		fresh := false
		if err != nil {
			if apperror.As(err).StatusCode() != apperror.StatusNotFound {
				return err
			}
			conv, err = aggregate.NewConversation(convID) // FE-anchored or BE-minted id
			if err != nil {
				return apperror.New(400, "invalid conversation id", err)
			}
			fresh = true
		}

		// A room that moves to another agent, or back to a provider, leaves
		// its old agent's session behind; remember it to remove afterwards.
		prevAgent, prevSession := conv.AgentID(), conv.AgentSession()
		prevEnv := conv.Environment()
		prevWorkspace := conv.Workspace()
		if j.Opts.Environment != "" {
			previous := conv.Environment()
			if err := conv.SetEnvironment(j.Opts.Environment); err != nil {
				return apperror.New(400, "unknown conversation environment", err)
			}
			if !fresh && previous != j.Opts.Environment {
				conv.SetWorkspace("")
			}
		}
		driver, err := d.environment(conv)
		if err != nil {
			return err
		}
		// The picker names either an agent or a provider; naming one
		// moves the room onto that runtime, naming neither keeps it.
		switch {
		case j.Opts.Agent != "":
			if err := conv.BindAgent(j.Opts.Agent); err != nil {
				return apperror.New(400, "invalid agent", err)
			}
		case j.ProviderID != "":
			conv.UnbindAgent()
		}
		// Options the picker staged before the room existed apply to it
		// now, ahead of the first turn: a mode chosen here is what the
		// agent should start in, not something to correct afterwards.
		if len(j.Opts.AgentOptions) > 0 {
			if conv.Runtime() != enum.RuntimeAgent {
				return apperror.New(400, "agent options need a room bound to an agent", nil)
			}
			for id, value := range j.Opts.AgentOptions {
				if err := conv.SetAgentOption(id, value); err != nil {
					return apperror.New(400, err.Error(), err)
				}
			}
		}
		agentRoom := conv.Runtime() == enum.RuntimeAgent
		if agentRoom && conv.Environment() != enum.EnvironmentLocal {
			return apperror.New(400, "agents run only in the local environment", nil)
		}
		// An agent room has no provider route: the agent picks its own
		// model, so provider and model selections do not apply.
		var (
			prov                *aggregate.Provider
			model               entity.Model
			svc                 dservice.TextModelService
			providerID, modelID string
		)
		if !agentRoom {
			prov, model, svc, err = d.resolveProvider(ctx, convID, conv, driver, j.ProviderID, j.ModelID)
			if err != nil {
				return err
			}
			providerID, modelID = prov.ID(), model.ID()
			// Per-turn selections override the catalog entry in memory; the
			// room's workspace rebinds the same way providerID/modelID do below.
			useSelection(&model, conv, j.Opts)
		}
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
		// A new room may open inside a project; a room in a project works
		// in the project's folders, whatever workspace it held before.
		if fresh && j.Opts.Project != "" {
			if d.Projects == nil {
				return apperror.New(503, "projects unavailable", nil)
			}
			if _, err := d.Projects.Get(ctx, j.Opts.Project); err != nil {
				if apperror.As(err).StatusCode() == apperror.StatusNotFound {
					return apperror.New(400, "unknown project", err)
				}
				return err
			}
			if err := conv.SetProject(j.Opts.Project); err != nil {
				return apperror.New(400, "invalid project", err)
			}
		}
		extra, err := d.applyProject(ctx, conv)
		if err != nil {
			return err
		}
		// File tools reach the project's other folders by absolute path.
		ctx = dservice.WithExtraFolders(ctx, extra)

		user, err := entity.NewUserMessage(valueobject.UserInput{
			Text:        j.Message,
			References:  j.Opts.References,
			Attachments: j.Attachments,
			Annotations: j.Opts.Annotations,
			Recording:   j.Opts.Recording,
		})
		if err != nil {
			return apperror.New(400, "invalid references", err)
		}
		conv.Append(user)
		if fresh {
			// Bind before the first write — a failed send still leaves a
			// room that can retry with providerID/modelID "".
			conv.SetProviderID(providerID)
			conv.SetModelID(modelID)
			rememberSelection(conv, j.Opts)
			// An explicit creation title names the room from its first
			// save; otherwise the first message's head does — SetTitle
			// normalizes either to one bounded line.
			title := j.Opts.Title
			if title == "" {
				title = user.Content()
			}
			conv.SetTitle(title)
			// Every room gets a handle chats can name it by.
			if conv.Label() == "" {
				if label, err := d.freeLabel(ctx); err == nil {
					_ = conv.SetLabel(label)
				}
			}
			if err := d.Convs.Save(ctx, conv); err != nil {
				return err
			}
		}
		// The system prompt is code, not data: it ships embedded and is
		// applied to the live aggregate every turn, never persisted — a
		// template upgrade reaches every room on its next send.
		if conv.SystemPrompt() == "" {
			conv.SetSystemPrompt(prompt.SystemPrompt("conversation"))
		}
		if j.Receipt == nil {
			return apperror.NewInternalError("send persistence receipt missing", nil)
		}
		if err := j.Receipt.appendUserMessage(func() (bool, error) {
			if err := ctx.Err(); err != nil {
				return false, apperror.New(408, "send canceled", err)
			}
			return d.Convs.AppendMessage(ctx, convID, user)
		}); err != nil {
			if fresh && !j.Receipt.UserMessagePersisted() {
				// The caller's ctx may already be canceled. A store that
				// honors it would leave the partial room behind.
				_ = d.Convs.Delete(context.WithoutCancel(ctx), convID)
			}
			return err
		}

		if agentRoom {
			if j.ModelID != "" {
				conv.SetModelID(j.ModelID)
			}
			err = d.runAgentTurn(ctx, convID, conv, driver, user, j.OnEvent, j.mailbox(), j.Opts.Origin)
			d.retireAgentSession(convID, prevAgent, prevSession, conv)
		} else {
			err = d.runTurn(ctx, convID, conv, driver, prov, model, svc, j.OnEvent, j.mailbox(), j.Opts.Origin)
		}
		if err != nil {
			// Route and model rebind only after success. Workspace and
			// environment must follow that rule for an existing room: a
			// per-step Save during the failed turn would otherwise keep
			// the override.
			if !fresh && (conv.Environment() != prevEnv || conv.Workspace() != prevWorkspace) {
				if setErr := conv.SetEnvironment(prevEnv); setErr == nil {
					conv.SetWorkspace(prevWorkspace)
					_ = d.Convs.Save(context.WithoutCancel(ctx), conv)
				}
			}
			return err
		}
		if !agentRoom {
			conv.SetProviderID(providerID) // existing rooms re-bind only on success —
			conv.SetModelID(modelID)       // a failed override keeps the old binding
			rememberSelection(conv, j.Opts)
		}
		if err := d.Convs.Save(ctx, conv); err != nil {
			return err
		}
		if !agentRoom {
			d.retireAgentSession(convID, prevAgent, prevSession, conv)
		}
		return nil
	}()
	return err
}
