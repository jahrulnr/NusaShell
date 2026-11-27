package job

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// runAgentTurn hands the user's message to the room's external agent. The
// agent runs its own tool loop; this turn persists what it produced, routes
// its permission requests through the agent's policy and the owner, and
// tries mid-turn steering; what the agent cannot take becomes a follow-up
// prompt after the current prompt yields at its next safe point or ends.
func (d Deps) runAgentTurn(ctx context.Context, convID string, conv *aggregate.Conversation, driver EnvironmentDriver, input entity.Message, onEvent event.TurnEventHandler, steers *SteeringInput, origin valueobject.TurnOrigin, model string) error {
	if d.Agents == nil || d.AgentRuntime == nil {
		return apperror.New(503, "agent runtime unavailable", nil)
	}
	agent, err := d.Agents.Load(ctx, conv.AgentID())
	if err != nil {
		if apperror.As(err).StatusCode() == apperror.StatusNotFound {
			return apperror.New(400, "conversation agent unavailable", err)
		}
		return err
	}
	// The runtime calls back from its own goroutines; subscribers expect
	// one ordered stream. Usage rides the finished event onto the room, and
	// the agent's report on its context window rides the context event.
	// lastUpdate is the last agent update's stamp. While it stays zero the
	// prompt has produced nothing, which is the only stretch a rate limit
	// may retry.
	var mu sync.Mutex
	var lastUpdate int64
	var rateAttempts int
	emit := func(ev event.TurnEvent) {
		mu.Lock()
		defer mu.Unlock()
		switch ev.Kind() {
		case enum.TurnEventFinished:
			conv.AddUsage(ev.Usage())
		case enum.TurnEventContext:
			conv.ReportAgentContext(ev.AgentContext())
		case enum.TurnEventTitle:
			// An agent names a room nobody has named; the room is saved
			// with the turn.
			conv.AdoptAgentTitle(ev.Delta())
		case enum.TurnEventClock:
			if ev.LastUpdateAt() > 0 {
				lastUpdate = ev.LastUpdateAt()
			}
		}
		if onEvent != nil {
			onEvent(ev)
		}
	}
	approve := func(actx context.Context, req valueobject.ApprovalRequest) (string, error) {
		if opt, ok := req.AutoAnswer(agent.Policy()); ok {
			return opt.ID, nil
		}
		// An "always allow" the owner gave in this room answers for them.
		if d.ApprovalRules != nil {
			if ok, err := d.ApprovalRules.Allows(actx, convID, req.Action()); err == nil && ok {
				if opt, ok := req.AllowAnswer(); ok {
					return opt.ID, nil
				}
			}
		}
		if d.Approvals == nil {
			return "", apperror.New(503, "approval broker unavailable", nil)
		}
		optionID, err := d.Approvals.ask(actx, convID, req, emit)
		if err == nil && d.ApprovalRules != nil {
			if opt, ok := req.Option(optionID); ok && opt.Choice == enum.ApprovalAllowAlways {
				if rule, rerr := aggregate.NewApprovalRule("", convID, agent.ID(), req.Action(), time.Now()); rerr == nil {
					_ = d.ApprovalRules.Create(context.WithoutCancel(actx), rule)
				}
			}
		}
		return optionID, err
	}
	// A request reaches the owner only where an answer surface exists;
	// an automation run has none, so its requests decline at once.
	var elicit dservice.Elicitor
	if d.Elicitations != nil && origin.Kind != enum.TurnOriginAutomation {
		elicit = func(ectx context.Context, e valueobject.Elicitation) (valueobject.ElicitationAnswer, error) {
			return d.Elicitations.ask(ectx, convID, e, emit)
		}
	}
	extra, err := d.applyProject(ctx, conv)
	if err != nil {
		return err
	}
	dir := TurnWorkspace(conv, driver)
	var servers []dservice.MCPServer
	if d.MCPServers != nil {
		servers = d.MCPServers(convID)
	}
	// model is what this turn asks for. Empty keeps the room's stored id.
	// A refusal must not leave the rejected id stored: the next send would
	// die at the same gate and look like a healthy idle room.
	priorModel := conv.ModelID()
	if model != "" {
		conv.SetModelID(model)
	}
	// Admission through the prompt write is preparing, including the
	// session load inside the prompt. The stamp is not startedAt.
	emit(event.NewClockEvent("preparing", 0, 0))
	// An explicit Retry skips the pause once. A later automatic retry
	// of this same turn still waits.
	release := pauseReleased(ctx)
	for {
		if perr := waitAgentPause(ctx, agent.ID(), conv.ModelID(), conv.Upstream(), release); perr != nil {
			ClearAgentHold(convID)
			return perr
		}
		release = false
		before := len(conv.Messages())
		// Steering that arrives while the agent works goes straight into
		// its turn when the agent takes it; what it does not take stays
		// queued for the follow-up prompt below. Delivery settles the
		// receipt: the agent has consumed the input, and it is written
		// with the reply.
		live, stopLive := context.WithCancel(ctx)
		var steering sync.WaitGroup
		var started func()
		if steers != nil {
			ready := make(chan struct{})
			started = func() { close(ready) }
			steering.Go(func() {
				// Steering is meaningful only once the prompt is on the
				// wire; before that an attempt could not reach a running
				// turn anyway, and a yield could not name its target.
				select {
				case <-live.Done():
					return
				case <-ready:
				}
				for {
					select {
					case <-live.Done():
						return
					case <-steers.arrived():
					}
					if live.Err() != nil {
						return
					}
					entries := steers.claim()
					for i, in := range entries {
						settle := func() {
							if in.receipt != nil {
								in.receipt.markPersisted()
							}
						}
						recordID := ""
						if in.record != nil {
							recordID = in.record.ID()
						} else {
							// The mailbox names the fold for an input with no line
							// of its own: the id it was submitted under is what the
							// wire's turn.steer frame carries, so the live bubble
							// and the folded segment keep one id.
							recordID = in.input.InputID
						}
						if !d.AgentRuntime.Steer(live, convID, in.input, recordID, settle) {
							steers.requeue(entries[i:])
							// Leave subsequent input pending behind the
							// first missed steer — a later attempt must not
							// jump the queue — and join this yield before
							// starting another prompt on the session, so a
							// delayed cancel can never target it.
							d.AgentRuntime.Yield(live, convID)
							return
						}
					}
				}
			})
		}
		attempted := conv.ModelID()
		err := d.AgentRuntime.Prompt(ctx, dservice.AgentPrompt{Agent: agent, Conversation: conv, Workspace: dir, Input: input, Model: attempted, ExtraFolders: extra, MCPServers: servers, Options: agentOptions(agent, conv), Elicit: elicit, OnStarted: started, Origin: origin}, emit, approve)
		stopLive()
		steering.Wait()
		if err != nil && modelNotOffered(err) {
			if model != "" && priorModel != model {
				conv.SetModelID(priorModel)
			} else {
				conv.SetModelID("")
			}
			if !assistantSince(conv, before) {
				text := apperror.As(err).Error()
				if attempted != "" {
					text = text + " (" + attempted + ")"
				}
				if msg, merr := entity.NewMessage(enum.RoleAssistant, text); merr == nil {
					conv.Append(msg)
				}
			}
		}
		if class, ok := apperror.AgentClass(err); ok && class == apperror.AgentClassRateLimited {
			var delay time.Duration
			var delayed interface{ RetryAfter() time.Duration }
			if errors.As(err, &delayed) {
				delay = delayed.RetryAfter()
			}
			wait, retry := rateLimitWait(delay, rateAttempts, lastUpdate != 0)
			pauseAgent(agent.ID(), attempted, conv.Upstream(), wait)
			if retry {
				rateAttempts++
				hold, _ := HoldFrom(err)
				hold.Retry = "auto"
				if delay == 0 {
					hold.RetryAt = 0
				}
				NoteAgentHold(convID, hold)
				emit(event.NewStatusEvent(enum.TurnStatusRetryWait, wait))
				continue
			}
		}
		// The stored word is what a later list reads. A refusal must say
		// failed, and a stop must say interrupted — neither is a completed turn.
		conv.SetTerminal(turnTerminal(err, conv, before))
		// The runtime appends the reply even when the turn broke off —
		// partial output is history the owner already watched stream.
		persistCtx := ctx
		if ctx.Err() != nil {
			persistCtx = context.WithoutCancel(ctx)
		}
		for _, m := range conv.Messages()[before:] {
			if _, perr := d.Convs.AppendMessage(persistCtx, convID, m); perr != nil {
				return perr
			}
		}
		if perr := d.Convs.Save(persistCtx, conv); perr != nil {
			return perr
		}
		if err != nil {
			rememberAgentHold(convID, err)
			return err
		}
		ClearAgentHold(convID)
		lastUpdate = 0
		rateAttempts = 0
		if steers == nil {
			return nil
		}
		claimed := steers.claim()
		if len(claimed) == 0 {
			return nil
		}
		// One accepted message is one line, forever. An input the accept path
		// already persisted keeps that line: the follow-up prompts with it
		// instead of writing a second one for the same words.
		var recorded, plain []steeringEntry
		for _, e := range claimed {
			if e.record != nil {
				recorded = append(recorded, e)
			} else {
				plain = append(plain, e)
			}
		}
		next, err := followUpMessage(claimed, plain)
		if err != nil {
			return err
		}
		committed := true
		if next != nil {
			conv.Append(*next)
			committed, err = d.Convs.AppendMessage(ctx, convID, *next)
			if err != nil {
				return err
			}
		}
		for _, in := range claimed {
			if in.receipt != nil && committed {
				in.receipt.markPersisted()
			}
		}
		for _, in := range claimed {
			if in.record != nil {
				emit(event.NewRecordedSteerEvent(in.input.Text, in.record.ID(), in.input.Attachments...))
				continue
			}
			emit(steerEvent(in))
		}
		// The prompt carries every text: the relayed ones live in their own
		// records, and a join of plain inputs is the line written above. With
		// nothing but records, the newest record is the prompt itself.
		switch {
		case len(recorded) == 0:
			input = *next
		case len(plain) == 0 && len(recorded) == 1:
			input = *recorded[0].record
		default:
			joined, jerr := joinedInput(claimed)
			if jerr != nil {
				return jerr
			}
			input = joined
		}
	}
}

// modelNotOffered is the identity refusal selectModel returns. Other
// failures keep the model the room asked for.
func modelNotOffered(err error) bool {
	ae := apperror.As(err)
	return ae.StatusCode() == 400 && ae.Error() == "the agent does not offer this model"
}

// assistantSince reports whether the turn already appended an assistant line.
func assistantSince(conv *aggregate.Conversation, before int) bool {
	for _, m := range conv.Messages()[before:] {
		if m.Role() == enum.RoleAssistant {
			return true
		}
	}
	return false
}

// turnTerminal is the word a list reads after this prompt. A stop or a
// restart is interrupted. Any other error is failed. A prompt that
// returned is completed, unless the reply it kept was itself cut short.
func turnTerminal(err error, conv *aggregate.Conversation, before int) string {
	if interruptedTurn(err) || assistantInterrupted(conv, before) {
		return "interrupted"
	}
	if err != nil {
		return "failed"
	}
	return "completed"
}

func interruptedTurn(err error) bool {
	if err == nil {
		return false
	}
	if apperror.As(err).StatusCode() == 408 {
		return true
	}
	return errors.Is(err, dservice.ErrInterruptedByUser) || errors.Is(err, dservice.ErrServerRestarted) || errors.Is(err, context.Canceled)
}

func assistantInterrupted(conv *aggregate.Conversation, before int) bool {
	for _, m := range conv.Messages()[before:] {
		if m.Role() == enum.RoleAssistant && m.FinishReason() == enum.FinishReasonInterrupted {
			return true
		}
	}
	return false
}

// followUpMessage answers the line a follow-up writes, nil when everything the
// turn could not deliver already has one. Only inputs without a record join it —
// a relayed prompt is never written twice.
func followUpMessage(_, plain []steeringEntry) (*entity.Message, error) {
	if len(plain) == 0 {
		return nil, nil
	}
	next, err := joinedInput(plain)
	if err != nil {
		return nil, err
	}
	return &next, nil
}

// joinedInput folds inputs into one user message: texts concatenate with the
// established separator while each input's reference spans shift by the prefix
// ahead of it, and uploads and marks merge in order. A join of one keeps its
// submitting request id; a join of several has no single submitter, so it
// carries none.
func joinedInput(entries []steeringEntry) (entity.Message, error) {
	var text strings.Builder
	var nextIn valueobject.UserInput
	for i, e := range entries {
		if i > 0 {
			text.WriteString("\n\n")
		}
		offset := text.Len()
		text.WriteString(e.input.Text)
		for _, ref := range e.input.References {
			nextIn.References = append(nextIn.References, ref.Shifted(offset))
		}
		nextIn.Attachments = append(nextIn.Attachments, e.input.Attachments...)
		nextIn.Annotations = append(nextIn.Annotations, e.input.Annotations...)
	}
	nextIn.Text = text.String()
	if len(entries) == 1 {
		nextIn.InputID = entries[0].input.InputID
		nextIn.Relay = entries[0].input.Relay
	}
	return entity.NewUserMessage(nextIn)
}

// agentOptions are the session options a turn applies, in order: the
// agent's defaults first, each room pick replacing a default at its own
// position or appending when the agent defaults do not name it.
func agentOptions(agent *aggregate.Agent, conv *aggregate.Conversation) valueobject.ConfigSelections {
	options := slices.Clone(agent.Defaults())
	for _, pick := range conv.AgentOptions() {
		if i := slices.IndexFunc(options, func(s valueobject.ConfigSelection) bool { return s.ID == pick.ID }); i >= 0 {
			options[i].Value = pick.Value
		} else {
			options = append(options, pick)
		}
	}
	return options
}

// retireAgentSession removes the agent-side session a room no longer uses:
// the one it had before an edit rewound it, or before it moved to another
// agent or back to a provider. It runs after the turn, so the new session
// exists first, and it leaves alone the session the room holds now — an agent
// that hands back the id it already had has reused it, and deleting that would
// delete the new session. Removing a session can mean starting the agent, so
// it runs in the background and a failure costs nothing but a stale entry in
// the agent's own history.
func (d Deps) retireAgentSession(convID, agentID, sessionID string, conv *aggregate.Conversation) {
	if d.AgentRuntime == nil || d.Agents == nil || agentID == "" || sessionID == "" {
		return
	}
	if agentID == conv.AgentID() && sessionID == conv.AgentSession() {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if agent, err := d.Agents.Load(ctx, agentID); err == nil {
			d.AgentRuntime.Discard(ctx, agent, convID, sessionID)
		}
	}()
}
