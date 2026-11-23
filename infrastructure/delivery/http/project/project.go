// Package project is the WebUI project boundary: list, read, create, full
// edit, pin, delete, and restore (the undo of a delete). A full edit carries the updatedAt it was based on
// so a stale tab cannot overwrite a newer folder list.
package project

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"

	appproject "github.com/jahrulnr/nusashell/application/project"
	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/internal/transport"
)

// Service is the project use case. *appproject.ProjectApp satisfies it.
type Service interface {
	List(ctx context.Context) ([]appproject.View, error)
	Get(ctx context.Context, id string) (appproject.View, error)
	Create(ctx context.Context, name string, folders []string, pinned bool) (appproject.View, error)
	Update(ctx context.Context, id, name string, folders []string, pinned bool, readAt time.Time) (appproject.View, error)
	SetPinned(ctx context.Context, id string, pinned bool) (appproject.View, error)
	Delete(ctx context.Context, id string) ([]string, error)
	Restore(ctx context.Context, id, name string, folders []string, pinned bool, roomIDs []string) (appproject.View, []string, error)
}

// Register mounts the routes under /api. A nil service registers nothing.
func Register(api *gin.RouterGroup, svc Service) {
	if svc == nil {
		return
	}
	s := &server{svc: svc}
	api.GET("/projects", s.list)
	api.POST("/projects", s.create)
	api.GET("/projects/:id", s.get)
	api.PUT("/projects/:id", s.update)
	api.PATCH("/projects/:id", s.pin)
	api.DELETE("/projects/:id", s.delete)
	api.POST("/projects/:id/restore", s.restore)
}

type server struct{ svc Service }

func (s *server) list(c *gin.Context) {
	out, err := s.svc.List(c.Request.Context())
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(200, gin.H{"data": out})
}

func (s *server) get(c *gin.Context) {
	id, ok := transport.PathID(c)
	if !ok {
		return
	}
	v, err := s.svc.Get(c.Request.Context(), id)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(200, v)
}

type createBody struct {
	Name    string   `json:"name"`
	Folders []string `json:"folders"`
	Pinned  bool     `json:"pinned"`
}

func (s *server) create(c *gin.Context) {
	var body createBody
	if !transport.DecodeJSON(c, &body) {
		return
	}
	v, err := s.svc.Create(c.Request.Context(), body.Name, body.Folders, body.Pinned)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(201, v)
}

type updateBody struct {
	Name      string     `json:"name"`
	Folders   []string   `json:"folders"`
	Pinned    bool       `json:"pinned"`
	UpdatedAt *time.Time `json:"updatedAt"`
}

func (s *server) update(c *gin.Context) {
	id, ok := transport.PathID(c)
	if !ok {
		return
	}
	var body updateBody
	if !transport.DecodeJSON(c, &body) {
		return
	}
	if body.UpdatedAt == nil {
		transport.WriteProblem(c, 400, "invalid_argument", "updatedAt from the project you edited is required")
		return
	}
	v, err := s.svc.Update(c.Request.Context(), id, body.Name, body.Folders, body.Pinned, *body.UpdatedAt)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(200, v)
}

func (s *server) pin(c *gin.Context) {
	id, ok := transport.PathID(c)
	if !ok {
		return
	}
	var body struct {
		Pinned *bool `json:"pinned"`
	}
	if !transport.DecodeJSON(c, &body) {
		return
	}
	if body.Pinned == nil {
		transport.WriteProblem(c, 400, "invalid_argument", "pinned is required")
		return
	}
	v, err := s.svc.SetPinned(c.Request.Context(), id, *body.Pinned)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(200, v)
}

func (s *server) delete(c *gin.Context) {
	id, ok := transport.PathID(c)
	if !ok {
		return
	}
	released, err := s.svc.Delete(c.Request.Context(), id)
	if released == nil {
		released = []string{}
	}
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(200, gin.H{"released": released})
}

type restoreBody struct {
	Name    string   `json:"name"`
	Folders []string `json:"folders"`
	Pinned  bool     `json:"pinned"`
	// Released are the thread ids the delete answered with.
	Released []string `json:"released"`
}

func (s *server) restore(c *gin.Context) {
	id, ok := transport.PathID(c)
	if !ok {
		return
	}
	var body restoreBody
	if !transport.DecodeJSON(c, &body) {
		return
	}
	v, rejoined, err := s.svc.Restore(c.Request.Context(), id, body.Name, body.Folders, body.Pinned, body.Released)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	if rejoined == nil {
		rejoined = []string{}
	}
	c.JSON(201, gin.H{"project": v, "rejoined": rejoined})
}
