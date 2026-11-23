// Package httpmcpserver is the HTTP boundary for the owner's MCP servers.
package httpmcpserver

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	appmcp "github.com/jahrulnr/nusashell/application/mcp"
	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/internal/transport"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Service is the use case. *appmcp.App satisfies it.
type Service interface {
	List(ctx context.Context) ([]appmcp.View, error)
	Get(ctx context.Context, id string) (appmcp.View, error)
	Create(ctx context.Context, in appmcp.Input) (appmcp.View, error)
	Update(ctx context.Context, id string, in appmcp.Input, readAt time.Time) (appmcp.View, error)
	SetEnabled(ctx context.Context, id string, on bool) (appmcp.View, error)
	Delete(ctx context.Context, id string) error
}

// Register mounts /api/mcp-servers; nil leaves it unregistered.
func Register(api *gin.RouterGroup, svc Service) {
	if svc == nil {
		return
	}
	s := &server{svc: svc}
	api.GET("/mcp-servers", s.list)
	api.POST("/mcp-servers", s.create)
	api.GET("/mcp-servers/:id", s.get)
	api.PUT("/mcp-servers/:id", s.update)
	api.PATCH("/mcp-servers/:id", s.enable)
	api.DELETE("/mcp-servers/:id", s.remove)
}

type server struct{ svc Service }

func (s *server) list(c *gin.Context) {
	list, err := s.svc.List(c.Request.Context())
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	if list == nil {
		list = []appmcp.View{}
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}

func (s *server) get(c *gin.Context) {
	v, err := s.svc.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

func (s *server) create(c *gin.Context) {
	var in appmcp.Input
	if err := c.ShouldBindJSON(&in); err != nil {
		transport.RespondError(c, apperror.New(400, "invalid body", err))
		return
	}
	v, err := s.svc.Create(c.Request.Context(), in)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, v)
}

func (s *server) update(c *gin.Context) {
	var body struct {
		appmcp.Input
		UpdatedAt string `json:"updatedAt"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		transport.RespondError(c, apperror.New(400, "invalid body", err))
		return
	}
	readAt, err := time.Parse(time.RFC3339Nano, body.UpdatedAt)
	if err != nil {
		transport.RespondError(c, apperror.New(400, "updatedAt from the server being edited is required", err))
		return
	}
	v, err := s.svc.Update(c.Request.Context(), c.Param("id"), body.Input, readAt)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

func (s *server) enable(c *gin.Context) {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Enabled == nil {
		transport.RespondError(c, apperror.New(400, "body {enabled} is required", err))
		return
	}
	v, err := s.svc.SetEnabled(c.Request.Context(), c.Param("id"), *body.Enabled)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

func (s *server) remove(c *gin.Context) {
	if err := s.svc.Delete(c.Request.Context(), c.Param("id")); err != nil {
		transport.RespondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
