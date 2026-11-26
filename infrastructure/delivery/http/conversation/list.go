package conversation

import (
	"slices"

	"github.com/gin-gonic/gin"

	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"

	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/internal/transport"
)

// Pagination bounds from the contract: list pages default to 30 (50 for
// history windows), capped at 100.
const (
	defaultListLimit    = 30
	defaultWindowStart  = -50
	defaultWindowLength = 50
	maxLimit            = 100
)

type conversationPage struct {
	Data []conversationDTO  `json:"data"`
	Page transport.PageMeta `json:"page"`
}

type messagePage struct {
	EffectiveStart int          `json:"effectiveStart"`
	NextStart      *int         `json:"nextStart"`
	Total          int          `json:"total"`
	HasMore        bool         `json:"hasMore"`
	Messages       []messageDTO `json:"messages"`
}

func (s *server) list(c *gin.Context) {
	offset, limit, ok := transport.ParsePageParams(c, defaultListLimit, maxLimit)
	if !ok {
		return
	}
	infos, err := s.reads.List(c.Request.Context())
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	// projectId narrows the list to one project's threads, so a project
	// shows all of its threads whatever the main list has paged in.
	if pid := c.Query("projectId"); pid != "" {
		infos = slices.DeleteFunc(infos, func(i drepo.ConversationInfo) bool { return i.ProjectID != pid })
	}
	page := transport.SlicePage(infos, offset, limit)
	out := conversationPage{
		Data: make([]conversationDTO, 0, len(page)),
		Page: transport.PageMeta{Offset: offset, Limit: limit, Total: len(infos),
			HasMore: offset+len(page) < len(infos)},
	}
	for _, i := range page {
		dto := toConversation(i)
		// The live transport turn and the wait behind it ride list items
		// too — a freshly loaded sidebar marks active rooms and their
		// queues without per-room detail reads. Nil lookups leave the
		// fields off the wire.
		if s.live != nil {
			if turnID, kind, ok := s.live.ActiveTurn(i.ID); ok {
				dto.ActiveTurnID = turnID
				dto.ActiveTurnKind = kind
			}
		}
		if s.queue != nil {
			dto.Waiting = s.queue.QueueDepth(i.ID)
		}
		if s.turns != nil {
			dto.TurnActive = s.turns.TurnActive(i.ID)
		}
		// One room's unreadable pick must not blind the owner to every other room:
		// the row keeps its place and says so, and opening the room shows why.
		dto.Pending, dto.PendingUnreadable = pendingView(c.Request.Context(), s.reads, i.ID)
		out.Data = append(out.Data, dto)
	}
	c.JSON(200, out)
}
