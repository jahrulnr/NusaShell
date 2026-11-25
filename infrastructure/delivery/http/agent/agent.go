// Package agent is the WebUI boundary for external agents: list, read,
// full-replace PUT, delete, host discovery, and the model list the picker
// shows under each agent. Environment values never cross outward — views
// carry each variable's name and stored reference only.
package agent

import (
	"context"

	"github.com/gin-gonic/gin"

	appagent "github.com/jahrulnr/nusashell/application/agent"
	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/internal/transport"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// Service is the agent management surface. *appagent.AgentApp satisfies it.
type Service interface {
	List(ctx context.Context) ([]appagent.View, error)
	Get(ctx context.Context, id string) (appagent.View, error)
	Upsert(ctx context.Context, in appagent.Input) (appagent.View, error)
	Delete(ctx context.Context, id string) error
	Detect(ctx context.Context) ([]appagent.Candidate, error)
	ImportDetected(ctx context.Context) ([]appagent.View, error)
	Models(ctx context.Context, id string) ([]appagent.Model, error)
	// Options reports the session options the agent offers — its modes and
	// config options — before any room exists.
	Options(ctx context.Context, id string) ([]dservice.AgentOption, error)
	Status(ctx context.Context, id string) (appagent.Status, error)
	Authenticate(ctx context.Context, id, methodID string) error
	Logout(ctx context.Context, id string) error
	Restart(ctx context.Context, id string) error
}

// Register mounts the routes under /api. A nil service registers nothing.
func Register(api *gin.RouterGroup, svc Service) {
	if svc == nil {
		return
	}
	s := &server{svc: svc}
	api.GET("/agents", s.list)
	api.GET("/agents/detect", s.detect)
	api.POST("/agents/import", s.importDetected)
	api.GET("/agents/:id", s.get)
	api.GET("/agents/:id/models", s.models)
	api.GET("/agents/:id/options", s.options)
	api.GET("/agents/:id/status", s.status)
	api.POST("/agents/:id/authenticate", s.authenticate)
	api.POST("/agents/:id/logout", s.act((Service).Logout))
	api.POST("/agents/:id/restart", s.act((Service).Restart))
	api.PUT("/agents/:id", s.put)
	api.DELETE("/agents/:id", s.delete)
}

type server struct{ svc Service }

type listBody[T any] struct {
	Data []T `json:"data"`
}

func (s *server) list(c *gin.Context) {
	views, err := s.svc.List(c.Request.Context())
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(200, listBody[appagent.View]{Data: views})
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

// putBody is the full agent document. Env maps names to a new secret value
// or the stored ref to keep; a name left out is removed.
type putBody struct {
	Name    string            `json:"name"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	Policy  string            `json:"policy"`
	// AutoStart keeps the agent's process running; Defaults are the session
	// options its rooms start with, by option id.
	AutoStart bool                         `json:"autoStart"`
	Defaults  valueobject.ConfigSelections `json:"defaults"`
}

func (s *server) put(c *gin.Context) {
	id, ok := transport.PathID(c)
	if !ok {
		return
	}
	var body putBody
	if !transport.DecodeJSON(c, &body) {
		return
	}
	v, err := s.svc.Upsert(c.Request.Context(), appagent.Input{
		ID: id, Name: body.Name, Command: body.Command, Args: body.Args, Env: body.Env, Policy: body.Policy,
		AutoStart: body.AutoStart, Defaults: body.Defaults,
	})
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

func (s *server) detect(c *gin.Context) {
	out, err := s.svc.Detect(c.Request.Context())
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(200, listBody[appagent.Candidate]{Data: out})
}

func (s *server) importDetected(c *gin.Context) {
	out, err := s.svc.ImportDetected(c.Request.Context())
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(200, listBody[appagent.View]{Data: out})
}

func (s *server) models(c *gin.Context) {
	id, ok := transport.PathID(c)
	if !ok {
		return
	}
	out, err := s.svc.Models(c.Request.Context(), id)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(200, listBody[appagent.Model]{Data: out})
}

// options answers the agent's own session options — modes and config —
// so the picker can show them before a thread exists. Values are the
// agent's; a room's overrides are read from the conversation instead.
func (s *server) options(c *gin.Context) {
	id, ok := transport.PathID(c)
	if !ok {
		return
	}
	out, err := s.svc.Options(c.Request.Context(), id)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(200, listBody[dservice.AgentOption]{Data: out})
}

func (s *server) status(c *gin.Context) {
	id, ok := transport.PathID(c)
	if !ok {
		return
	}
	out, err := s.svc.Status(c.Request.Context(), id)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(200, out)
}

func (s *server) authenticate(c *gin.Context) {
	id, ok := transport.PathID(c)
	if !ok {
		return
	}
	var body struct {
		MethodID string `json:"methodId"`
	}
	if !transport.DecodeJSON(c, &body) {
		return
	}
	if err := s.svc.Authenticate(c.Request.Context(), id, body.MethodID); err != nil {
		transport.RespondError(c, err)
		return
	}
	c.Status(204)
}

// act serves a bodiless POST that runs one action on the agent.
func (s *server) act(do func(Service, context.Context, string) error) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := transport.PathID(c)
		if !ok {
			return
		}
		if err := do(s.svc, c.Request.Context(), id); err != nil {
			transport.RespondError(c, err)
			return
		}
		c.Status(204)
	}
}
