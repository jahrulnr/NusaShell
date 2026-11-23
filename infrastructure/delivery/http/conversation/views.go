package conversation

import (
	"context"
	"encoding/json"

	"github.com/gin-gonic/gin"

	appconversation "github.com/jahrulnr/nusashell/application/conversation"
	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/internal/transport"
)

// Viewer keeps what the owner left in each thread's view — the unsent
// draft and the side pane. *appconversation.ConversationApp satisfies it.
type Viewer interface {
	View(ctx context.Context, key string) (appconversation.View, error)
	SaveView(ctx context.Context, key string, doc json.RawMessage) (appconversation.View, error)
}

// registerViews mounts a thread's view and the new-thread composer's.
func registerViews(api *gin.RouterGroup, svc Viewer) {
	key := func(c *gin.Context) string {
		if id := c.Param("id"); id != "" {
			return id
		}
		return appconversation.NewThreadView
	}
	get := func(c *gin.Context) {
		v, err := svc.View(c.Request.Context(), key(c))
		if err != nil {
			transport.RespondError(c, err)
			return
		}
		c.JSON(200, gin.H{"view": v})
	}
	put := func(c *gin.Context) {
		var body struct {
			Document json.RawMessage `json:"document"`
		}
		if !transport.DecodeJSON(c, &body) {
			return
		}
		v, err := svc.SaveView(c.Request.Context(), key(c), body.Document)
		if err != nil {
			transport.RespondError(c, err)
			return
		}
		c.JSON(200, gin.H{"view": v})
	}
	api.GET("/conversations/:id/view", get)
	api.PUT("/conversations/:id/view", put)
	api.GET("/view", get)
	api.PUT("/view", put)
}
