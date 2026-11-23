package appconversation

import (
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Approve answers a permission request an agent raised in convID with one
// of the options the request offered. It does not go through the room's
// worker: the turn that raised the request is the job running there, and
// it is waiting for exactly this answer.
func (app *ConversationApp) Approve(convID, requestID, optionID string) error {
	approvals := app.depsSnapshot().Approvals
	if approvals == nil {
		return apperror.New(503, "agent runtime unavailable", nil)
	}
	return approvals.Answer(convID, requestID, optionID)
}

// PendingApprovals lists the permission requests waiting in convID — what a
// reconnecting client renders as open questions.
func (app *ConversationApp) PendingApprovals(convID string) []valueobject.ApprovalRequest {
	approvals := app.depsSnapshot().Approvals
	if approvals == nil {
		return nil
	}
	return approvals.Pending(convID)
}
