package job

import (
	"context"
	"maps"
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
func (d Deps) runAgentTurn(ctx context.Context, convID string, conv *aggregate.Conversation, driver EnvironmentDriver, input entity.Message, onEvent event.TurnEventHandler, steers *SteeringInput) error {
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
	var mu sync.Mutex
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
	var elicit dservice.Elicitor
	if d.Elicitations != nil {
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
	for {
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
						if !d.AgentRuntime.Steer(live, convID, in.input, settle) {
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
		err := d.AgentRuntime.Prompt(ctx, dservice.AgentPrompt{Agent: agent, Conversation: conv, Workspace: dir, Input: input, Model: conv.ModelID(), ExtraFolders: extra, MCPServers: servers, Options: agentOptions(agent, conv), Elicit: elicit, OnStarted: started}, emit, approve)
		stopLive()
		steering.Wait()
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
			return err
		}
		if steers == nil {
			return nil
		}
		claimed := steers.claim()
		if len(claimed) == 0 {
			return nil
		}
		// One joined follow-up: texts concatenate with the established
		// separator while each input's reference spans shift by the
		// prefix ahead of it, and uploads and marks merge in order.
		var text strings.Builder
		var nextIn valueobject.UserInput
		for i, e := range claimed {
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
		next, err := entity.NewUserMessage(nextIn)
		if err != nil {
			return err
		}
		conv.Append(next)
		committed, err := d.Convs.AppendMessage(ctx, convID, next)
		for _, in := range claimed {
			if in.receipt != nil && committed {
				in.receipt.markPersisted()
			}
		}
		if err != nil {
			return err
		}
		for _, in := range claimed {
			emit(event.NewSteerEvent(in.input.Text, in.input.Attachments...))
		}
		input = next
	}
}

// agentOptions are the session options a turn applies: the agent's defaults,
// overridden by what the owner picked for this room.
func agentOptions(agent *aggregate.Agent, conv *aggregate.Conversation) map[string]string {
	options := agent.Defaults()
	if options == nil {
		options = map[string]string{}
	}
	maps.Copy(options, conv.AgentOptions())
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
