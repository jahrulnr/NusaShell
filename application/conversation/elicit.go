package appconversation

import (
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// AnswerElicitation answers an agent's request for input in convID, like
// Approve: the turn that raised it is waiting for exactly this answer.
func (app *ConversationApp) AnswerElicitation(convID, id string, a valueobject.ElicitationAnswer) error {
	elicitations := app.depsSnapshot().Elicitations
	if elicitations == nil {
		return apperror.New(503, "agent runtime unavailable", nil)
	}
	return elicitations.Answer(convID, id, a)
}

// PendingElicitations lists the requests for input waiting in convID.
func (app *ConversationApp) PendingElicitations(convID string) []valueobject.Elicitation {
	elicitations := app.depsSnapshot().Elicitations
	if elicitations == nil {
		return nil
	}
	return elicitations.Pending(convID)
}
