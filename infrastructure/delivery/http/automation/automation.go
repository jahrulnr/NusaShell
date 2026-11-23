// Package automation is the WebUI automation boundary: list, read, create,
// full edit, pause, delete, run history, run now, and a test message to the
// result chat. A full edit carries the updatedAt it was based on, so a
// stale tab cannot overwrite a newer schedule.
package automation

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"

	appautomation "github.com/jahrulnr/nusashell/application/automation"
	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/internal/transport"
)

// Service is the automation use case. *appautomation.App satisfies it.
type Service interface {
	Zone() string
	List(ctx context.Context) ([]appautomation.View, error)
	Get(ctx context.Context, id string) (appautomation.View, error)
	Runs(ctx context.Context, id string) ([]appautomation.RunView, error)
	Create(ctx context.Context, in appautomation.Input) (appautomation.View, error)
	Update(ctx context.Context, id string, in appautomation.Input, readAt time.Time) (appautomation.View, error)
	SetEnabled(ctx context.Context, id string, enabled bool) (appautomation.View, error)
	Delete(ctx context.Context, id string) error
	RunNow(ctx context.Context, id string) (appautomation.RunView, error)
	TestNotify(ctx context.Context, id string) error
}

// Register mounts the routes under /api. A nil service registers nothing.
func Register(api *gin.RouterGroup, svc Service) {
	if svc == nil {
		return
	}
	s := &server{svc: svc}
	api.GET("/automations", s.list)
	api.POST("/automations", s.create)
	api.GET("/automations/:id", s.get)
	api.PUT("/automations/:id", s.update)
	api.PATCH("/automations/:id", s.enable)
	api.DELETE("/automations/:id", s.delete)
	api.GET("/automations/:id/runs", s.runs)
	api.POST("/automations/:id/runs", s.runNow)
	api.POST("/automations/:id/test", s.test)
}

type server struct{ svc Service }

func (s *server) list(c *gin.Context) {
	out, err := s.svc.List(c.Request.Context())
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(200, gin.H{"data": out, "timezone": s.svc.Zone()})
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

// body is the automation document. Times are RFC 3339.
type body struct {
	Name       string              `json:"name"`
	Prompt     string              `json:"prompt"`
	AgentID    string              `json:"agentId"`
	ProviderID string              `json:"providerId"`
	ModelID    string              `json:"modelId"`
	Schedule   string              `json:"schedule"`
	RunAt      *time.Time          `json:"runAt"`
	Cron       string              `json:"cron"`
	Timezone   string              `json:"timezone"`
	Thread     string              `json:"thread"`
	ProjectID  string              `json:"projectId"`
	Notify     *appautomation.Chat `json:"notify"`
	ExpiresAt  *time.Time          `json:"expiresAt"`
	Enabled    *bool               `json:"enabled"`
	UpdatedAt  *time.Time          `json:"updatedAt"`
}

func (b body) input() appautomation.Input {
	in := appautomation.Input{Name: b.Name, Prompt: b.Prompt, AgentID: b.AgentID, ProviderID: b.ProviderID, ModelID: b.ModelID,
		Schedule: b.Schedule, Cron: b.Cron, Timezone: b.Timezone, Thread: b.Thread, ProjectID: b.ProjectID, Notify: b.Notify, Enabled: b.Enabled}
	if b.RunAt != nil {
		in.RunAt = *b.RunAt
	}
	if b.ExpiresAt != nil {
		in.ExpiresAt = *b.ExpiresAt
	}
	return in
}

func (s *server) create(c *gin.Context) {
	var b body
	if !transport.DecodeJSON(c, &b) {
		return
	}
	v, err := s.svc.Create(c.Request.Context(), b.input())
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(201, v)
}

func (s *server) update(c *gin.Context) {
	id, ok := transport.PathID(c)
	if !ok {
		return
	}
	var b body
	if !transport.DecodeJSON(c, &b) {
		return
	}
	if b.UpdatedAt == nil {
		transport.WriteProblem(c, 400, "invalid_argument", "updatedAt from the automation you edited is required")
		return
	}
	v, err := s.svc.Update(c.Request.Context(), id, b.input(), *b.UpdatedAt)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(200, v)
}

func (s *server) enable(c *gin.Context) {
	id, ok := transport.PathID(c)
	if !ok {
		return
	}
	var b struct {
		Enabled *bool `json:"enabled"`
	}
	if !transport.DecodeJSON(c, &b) {
		return
	}
	if b.Enabled == nil {
		transport.WriteProblem(c, 400, "invalid_argument", "enabled is required")
		return
	}
	v, err := s.svc.SetEnabled(c.Request.Context(), id, *b.Enabled)
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
	if err := s.svc.Delete(c.Request.Context(), id); err != nil {
		transport.RespondError(c, err)
		return
	}
	c.Status(204)
}

func (s *server) runs(c *gin.Context) {
	id, ok := transport.PathID(c)
	if !ok {
		return
	}
	out, err := s.svc.Runs(c.Request.Context(), id)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(200, gin.H{"data": out})
}

func (s *server) runNow(c *gin.Context) {
	id, ok := transport.PathID(c)
	if !ok {
		return
	}
	run, err := s.svc.RunNow(c.Request.Context(), id)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(202, run)
}

func (s *server) test(c *gin.Context) {
	id, ok := transport.PathID(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	if err := s.svc.TestNotify(ctx, id); err != nil {
		transport.RespondError(c, err)
		return
	}
	c.Status(204)
}
