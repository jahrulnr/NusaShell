package conversation

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/internal/transport"
	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// AgentSurface is a room's agent session: its options and approval rules.
// *appconversation.ConversationApp satisfies it.
type AgentSurface interface {
	AgentOptions(ctx context.Context, convID string) ([]dservice.AgentOption, dservice.OptionsState, error)
	// SetAgentOption validates the pick and parks it for the room's next turn;
	// parked is what the answer renders as 202 rather than "applied".
	SetAgentOption(ctx context.Context, convID, id, value string) (parked bool, err error)
	ApprovalRules(ctx context.Context, convID string) ([]aggregate.ApprovalRule, error)
	DeleteApprovalRule(ctx context.Context, convID, id string) error
}

// PendingReader answers the pick a room waits to apply. A surface that takes a
// pick needs it to answer with the same object the room projection carries.
type PendingReader interface {
	Pending(ctx context.Context, convID string) (drepo.PendingSelection, bool, error)
}

func registerAgent(api *gin.RouterGroup, svc AgentSurface, pending PendingReader) {
	a := &agentServer{svc: svc, pending: pending}
	api.GET("/conversations/:id/agent-options", a.options)
	// A pick's answer carries the room's pending state, which only a read
	// surface can give; a writes-only composition leaves the route off.
	if pending != nil {
		api.PUT("/conversations/:id/agent-options", a.setOption)
	}
	api.GET("/conversations/:id/approval-rules", a.rules)
	api.DELETE("/conversations/:id/approval-rules/:rule", a.deleteRule)
}

type agentServer struct {
	svc     AgentSurface
	pending PendingReader
}

func (a *agentServer) options(c *gin.Context) {
	opts, state, err := a.svc.AgentOptions(c.Request.Context(), c.Param("id"))
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	// status is only ever "ready" or "empty" here: "unavailable" always
	// travels with err and became the error response above. The HTTP
	// status already says the request failed; the body's job is to
	// describe the list — never re-add "unavailable" as a wire value.
	c.JSON(http.StatusOK, gin.H{"data": opts, "status": state})
}

func (a *agentServer) setOption(c *gin.Context) {
	var body struct {
		ID    string `json:"id"`
		Value string `json:"value"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.ID == "" {
		transport.RespondError(c, apperror.New(400, "body {id, value} is required", err))
		return
	}
	ctx := c.Request.Context()
	parked, err := a.svc.SetAgentOption(ctx, c.Param("id"), body.ID, body.Value)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	opts, state, err := a.svc.AgentOptions(ctx, c.Param("id"))
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	// The answer carries the room's pending state as the write left it: the
	// same object the room projection carries, null when nothing waits. A pick
	// that cannot be read back is null with pendingUnreadable set, so a client
	// reads "unknown" rather than "nothing waits", and the write is not an error.
	pending, unreadable := pendingView(ctx, a.pending, c.Param("id"))
	// 202 says the pick waits for the next turn. A parked pick that cannot be
	// read back still waits; a turn that took the pick in the meantime has
	// applied it, and then the answer is plain 200.
	status := http.StatusOK
	if parked && (pending != nil || unreadable) {
		status = http.StatusAccepted
	}
	answer := gin.H{"data": opts, "status": state, "pending": pending}
	if unreadable {
		answer["pendingUnreadable"] = true
	}
	c.JSON(status, answer)
}

func (a *agentServer) rules(c *gin.Context) {
	list, err := a.svc.ApprovalRules(c.Request.Context(), c.Param("id"))
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	type ruleDTO struct {
		ID        string `json:"id"`
		Action    string `json:"action"`
		CreatedAt string `json:"createdAt"`
	}
	out := []ruleDTO{}
	for _, r := range list {
		out = append(out, ruleDTO{ID: r.ID(), Action: r.Action().String(), CreatedAt: r.CreatedAt().UTC().Format("2006-01-02T15:04:05Z")})
	}
	c.JSON(http.StatusOK, gin.H{"data": out})
}

func (a *agentServer) deleteRule(c *gin.Context) {
	if err := a.svc.DeleteApprovalRule(c.Request.Context(), c.Param("id"), c.Param("rule")); err != nil {
		transport.RespondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
