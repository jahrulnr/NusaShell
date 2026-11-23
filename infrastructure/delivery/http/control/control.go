// Package control is the WebUI boundary for the cross-thread activity
// log — the record of which thread called which conversation tool.
package control

import (
	"github.com/gin-gonic/gin"

	appcontrol "github.com/jahrulnr/nusashell/application/control"
	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/internal/transport"
)

// Service is the surface this boundary needs — the activity ring, plus
// Clear for the delete-all purge. *appcontrol.App satisfies it.
type Service interface {
	Activity(f appcontrol.ActivityFilter) []appcontrol.ActivityEntry
	Clear()
}

// Deps wires the use case; Activity is optional — nil leaves the route
// unregistered.
type Deps struct {
	Activity Service
}

// Register mounts this boundary's routes under /api.
func Register(api *gin.RouterGroup, d Deps) {
	if d.Activity == nil {
		return
	}
	s := &server{activity: d.Activity}
	api.GET("/conversation-control/activity", s.list)
}

type server struct {
	activity Service
}

// list answers GET /api/conversation-control/activity with entries
// newest-first; ?caller= and ?target= filter on thread ids, ?limit= caps
// the page (default 50, max the ring's capacity).
func (s *server) list(c *gin.Context) {
	_, limit, ok := transport.ParsePageParams(c, 50, 500)
	if !ok {
		return
	}
	entries := s.activity.Activity(appcontrol.ActivityFilter{
		Caller: c.Query("caller"),
		Target: c.Query("target"),
		Limit:  limit,
	})
	c.JSON(200, gin.H{"entries": entries})
}
