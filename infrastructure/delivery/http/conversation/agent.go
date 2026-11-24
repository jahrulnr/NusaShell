package conversation

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/internal/transport"
	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// AgentSurface is a room's agent session: its options and approval rules.
// *appconversation.ConversationApp satisfies it.
type AgentSurface interface {
	AgentOptions(ctx context.Context, convID string) ([]dservice.AgentOption, error)
	SetAgentOption(ctx context.Context, convID, id, value string) error
	ApprovalRules(ctx context.Context, convID string) ([]aggregate.ApprovalRule, error)
	DeleteApprovalRule(ctx context.Context, convID, id string) error
}

func registerAgent(api *gin.RouterGroup, svc AgentSurface) {
	a := &agentServer{svc: svc}
	api.GET("/conversations/:id/agent-options", a.options)
	api.PUT("/conversations/:id/agent-options", a.setOption)
	api.GET("/conversations/:id/approval-rules", a.rules)
	api.DELETE("/conversations/:id/approval-rules/:rule", a.deleteRule)
}

type agentServer struct{ svc AgentSurface }

func (a *agentServer) options(c *gin.Context) {
	opts, err := a.svc.AgentOptions(c.Request.Context(), c.Param("id"))
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": opts})
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
	if err := a.svc.SetAgentOption(c.Request.Context(), c.Param("id"), body.ID, body.Value); err != nil {
		transport.RespondError(c, err)
		return
	}
	a.options(c)
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
