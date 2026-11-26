// Package skill is the Skills-page HTTP boundary: the joined catalog the
// page lists, and the owner's level moves on it. The wire carries
// projections only — a move never names the ownership row, just the disk
// digest the page showed, so the owner decides on bytes they have seen.
package skill

import (
	"github.com/gin-gonic/gin"

	appskill "github.com/jahrulnr/nusashell/application/skill"
	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/internal/transport"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
)

// Deps are the three ports the Skills page reads and moves through. Nil
// fields leave the routes unregistered, so a hand-built handler keeps
// working without the feature.
type Deps struct {
	Catalog   drepo.Skills
	Ownership drepo.SkillOwnership
	Proposals drepo.SkillProposals
}

// Register mounts the routes under /api. The workspace query parameter
// selects the workspace skill root the catalog joins — empty means no
// workspace root, which is a page of the other three roots.
func Register(api *gin.RouterGroup, d Deps) {
	if d.Catalog == nil || d.Ownership == nil || d.Proposals == nil {
		return
	}
	s := &server{
		listDeps: appskill.ListDeps{Catalog: d.Catalog, Ownership: d.Ownership, Proposals: d.Proposals},
		moveDeps: appskill.MoveDeps{Catalog: d.Catalog, Ownership: d.Ownership},
	}
	api.GET("/skills", s.list)
	api.POST("/skills/:name/level", s.move)
}

type server struct {
	listDeps appskill.ListDeps
	moveDeps appskill.MoveDeps
}

// row is the wire shape of one Skills-page line: the catalog entry the
// owner reads, joined with who may change it and what waits on them. The
// digest is the whole file on disk — the value a level move must send
// back unchanged for the move to count.
type row struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
	Path        string `json:"path"`
	Digest      string `json:"digest"`
	// Level is who may change the skill now: the row's level while the row
	// describes the file on disk, user otherwise.
	Level string `json:"level"`
	// Tracked reports that an ownership row exists for the skill.
	Tracked bool `json:"tracked"`
	// Changed reports that the file was edited outside the loop since its
	// row was written; the level above is then the owner's.
	Changed bool `json:"changed"`
	// Pending counts every proposal waiting on the owner for this skill;
	// PendingStale counts those the owner can no longer accept.
	Pending      int `json:"pending"`
	PendingStale int `json:"pendingStale"`
}

// list answers the page: every live catalog row joined with its effective
// level and pending count, the catalog's own diagnostics and one for each
// ownership row whose file is gone, and the total of proposals waiting on the
// owner. The join is one pass whatever the tree
// holds; the transport does not page it, because the catalog is bounded by
// the byte budget the loop enforces. Reading the page writes nothing.
func (s *server) list(c *gin.Context) {
	page, err := appskill.List(c.Request.Context(), s.listDeps, c.Query("workspace"))
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	rows := make([]row, len(page.Rows))
	for i, r := range page.Rows {
		rows[i] = row{
			Name:         r.Name,
			Description:  r.Description,
			Source:       r.Source,
			Path:         r.Path,
			Digest:       r.Digest,
			Level:        r.Level,
			Tracked:      r.Tracked,
			Changed:      r.Changed,
			Pending:      r.Pending,
			PendingStale: r.PendingStale,
		}
	}
	diagnostics := page.Diagnostics
	if diagnostics == nil {
		diagnostics = []string{}
	}
	c.JSON(200, gin.H{"data": rows, "diagnostics": diagnostics, "pending": page.Pending, "pendingStale": page.PendingStale})
}

// move records one owner decision: user takes the skill back, agent hands
// it to the loop. The body carries the level and the digest the page
// loaded; a file that moved since answers 409 in the owner's words, and
// the row the page read is never part of the wire.
func (s *server) move(c *gin.Context) {
	var body struct {
		Level  string `json:"level"`
		Digest string `json:"digest"`
	}
	if !transport.DecodeJSON(c, &body) {
		return
	}
	in := appskill.MoveInput{
		Name:      c.Param("name"),
		Level:     body.Level,
		Digest:    body.Digest,
		Workspace: c.Query("workspace"),
	}
	if err := appskill.MoveLevel(c.Request.Context(), s.moveDeps, in); err != nil {
		transport.RespondError(c, err)
		return
	}
	c.Status(204)
}
