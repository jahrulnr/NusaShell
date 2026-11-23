// Package websearch is the WebUI boundary of the web tools' settings: the
// owner reads and edits them, stores a source's key, and tests a search
// or fetch. Key values travel in only.
package websearch

import (
	"context"
	"encoding/json"

	"github.com/gin-gonic/gin"

	appwebsearch "github.com/jahrulnr/nusashell/application/websearch"
	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/internal/transport"
)

// Service is the web search settings use case. *appwebsearch.App
// satisfies it.
type Service interface {
	Status(ctx context.Context) (appwebsearch.Status, error)
	Update(ctx context.Context, values map[string]json.RawMessage) (appwebsearch.Status, error)
	SetKey(ctx context.Context, source, value string) (appwebsearch.Status, error)
	ClearKey(ctx context.Context, source string) (appwebsearch.Status, error)
	Test(ctx context.Context, in appwebsearch.TestInput) (appwebsearch.TestResult, error)
}

// Register mounts the routes under /api. A nil service registers nothing.
func Register(api *gin.RouterGroup, svc Service) {
	if svc == nil {
		return
	}
	s := &server{svc: svc}
	api.GET("/websearch", s.status)
	api.PATCH("/websearch", s.update)
	api.PUT("/websearch/keys/:source", s.setKey)
	api.DELETE("/websearch/keys/:source", s.clearKey)
	api.POST("/websearch/test", s.test)
}

type server struct{ svc Service }

func (s *server) status(c *gin.Context) {
	st, err := s.svc.Status(c.Request.Context())
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(200, st)
}

// update takes {"limit": 20, "brave.enabled": "off", ...}: keys relative
// to the websearch section, values as JSON.
func (s *server) update(c *gin.Context) {
	var body map[string]json.RawMessage
	if !transport.DecodeJSON(c, &body) {
		return
	}
	st, err := s.svc.Update(c.Request.Context(), body)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(200, st)
}

func (s *server) setKey(c *gin.Context) {
	var body struct {
		Value string `json:"value"`
	}
	if !transport.DecodeJSON(c, &body) {
		return
	}
	st, err := s.svc.SetKey(c.Request.Context(), c.Param("source"), body.Value)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(200, st)
}

func (s *server) clearKey(c *gin.Context) {
	st, err := s.svc.ClearKey(c.Request.Context(), c.Param("source"))
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(200, st)
}

func (s *server) test(c *gin.Context) {
	var body appwebsearch.TestInput
	if !transport.DecodeJSON(c, &body) {
		return
	}
	res, err := s.svc.Test(c.Request.Context(), body)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(200, res)
}
