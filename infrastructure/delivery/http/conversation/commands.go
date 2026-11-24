package conversation

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	appconversation "github.com/jahrulnr/nusashell/application/conversation"
	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/internal/transport"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
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

// project finishes a room projection with the read-side facts a client paints
// its header with: whether a manual fold would do anything, and the live turn
// this process's hub holds for the room.
func (s *server) project(ctx context.Context, info drepo.ConversationInfo) (conversationDTO, error) {
	available, err := s.reads.CanCompact(ctx, info.ID)
	if err != nil {
		return conversationDTO{}, err
	}
	out := toConversation(info)
	out.CanCompact = &available
	if s.live != nil {
		if turnID, kind, ok := s.live.ActiveTurn(info.ID); ok {
			out.ActiveTurnID = turnID
			out.ActiveTurnKind = kind
		}
	}
	return out, nil
}
