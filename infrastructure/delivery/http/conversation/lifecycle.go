package conversation

import (
	"context"

	appconversation "github.com/jahrulnr/nusashell/application/conversation"
	appjob "github.com/jahrulnr/nusashell/application/job"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
)

// applyLifecycle writes the one lifecycle word. A live turn waiting on the
// owner is approval; any other live turn is running. With no live turn the
// stored terminal decides, and a terminal that cannot be read is unknown.
func (s *server) applyLifecycle(ctx context.Context, info drepo.ConversationInfo, dto *conversationDTO) {
	liveTurn := false
	if s.live != nil {
		_, _, liveTurn = s.live.ActiveTurn(info.ID)
	}
	live := liveTurn || (s.turns != nil && s.turns.TurnActive(info.ID))
	waiting := false
	if liveTurn && s.approvals != nil && len(s.approvals.PendingApprovals(info.ID)) > 0 {
		waiting = true
	}
	if liveTurn && s.elicitations != nil && len(s.elicitations.PendingElicitations(info.ID)) > 0 {
		waiting = true
	}
	terminal, readable := s.readTerminal(ctx, info)
	dto.Lifecycle = appconversation.Lifecycle(live, waiting, readable, terminal)
	if hold, ok := appjob.CurrentAgentHold(info.ID); ok {
		dto.AgentClass = hold.Class
		dto.Retry = hold.Retry
		dto.AgentMessage = hold.Message
		if hold.RetryAt > 0 {
			at := hold.RetryAt
			dto.RetryAt = &at
		}
	}
}

func (s *server) readTerminal(ctx context.Context, info drepo.ConversationInfo) (string, bool) {
	if info.MessageCount == 0 {
		return appconversation.ResolveTerminal(info.Terminal, nil, nil)
	}
	if s.reads == nil {
		return "", false
	}
	start := info.MessageCount - 8
	if start < 0 {
		start = 0
	}
	msgs, err := s.reads.History(ctx, info.ID, start, info.MessageCount)
	// A stored word is not a readable terminal when the tail it names is
	// gone. An empty window against a positive count is that loss; Load
	// reports it, and a list must not call the room idle.
	if err != nil || len(msgs) == 0 {
		return "", false
	}
	return appconversation.ResolveTerminal(info.Terminal, msgs, nil)
}
