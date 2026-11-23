package conversation

import (
	"github.com/gin-gonic/gin"

	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/internal/transport"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

type approvals struct{ svc Approver }

// ApprovalOptionDTO is one answer a permission request offers.
type ApprovalOptionDTO struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Choice string `json:"choice"`
}

// ApprovalDTO is a permission request waiting on the owner. Input is the
// agent's own description of the call — inert display text.
type ApprovalDTO struct {
	ID      string              `json:"id"`
	CallID  string              `json:"callId,omitempty"`
	Title   string              `json:"title"`
	Input   string              `json:"input,omitempty"`
	Action  string              `json:"action"`
	Options []ApprovalOptionDTO `json:"options"`
}

// ToApprovalDTO projects a request for the wire; the websocket boundary
// uses the same shape.
func ToApprovalDTO(r valueobject.ApprovalRequest) ApprovalDTO {
	out := ApprovalDTO{ID: r.ID(), CallID: r.CallID(), Title: r.Title(), Input: r.Input(), Action: r.Action().String()}
	for _, o := range r.Options() {
		out.Options = append(out.Options, ApprovalOptionDTO{ID: o.ID, Name: o.Name, Choice: o.Choice.String()})
	}
	return out
}

func (a *approvals) list(c *gin.Context) {
	id, ok := transport.PathID(c)
	if !ok {
		return
	}
	out := []ApprovalDTO{}
	for _, r := range a.svc.PendingApprovals(id) {
		out = append(out, ToApprovalDTO(r))
	}
	c.JSON(200, gin.H{"data": out})
}

type answerBody struct {
	OptionID string `json:"optionId"`
}

func (a *approvals) answer(c *gin.Context) {
	id, ok := transport.PathID(c)
	if !ok {
		return
	}
	requestID := c.Param("requestId")
	if err := valueobject.ValidID(requestID); err != nil {
		transport.RespondError(c, apperror.New(400, "invalid approval request id", err))
		return
	}
	var body answerBody
	if !transport.DecodeJSON(c, &body) {
		return
	}
	if err := a.svc.Approve(id, requestID, body.OptionID); err != nil {
		transport.RespondError(c, err)
		return
	}
	c.Status(204)
}
