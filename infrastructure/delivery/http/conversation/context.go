package conversation

import (
	"github.com/gin-gonic/gin"

	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/internal/transport"
)

// context answers the composer footer's "~used/total Tokens" line. The
// conversation is optional — a fresh, never-persisted room still gets
// the model's capacity half when providerId+modelId name a known model. A
// room bound to an agent answers with the agent's own report.
func (s *server) context(c *gin.Context) {
	stats, err := s.reads.ContextFooterStats(
		c.Request.Context(),
		c.Query("conversationId"),
		c.Query("providerId"),
		c.Query("modelId"),
	)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	out := gin.H{
		"estimate": stats.Estimate,
		"total":    stats.Total,
	}
	// An agent room reports its own figures: tokens held now, and a cost
	// when the agent prices its work.
	if !stats.Agent.IsZero() {
		out["live"] = stats.Agent.Used()
		if amount, currency, ok := stats.Agent.Cost(); ok {
			out["cost"] = gin.H{"amount": amount, "currency": currency}
		}
	}
	c.JSON(200, out)
}
