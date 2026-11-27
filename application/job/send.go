package job

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	repository "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
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
	AgentOptions valueobject.ConfigSelections
	// SystemPayload marks the message as a server record rather than the owner's
	// words — a wake turn's completion record, for one. It rides a user-role
	// message typed system: readable, never editable.
	SystemPayload bool
	// Relay names the thread this prompt came from, when another thread sent
	// it. The job records it on the user message; the head is rendered from it
	// at request time, never stored.
	Relay *valueobject.Relay
	// LiveTurn names the transport's live turn for this send — a websocket
	// turnId — when one exists. The app binds the turn's mailbox to it, so a
	// steer naming that exact turn reaches this job whether it is queued or
	// already running. "" means no transport turn: the running job decides.
	LiveTurn string
	// AcceptedRecord is the user line the accepting path already wrote for this
	// prompt. The job uses it — appends it to its own view if needed, marks the
	// receipt — instead of writing a second line for the same words. A record
	// that is not in the store is a failure, never a reason to write another.
	AcceptedRecord *entity.Message
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
	// exec is the runtime this turn resolved. The rescue reads it after the
	// aggregate is out of scope, and after the active snapshot is cleared.
	exec turnExecutor
	// active is the binding this turn actually runs on, once it resolved one.
	// A projection reads it to answer "what is answering me now"; it is cleared
	// when the turn ends, at the same defer that releases the room.
	activeMu      sync.Mutex
	activeBinding ActiveBinding
	active        bool
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

// rescueFailed says out loud that a line the room accepted will not be delivered
// by this turn. The room, not the words: a log is not a transcript.
func (j *Send) rescueFailed(step, msgID string, err error) {
	if j.Deps.Log == nil {
		return
	}
	j.Deps.Log.Warn("relay rescue failed", "conversation", j.ConvID(), "message", msgID, "step", step, "error", err)
}

// rescueSteers delivers what acceptance already recorded but this turn did not
// consume. It never writes provenance: acceptance owns the one relay line. An
// owner's steer is not the caller's to decide either — the same rule covers
// both, and the text the turn never reached becomes the next turn.
func (j *Send) rescueSteers() {
	for _, e := range j.mailbox().detachPending() {
		if e.input.Relay == nil {
			// Waiting text whose turn answered without it is the next turn —
			// never a draft, never a silent drop. A composition without the
			// hook (the CLI's) has no queue to hand it to.
			if j.Deps.NextTurn == nil {
				continue
			}
			if err := j.Deps.NextTurn(j.ConvID(), e.input, j.Opts.Origin); err != nil {
				j.rescueFailed("next turn", e.input.InputID, err)
			}
			continue
		}
		if e.record != nil {
			// The line exists. A provider turn reads it from history on its next
			// model call; an agent session never reads that history, so the text
			// is delivered to a following turn — carrying this very line, never a
			// second one. One attempt: the follow-up job carries the record, so
			// its own rescue stays out of the loop.
			if j.exec != nil && j.exec.followUpRelay() && j.Deps.FollowUp != nil && j.Opts.AcceptedRecord == nil {
				if err := j.Deps.FollowUp(j.ConvID(), *e.record); err != nil {
					j.rescueFailed("follow-up", e.record.ID(), err)
				}
			}
			continue
		}
		// A relay without its acceptance record cannot be rescued honestly:
		// writing a fresh row here would be a second provenance writer and
		// would hide the acceptance failure. The mailbox rejects this shape,
		// so reaching it is a loud invariant breach.
		if j.Deps.Log != nil {
			j.Deps.Log.Error("relayed steer has no acceptance record", "conversation", j.ConvID(), "relay", e.input.Relay.ID)
		}
	}
}

// Steer buffers user input for the running turn — the worker's
// out-of-band control path. The turn loop folds it at the next safe
// point; it never interrupts the in-flight provider call.
// Active answers the kind and identifiers this turn is using, false when
// the turn has not resolved one or has already ended.
func (j *Send) Active() (ActiveBinding, bool) {
	j.activeMu.Lock()
	defer j.activeMu.Unlock()
	return j.activeBinding, j.active
}

func (j *Send) setActive(b ActiveBinding) {
	j.activeMu.Lock()
	j.activeBinding, j.active = b, true
	j.activeMu.Unlock()
}

func (j *Send) clearActive() {
	j.activeMu.Lock()
	j.activeBinding, j.active = ActiveBinding{}, false
	j.activeMu.Unlock()
}

func (j *Send) Steer(text string) { j.mailbox().push(text) }

// SteerInput buffers a full input — text with its selections, uploads, marks
// and relay record — so the fold can write one line carrying all of it. It
// reports whether the mailbox took it; a full mailbox buffered nothing.
func (j *Send) SteerInput(in valueobject.UserInput, record *entity.Message) (any, bool) {
	r, err := j.mailbox().SubmitBefore(in, j.Deps.Config.MailboxCap, record)
	if err != nil {
		return nil, false
	}
	return r, true
}

// RecallInput withdraws the pending input submitted under inputID.
func (j *Send) RecallInput(inputID string) bool { return j.mailbox().RecallID(inputID) }

// SteerInputAvailable reports whether the mailbox has room right now.
func (j *Send) SteerInputAvailable() bool { return j.mailbox().available(j.Deps.Config.MailboxCap) }

// RecallSteer withdraws the newest un-fired steering input.
func (j *Send) RecallSteer() (string, bool) { return j.mailbox().popLast() }

// Execute runs the whole turn end-to-end on the worker's goroutine.
// IsTurn marks this job as a turn: a runtime pick must not be applied under it,
// because a turn fixes its runtime when it starts.
func (*Send) IsTurn() bool { return true }

func (j *Send) Execute(execCtx context.Context) (err error) {
	defer j.finish(&err)
	// A relayed prompt the turn never folded still has to reach the next one:
	// history is the persistence, so the line is written once, here, and the
	// fold that never ran leaves no second copy behind.
	defer j.rescueSteers()
	ctx := j.Cancellable(execCtx, j.Ctx)
	d := j.Deps
	convID := j.ConvID()
	// The room is claimed for exactly as long as this turn runs, and released by
	// the deferred call before the result reaches the caller — the worker's own
	// "what is running" carries a moment past that, which would park a pick
	// taken right after the turn for nothing. A composition without a gate (the
	// CLI's) simply has nothing to claim; ConversationApp always fills it.
	if d.Turns != nil {
		d.Turns.BeginTurn(convID)
		defer d.Turns.EndTurn(convID)
	}
	// A turn no socket admitted still has an audience — its room. The seal runs
	// on every exit, so a failed turn closes its journal too.
	onEvent, sealLive := liveTurn(d, convID, "send", j.Opts.Origin, j.OnEvent)
	persisted := false
	defer func() { sealLive(persisted, err) }()
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
		// A pick taken while this room was busy waits for exactly this moment:
		// it applies before anything reads the room's runtime, so the turn that
		// runs next — from any thread — is the one it changes. A brand-new room
		// has nothing parked.
		var consumedAt time.Time
		appliedPending := false
		if !fresh {
			consumedAt, appliedPending, err = applyPendingSelection(ctx, d, conv, onEvent)
			if err != nil {
				return err
			}
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
				if errors.Is(err, aggregate.ErrAgentNeedsLocal) {
					return apperror.New(400, err.Error(), err)
				}
				return apperror.New(400, "invalid agent", err)
			}
		case j.ProviderID != "":
			conv.UnbindAgent()
		}
		// Options the picker staged before the room existed apply to it
		// now, ahead of the first turn: a mode chosen here is what the
		// agent should start in, not something to correct afterwards.
		if len(j.Opts.AgentOptions) > 0 {
			if !conv.Runtime().QueriesAgentSessionOptions() {
				return apperror.New(400, "agent options need a room bound to an agent", nil)
			}
			for _, pick := range j.Opts.AgentOptions {
				if err := conv.SetAgentOption(pick.ID, pick.Value); err != nil {
					return apperror.New(400, err.Error(), err)
				}
			}
		}
		ex, err := executorFor(conv.Runtime())
		if err != nil {
			return err
		}
		j.exec = ex
		// The kind this turn runs, from the moment it resolved one until it
		// ends. A projection reads the snapshot, never the stored binding a
		// later turn will use.
		j.setActive(ex.snapshot(conv, j.ProviderID, j.ModelID))
		defer j.clearActive()
		if err := ex.refuse(&j.Opts.Effort, &j.Opts.Upstream); err != nil {
			return err
		}
		if err := ex.rejectEnvironment(conv); err != nil {
			return err
		}
		rt, err := ex.resolve(ctx, d, convID, conv, driver, j.ProviderID, j.ModelID, &j.Opts)
		if err != nil {
			return err
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

		var user entity.Message
		if j.Opts.AcceptedRecord != nil {
			// The accepting path wrote this line before it answered the sender.
			// Using it is what makes a queued acceptance a promise the store
			// already keeps — and it must be there: a record that is not stored
			// is a broken acceptance, not a reason to write a second line.
			record := *j.Opts.AcceptedRecord
			if !storedMessage(ctx, d.Convs, convID, record) {
				return apperror.New(500, "accepted relay record is not in the store",
					fmt.Errorf("message %s", record.ID()))
			}
			user = record
			j.Opts.Relay = nil
		} else {
			var err error
			user, err = entity.NewUserMessage(valueobject.UserInput{
				Text:        j.Message,
				References:  j.Opts.References,
				Attachments: j.Attachments,
				Annotations: j.Opts.Annotations,
				Recording:   j.Opts.Recording,
			})
			if err != nil {
				return apperror.New(400, "invalid references", err)
			}
		}
		// A prompt another thread handed over carries its own relay head; the
		// owner cannot rewrite that room's words from here, so the provenance
		// says so and the message stays non-editable in the target.
		if j.Opts.AcceptedRecord == nil {
			if j.Opts.SystemPayload {
				user.SetUserType(enum.LLMUserTypeSystem)
			}
			if origin, ok := j.Opts.Origin.Normalized(); ok && origin.Kind == enum.TurnOriginConversation {
				if !fresh {
					return apperror.NewInternalError("relayed send has no acceptance record", nil)
				}
				// A freshly spawned room does not exist when control accepts the
				// prompt, so its first line is written here with the room. Every
				// send to an existing room carries AcceptedRecord instead.
				user.SetUserType(enum.LLMUserTypeRelay)
				if j.Opts.Relay != nil {
					user.SetRelay(*j.Opts.Relay)
				}
			}
		}
		if !hasMessage(conv, user.ID()) {
			conv.Append(user)
		}
		if fresh {
			// Bind before the first write — a failed send still leaves a
			// room that can retry with providerID/modelID "". An agent
			// target stores no provider route; the executor drops the picks
			// a provider call would have kept.
			conv.SetProviderID(rt.providerID)
			conv.SetModelID(rt.modelID)
			ex.rememberFresh(conv, j.Opts)
			// An explicit creation title names the room from its first
			// save; otherwise the first message's head does — SetTitle
			// normalizes either to one bounded line.
			title := j.Opts.Title
			if title == "" {
				title = user.Content()
			}
			conv.SetTitle(title)
			// The room's handle is the store's to give: Save allocates one for
			// a new room under the lock it writes with, so two rooms created at
			// the same moment cannot pick the same animal.
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
		appendErr := j.Receipt.appendUserMessage(func() (bool, error) {
			if j.Opts.AcceptedRecord != nil {
				return true, nil
			}
			if err := ctx.Err(); err != nil {
				return false, apperror.New(408, "send canceled", err)
			}
			return d.Convs.AppendMessage(ctx, convID, user)
		})
		persisted = j.Receipt.UserMessagePersisted()
		if appendErr != nil {
			if fresh && !persisted {
				// The caller's ctx may already be canceled. A store that
				// honors it would leave the partial room behind.
				_ = d.Convs.Delete(context.WithoutCancel(ctx), convID)
			}
			return appendErr
		}

		err = ex.run(ctx, d, convID, conv, driver, user, rt, onEvent, j.mailbox(), j.Opts.Origin, j.ModelID)
		if ex.retireUnconditionally() {
			ex.retire(d, convID, prevAgent, prevSession, conv)
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
		ex.commit(conv, rt, j.Opts)
		if err := d.Convs.Save(ctx, conv); err != nil {
			return err
		}
		// The pick rode this turn's own save; the claim release runs after this
		// clear, so a failed save leaves both the file and the projection honest.
		clearConsumedPending(ctx, d, convID, consumedAt, appliedPending)
		if !ex.retireUnconditionally() {
			ex.retire(d, convID, prevAgent, prevSession, conv)
		}
		return nil
	}()
	return err
}

// storedMessage reports whether the line an acceptance wrote is in the room's
// history — read from the store, never assumed.
func storedMessage(ctx context.Context, convs repository.Conversation, convID string, rec entity.Message) bool {
	conv, err := convs.Load(ctx, convID)
	if err != nil {
		return false
	}
	for _, m := range conv.Messages() {
		if m.ID() == rec.ID() {
			return true
		}
	}
	return false
}
