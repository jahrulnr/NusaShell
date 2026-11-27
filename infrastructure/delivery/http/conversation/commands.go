package conversation

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	appconversation "github.com/jahrulnr/nusashell/application/conversation"
	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/internal/transport"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

// CommandSurface is the slash-command surface: the catalog a composer reads
// and the one endpoint that runs a native command. A command is an action, not
// a turn — it writes no message and emits no turn frame.
// *appconversation.ConversationApp satisfies it.
type CommandSurface interface {
	Commands(ctx context.Context, convID string) ([]appconversation.Command, error)
	RunCommand(ctx context.Context, convID string, in appconversation.CommandInput) (appconversation.CommandResult, error)
}

// listCommands answers GET /api/commands?conversationId=: the native catalog
// merged with the room's agent commands, native first, each with this room's
// availability. Without a conversationId every native row is off — a composer
// with no room yet reserves the names but cannot run them.
func (s *server) listCommands(c *gin.Context) {
	rows, err := s.commands.Commands(c.Request.Context(), c.Query("conversationId"))
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": rows})
}

// runCommand answers POST /api/conversations/{id}/commands with body
// {name, args, confirm}: the command's name and the room detail projection it
// leaves the caller on — the new room for a fork. Every precondition is the
// application's; a refusal carries its own stable code (busy,
// confirmation_required, nothing_to_*).
func (s *server) runCommand(c *gin.Context) {
	id, ok := transport.PathID(c)
	if !ok {
		return
	}
	var body struct {
		Name    string `json:"name"`
		Args    string `json:"args"`
		Confirm bool   `json:"confirm"`
	}
	if !transport.DecodeJSON(c, &body) {
		return
	}
	// Server-lifecycle context: a compaction, a clear, or a fork is accepted
	// work — an abandoned request must not cancel it half-way.
	res, err := s.commands.RunCommand(s.jobs, id, appconversation.CommandInput{Name: body.Name, Args: body.Args, Confirm: body.Confirm})
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	out, err := s.project(s.jobs, res.Conversation)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"command": res.Command, "conversation": out})
}

// pendingOf reads the room's waiting pick as the object every projection and
// every answer to a pick carries. It is nil when nothing waits — the wire says
// null — and it is read at the moment of the call, so a caller that reads it
// after a write answers the state the write left.
func pendingOf(ctx context.Context, reads PendingReader, convID string) (*pendingDTO, error) {
	sel, ok, err := reads.Pending(ctx, convID)
	if err != nil || !ok {
		return nil, err
	}
	return toPending(sel), nil
}

// pendingView is the waiting pick as a read answers it. A pick that cannot be
// read never fails the read: it answers nil and unreadable, and the caller says
// so on the wire. Writes that would supersede or consume the pick refuse on
// their own path instead.
func pendingView(ctx context.Context, reads PendingReader, convID string) (pending *pendingDTO, unreadable bool) {
	pending, err := pendingOf(ctx, reads, convID)
	if err != nil {
		return nil, true
	}
	return pending, false
}

// project finishes a room projection with the read-side facts a client paints
// its header with: whether a manual fold would do anything, the live turn
// this process's hub holds for the room, and how many jobs wait behind it.
func (s *server) project(ctx context.Context, info drepo.ConversationInfo) (conversationDTO, error) {
	available, err := s.reads.CanCompact(ctx, info.ID)
	if err != nil {
		// The tail would not load. The room still opens: compact is not
		// available, and the lifecycle read says the history is unknown.
		available = false
	}
	out := toConversation(info)
	out.CanCompact = &available
	if s.live != nil {
		if turnID, kind, ok := s.live.ActiveTurn(info.ID); ok {
			out.ActiveTurnID = turnID
			out.ActiveTurnKind = kind
		}
	}
	if s.queue != nil {
		out.Waiting = s.queue.QueueDepth(info.ID)
	}
	if s.turns != nil {
		out.TurnActive = s.turns.TurnActive(info.ID)
	}
	// A waiting pick that cannot be read does not fail the room either: the room
	// answers pending null and says it is unreadable, so the owner still opens it.
	out.Pending, out.PendingUnreadable = pendingView(ctx, s.reads, info.ID)
	s.applyLifecycle(ctx, info, &out)
	// The room's agent options come from the reading the picker draws, so the
	// two cannot disagree. A read that fails does not fail the room, which the
	// owner still needs: the room says it is unreadable instead, and the
	// agent-options route answers the reason.
	if info.Kind().QueriesAgentSessionOptions() {
		out.AgentOptions = s.agentOptionsOf(ctx, info.ID)
		out.AgentOptionsUnreadable = out.AgentOptions == nil
	}
	return out, nil
}

// agentOptionsOf is a room's agent options as the projection carries them: the
// agent's answer with its status, when the answer is one the wire carries. It is
// nil when there is no such answer: no agent surface composed, a read error, or
// the unavailable state, which never reaches the wire.
func (s *server) agentOptionsOf(ctx context.Context, convID string) *agentOptionsDTO {
	if s.agent == nil {
		return nil
	}
	opts, state, source, err := s.agent.AgentOptions(ctx, convID)
	if err != nil || (state != dservice.OptionsReady && state != dservice.OptionsEmpty) {
		return nil
	}
	return &agentOptionsDTO{Status: string(state), Source: string(source), Data: toAgentOptions(opts)}
}
