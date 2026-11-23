// Package notify is the WebUI boundary for desktop notifications: their
// state on this machine and a test notification.
package notify

import (
	"context"

	"github.com/gin-gonic/gin"

	appnotify "github.com/jahrulnr/nusashell/application/notify"
	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/internal/transport"
)

// Service is the notification use case. *appnotify.App satisfies it.
type Service interface {
	Status() appnotify.Status
	Test(ctx context.Context) error
}

// Register mounts the routes under /api. A nil service registers nothing.
func Register(api *gin.RouterGroup, svc Service) {
	if svc == nil {
		return
	}
	api.GET("/notifications", func(c *gin.Context) { c.JSON(200, svc.Status()) })
	api.POST("/notifications/test", func(c *gin.Context) {
		if err := svc.Test(c.Request.Context()); err != nil {
			transport.RespondError(c, err)
			return
		}
		c.Status(204)
	})
}
