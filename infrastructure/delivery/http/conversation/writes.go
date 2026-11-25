package conversation

import (
	"bytes"
	"encoding/json"

	"github.com/gin-gonic/gin"

	appconversation "github.com/jahrulnr/nusashell/application/conversation"
	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/internal/transport"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// annotate is PATCH: any of title, projectId ("" leaves the project),
// pinned, memoryOff, label ("" clears), and routing (an object sets it, null returns
// the room to the default); an absent field keeps its value.
func (s *server) annotate(c *gin.Context) {
	id, ok := transport.PathID(c)
	if !ok {
		return
	}
	var body struct {
		Title     *string         `json:"title"`
		ProjectID *string         `json:"projectId"`
		Pinned    *bool           `json:"pinned"`
		MemoryOff *bool           `json:"memoryOff"`
		Label     *string         `json:"label"`
		Routing   json.RawMessage `json:"routing"`
		// The model picker's selection, applied without a turn. agentId
		// names an agent (modelId is then the model it reports); otherwise
		// providerId and modelId name a provider route.
		AgentID    *string `json:"agentId"`
		ProviderID *string `json:"providerId"`
		ModelID    *string `json:"modelId"`
		// The picker's reasoning effort and aggregator pin; "default" and
		// "" return them to the model's and the provider's own.
		Effort   *string `json:"effort"`
		Upstream *string `json:"upstream"`
	}
	if !transport.DecodeJSON(c, &body) {
		return
	}
	a := appconversation.Annotation{Title: body.Title, ProjectID: body.ProjectID, Pinned: body.Pinned, MemoryOff: body.MemoryOff, Label: body.Label}
	if body.AgentID != nil || body.ProviderID != nil || body.ModelID != nil {
		b := appconversation.RuntimeBinding{}
		if body.AgentID != nil {
			b.AgentID = *body.AgentID
		}
		if body.ProviderID != nil {
			b.ProviderID = *body.ProviderID
		}
		if body.ModelID != nil {
			b.ModelID = *body.ModelID
			if body.AgentID != nil {
				// For an agent the id is the model it reports, not a
				// provider catalog entry.
				b.AgentModel, b.ModelID = b.ModelID, ""
			}
		}
		a.Runtime = &b
	}
	if body.Effort != nil {
		e, ok := enum.ParseEffort(*body.Effort)
		if !ok && *body.Effort != "" {
			transport.WriteProblem(c, 400, "invalid_argument", "effort is not a known reasoning effort")
			return
		}
		a.Effort = &e
	}
	a.Upstream = body.Upstream
	switch raw := bytes.TrimSpace(body.Routing); {
	case len(raw) == 0:
	case bytes.Equal(raw, []byte("null")):
		a.ResetRouting = true
	default:
		var r valueobject.Routing
		if err := json.Unmarshal(raw, &r); err != nil {
			transport.WriteProblem(c, 400, "invalid_argument", "routing must be an object or null")
			return
		}
		a.Routing = &r
	}
	if a.Title == nil && a.ProjectID == nil && a.Pinned == nil && a.MemoryOff == nil && a.Label == nil && a.Routing == nil && !a.ResetRouting && a.Runtime == nil && a.Effort == nil && a.Upstream == nil {
		transport.WriteProblem(c, 400, "invalid_argument", "nothing to update — send title, projectId, pinned, memoryOff, label, routing, agentId, providerId, modelId, effort, or upstream")
		return
	}
	ctx := c.Request.Context()
	parked, err := s.writes.Annotate(ctx, id, a)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	info, err := s.reads.Info(ctx, id)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	doc, err := s.project(ctx, info)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	status := 200
	if parked {
		// The pick is taken but a turn's runtime is fixed when it starts, so
		// nothing is applied yet. The projection already carries the pending
		// object; 202 is what says it is not "done".
		status = 202
	}
	c.JSON(status, doc)
}

func (s *server) delete(c *gin.Context) {
	id, ok := transport.PathID(c)
	if !ok {
		return
	}
	// Server-lifecycle context: an abandoned request must not cancel an
	// accepted delete; server shutdown cancels it instead.
	if err := s.writes.Delete(s.jobs, id); err != nil {
		transport.RespondError(c, err)
		return
	}
	c.Status(204)
}

// deleteAll answers DELETE /api/conversations: every thread and the
// artifacts only threads own (uploads, picks, recordings, views) are
// purged. The body must be {"confirm":"DELETE"} — anything else is a
// 400 confirm_required so an accidental or scripted DELETE without the
// confirmation never empties the server.
func (s *server) deleteAll(c *gin.Context) {
	var body struct {
		Confirm string `json:"confirm"`
	}
	if !transport.DecodeJSON(c, &body) {
		return
	}
	if body.Confirm != "DELETE" {
		transport.WriteProblem(c, 400, "confirm_required", `send {"confirm":"DELETE"} to delete every conversation and its files`)
		return
	}
	// Server-lifecycle context: an abandoned request must not cancel an
	// accepted purge; server shutdown cancels it instead.
	deleted, err := s.writes.DeleteAll(s.jobs)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	// The cross-thread activity log points at threads that no longer
	// exist — clear it with the purge.
	if s.clearActivity != nil {
		s.clearActivity.Clear()
	}
	c.JSON(200, gin.H{"deleted": deleted})
}
