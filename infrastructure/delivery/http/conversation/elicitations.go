package conversation

import (
	"encoding/json"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/internal/transport"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Elicitor answers the requests for input a room's agent is waiting on.
// *appconversation.ConversationApp satisfies it.
type Elicitor interface {
	AnswerElicitation(convID, id string, a valueobject.ElicitationAnswer) error
	PendingElicitations(convID string) []valueobject.Elicitation
}

type elicitations struct{ svc Elicitor }

// ElicitationDTO is a request for input waiting on the owner: a form
// (schema) or a URL to visit. The message and schema are the agent's own
// inert text.
type ElicitationDTO struct {
	ID        string          `json:"id"`
	Mode      string          `json:"mode"`
	Message   string          `json:"message,omitempty"`
	Schema    json.RawMessage `json:"schema,omitempty"`
	URL       string          `json:"url,omitempty"`
	ExpiresAt *time.Time      `json:"expiresAt,omitempty"`
}

// ToElicitationDTO projects a request for the wire; the websocket
// boundary uses the same shape.
func ToElicitationDTO(e valueobject.Elicitation) ElicitationDTO {
	dto := ElicitationDTO{ID: e.ID(), Mode: e.Mode(), Message: e.Message(), Schema: e.Schema(), URL: e.URL()}
	if d := e.Deadline(); !d.IsZero() {
		dto.ExpiresAt = &d
	}
	return dto
}

func (e *elicitations) list(c *gin.Context) {
	id, ok := transport.PathID(c)
	if !ok {
		return
	}
	out := []ElicitationDTO{}
	for _, r := range e.svc.PendingElicitations(id) {
		out = append(out, ToElicitationDTO(r))
	}
	c.JSON(200, gin.H{"data": out})
}

func (e *elicitations) answer(c *gin.Context) {
	id, ok := transport.PathID(c)
	if !ok {
		return
	}
	reqID := c.Param("elicitationId")
	if err := valueobject.ValidID(reqID); err != nil {
		transport.RespondError(c, apperror.New(400, "invalid elicitation id", err))
		return
	}
	var body valueobject.ElicitationAnswer
	if !transport.DecodeJSON(c, &body) {
		return
	}
	if err := e.svc.AnswerElicitation(id, reqID, body); err != nil {
		transport.RespondError(c, err)
		return
	}
	c.Status(204)
}
