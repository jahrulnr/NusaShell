// Package httpmcp is the HTTP side of the agents' MCP relay: an agent's
// `nusashell mcp run <server>` child lists and calls a built-in server's
// tools here, authorized by its thread's token instead of a login.
package httpmcp

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/internal/transport"
	deliverymcp "github.com/jahrulnr/nusashell/infrastructure/delivery/mcp"
	"github.com/jahrulnr/nusashell/infrastructure/service/mcp"
)

// Deps are the grants and the servers on offer.
type Deps struct {
	Grants   *deliverymcp.Grants
	Catalogs []deliverymcp.Catalog
}

// Register mounts GET and POST /api/mcp/:server. Nil grants leave them
// unregistered.
func Register(api *gin.RouterGroup, d Deps) {
	if d.Grants == nil {
		return
	}
	s := &server{d: d}
	api.GET("/mcp/:server", s.list)
	api.POST("/mcp/:server", s.call)
}

type server struct{ d Deps }

// resolve finds the caller's thread and the server it asks for.
func (s *server) resolve(c *gin.Context) (string, deliverymcp.Catalog, bool) {
	token, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
	convID, known := s.d.Grants.Thread(strings.TrimSpace(token))
	if !ok || !known {
		transport.WriteProblem(c, http.StatusUnauthorized, "unauthenticated", "unknown thread token")
		return "", deliverymcp.Catalog{}, false
	}
	for _, cat := range s.d.Catalogs {
		if cat.Name == c.Param("server") {
			return convID, cat, true
		}
	}
	transport.WriteProblem(c, http.StatusNotFound, "not_found", "no MCP server "+c.Param("server"))
	return "", deliverymcp.Catalog{}, false
}

func (s *server) list(c *gin.Context) {
	if _, cat, ok := s.resolve(c); ok {
		c.JSON(http.StatusOK, gin.H{"name": cat.Name, "tools": cat.Tools})
	}
}

// call runs one tool. A failed tool is a 200 with an error result, so the
// agent reads why.
func (s *server) call(c *gin.Context) {
	convID, cat, ok := s.resolve(c)
	if !ok {
		return
	}
	var body struct {
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Tool == "" {
		transport.WriteProblem(c, http.StatusBadRequest, "invalid_request", "body needs tool and arguments")
		return
	}
	res, err := cat.Call(c.Request.Context(), convID, body.Tool, body.Arguments)
	if err != nil {
		res = mcp.ErrorResult(err.Error())
	}
	if res.Content == nil {
		res.Content = []mcp.Content{}
	}
	c.JSON(http.StatusOK, res)
}
