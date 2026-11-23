package provider

import (
	"github.com/gin-gonic/gin"

	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/internal/transport"
)

// Pagination bounds from the contract.
const (
	defaultListLimit = 30
	maxLimit         = 100
)

type providerPage struct {
	Data []providerDTO      `json:"data"`
	Page transport.PageMeta `json:"page"`
}

func (s *server) list(c *gin.Context) {
	offset, limit, ok := transport.ParsePageParams(c, defaultListLimit, maxLimit)
	if !ok {
		return
	}
	infos, err := s.reads.List(c.Request.Context())
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	page := transport.SlicePage(infos, offset, limit)
	out := providerPage{
		Data: make([]providerDTO, 0, len(page)),
		Page: transport.PageMeta{Offset: offset, Limit: limit, Total: len(infos),
			HasMore: offset+len(page) < len(infos)},
	}
	for _, i := range page {
		out.Data = append(out.Data, toProvider(i))
	}
	c.JSON(200, out)
}

func (s *server) get(c *gin.Context) {
	id, ok := transport.PathID(c)
	if !ok {
		return
	}
	info, err := s.reads.Get(c.Request.Context(), id)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(200, toProvider(info))
}

// providers answers the picker's route layer: the upstream backends the
// provider can serve the named model through. ?model= is required — the
// endpoint set is per-model.
func (s *server) upstreams(c *gin.Context) {
	id, ok := transport.PathID(c)
	if !ok {
		return
	}
	modelID := c.Query("model")
	if modelID == "" {
		transport.WriteProblem(c, 400, "invalid_argument", "query parameter model is required")
		return
	}
	upstreams, err := s.reads.DiscoverUpstreams(c.Request.Context(), id, modelID)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	type upstreamDTO struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	out := make([]upstreamDTO, 0, len(upstreams))
	for _, p := range upstreams {
		out = append(out, upstreamDTO{Name: p.Name, Slug: p.Slug})
	}
	c.JSON(200, gin.H{"data": out})
}
