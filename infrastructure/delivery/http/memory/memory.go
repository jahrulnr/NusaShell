// Package memory is the WebUI memory boundary: the owner lists, searches,
// writes, verifies, marks stale, forgets, restores, and deletes memories,
// and reviews the memories suggested from compacted threads.
package memory

import (
	"context"

	"github.com/gin-gonic/gin"

	appmemory "github.com/jahrulnr/nusashell/application/memory"
	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/internal/transport"
)

// Service is the memory use case. *appmemory.App satisfies it.
type Service interface {
	List(ctx context.Context, f appmemory.Filter) ([]appmemory.View, error)
	Create(ctx context.Context, in appmemory.OwnerInput) (appmemory.View, error)
	Update(ctx context.Context, id string, in appmemory.OwnerInput) (appmemory.View, error)
	Act(ctx context.Context, id, action, reason string) (appmemory.View, error)
	Delete(ctx context.Context, id string) error
	Proposals(ctx context.Context, status string, limit int) ([]appmemory.ProposalView, int, error)
	DecideProposal(ctx context.Context, id, action string, edit *appmemory.ProposalEdit) (appmemory.ProposalView, error)
}

// Register mounts the routes under /api. A nil service registers nothing.
func Register(api *gin.RouterGroup, svc Service) {
	if svc == nil {
		return
	}
	s := &server{svc: svc}
	api.GET("/memories", s.list)
	api.POST("/memories", s.create)
	api.PUT("/memories/:id", s.update)
	api.POST("/memories/:id/actions", s.act)
	api.DELETE("/memories/:id", s.delete)
	api.GET("/memory-suggestions", s.proposals)
	api.POST("/memory-suggestions/:id/actions", s.decide)
}

type server struct{ svc Service }

// list answers one page of the memories matching the filter, newest
// first — or most relevant first with q.
func (s *server) list(c *gin.Context) {
	offset, limit, ok := transport.ParsePageParams(c, 50, 200)
	if !ok {
		return
	}
	all, err := s.svc.List(c.Request.Context(), appmemory.Filter{
		Scope: c.Query("scope"), Type: c.Query("type"), Status: c.Query("status"), Query: c.Query("q"),
	})
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(200, gin.H{
		"data": transport.SlicePage(all, offset, limit),
		"page": transport.PageMeta{Offset: offset, Limit: limit, Total: len(all), HasMore: offset+limit < len(all)},
	})
}

func (s *server) create(c *gin.Context) {
	var body appmemory.OwnerInput
	if !transport.DecodeJSON(c, &body) {
		return
	}
	v, err := s.svc.Create(c.Request.Context(), body)
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
	var body appmemory.OwnerInput
	if !transport.DecodeJSON(c, &body) {
		return
	}
	v, err := s.svc.Update(c.Request.Context(), id, body)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(200, v)
}

func (s *server) act(c *gin.Context) {
	id, ok := transport.PathID(c)
	if !ok {
		return
	}
	var body struct {
		Action string `json:"action"`
		Reason string `json:"reason"`
	}
	if !transport.DecodeJSON(c, &body) {
		return
	}
	v, err := s.svc.Act(c.Request.Context(), id, body.Action, body.Reason)
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

// proposals answers one page of suggestions — "pending" (the default) or
// "decided" — and how many wait for the owner.
func (s *server) proposals(c *gin.Context) {
	offset, limit, ok := transport.ParsePageParams(c, 50, 200)
	if !ok {
		return
	}
	all, pending, err := s.svc.Proposals(c.Request.Context(), c.Query("status"), 0)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(200, gin.H{
		"data":    transport.SlicePage(all, offset, limit),
		"page":    transport.PageMeta{Offset: offset, Limit: limit, Total: len(all), HasMore: offset+limit < len(all)},
		"pending": pending,
	})
}

// decide accepts or dismisses a suggestion; an accept may carry the
// owner's edit.
func (s *server) decide(c *gin.Context) {
	id, ok := transport.PathID(c)
	if !ok {
		return
	}
	var body struct {
		Action string                  `json:"action"`
		Edit   *appmemory.ProposalEdit `json:"edit"`
	}
	if !transport.DecodeJSON(c, &body) {
		return
	}
	v, err := s.svc.DecideProposal(c.Request.Context(), id, body.Action, body.Edit)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(200, v)
}
