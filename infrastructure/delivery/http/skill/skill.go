// Package skill is the Skills-page HTTP boundary: the joined catalog the
// page lists, and the owner's level moves on it. The wire carries
// projections only — a move never names the ownership row, just the disk
// digest the page showed, so the owner decides on bytes they have seen.
package skill

import (
	"time"

	"github.com/gin-gonic/gin"

	appskill "github.com/jahrulnr/nusashell/application/skill"
	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/internal/transport"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
)

// Deps are the ports the Skills page reads, moves, and decides through. Every
// one is required: a nil catalog, ownership, proposals, decisions, writer, or
// folders leaves every route unregistered, so a surface missing a port never
// answers half-wired. Folders is the other workspace folders a name must be
// free in: the page must see them, and a test that means one workspace passes
// an explicit empty source (oneWorkspace), never nil. Budget is the catalog
// byte budget an accept re-checks. Log receives the decision failures that
// leave the file and the database apart.
type Deps struct {
	Catalog   drepo.Skills
	Ownership drepo.SkillOwnership
	Proposals drepo.SkillProposals
	Decisions drepo.SkillDecisions
	Writer    drepo.SkillWriter
	Log       drepo.Logger
	Budget    int
	// Folders lists the other workspace folders a name must be free in. It is
	// required: a page that cannot see them would take a name that another
	// workspace already holds.
	Folders appskill.SkillFolders
}

// Register mounts the routes under /api. The workspace query parameter
// selects the workspace skill root the catalog joins — empty means no
// workspace root, which is a page of the other three roots.
func Register(api *gin.RouterGroup, d Deps) {
	if d.Catalog == nil || d.Ownership == nil || d.Proposals == nil || d.Decisions == nil || d.Writer == nil || d.Folders == nil {
		return
	}
	s := &server{
		listDeps: appskill.ListDeps{Catalog: d.Catalog, Ownership: d.Ownership, Proposals: d.Proposals, Folders: d.Folders, Writer: d.Writer},
		moveDeps: appskill.MoveDeps{Catalog: d.Catalog, Ownership: d.Ownership},
		decideDeps: appskill.DecideDeps{
			Proposals: d.Proposals, Decisions: d.Decisions, Ownership: d.Ownership,
			Catalog: d.Catalog, Writer: d.Writer, Log: d.Log, Budget: d.Budget, Folders: d.Folders,
		},
	}
	// The queue is not under /skills. A literal "proposals" segment there would
	// win over :name in gin, so POST /skills/proposals/level would answer 404 and
	// a learned skill named proposals could not be moved by level. Its own
	// prefix keeps the level route whole for every name.
	api.GET("/skills", s.list)
	api.POST("/skills/:name/level", s.move)
	api.GET("/skill-proposals", s.proposals)
	api.POST("/skill-proposals/:id/decide", s.decide)
}

type server struct {
	listDeps   appskill.ListDeps
	moveDeps   appskill.MoveDeps
	decideDeps appskill.DecideDeps
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
// ownership row whose file no longer reads as a skill, and the total of proposals waiting on the
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

// proposalRow is one pending proposal on the wire. Open is whether the owner
// can still accept it, by the same rule the page uses to label a row stale.
// StaleCause says why it cannot: one sentence, empty while open.
type proposalRow struct {
	ID          string           `json:"id"`
	Op          string           `json:"op"`
	Kind        string           `json:"kind"`
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Target      string           `json:"target"`
	Tool        string           `json:"tool"`
	Reason      string           `json:"reason"`
	Evidence    proposalEvidence `json:"evidence"`
	CreatedAt   time.Time        `json:"createdAt"`
	Open        bool             `json:"open"`
	StaleCause  string           `json:"staleCause"`
}

// proposalEvidence is what the proposal rests on: its kind, the owner's words
// or the call, turns, or rewrite it cites, and when that happened. Date is null
// when the turn carried no time.
type proposalEvidence struct {
	Kind  string     `json:"kind"`
	Quote string     `json:"quote"`
	Date  *time.Time `json:"date"`
}

// proposals lists what is waiting on the owner. A proposal the owner can no
// longer accept stays in the list with open false, so the queue has a door
// even when accept would refuse. Reading writes nothing.
func (s *server) proposals(c *gin.Context) {
	pending, err := appskill.ListPending(c.Request.Context(), s.listDeps, c.Query("workspace"))
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	rows := make([]proposalRow, len(pending))
	for i, p := range pending {
		row := proposalRow{
			ID: p.ID, Op: p.Op, Kind: p.Kind, Name: p.Name, Description: p.Description,
			Target: p.Target, Tool: p.Tool, Reason: p.Reason,
			Evidence:   proposalEvidence{Kind: p.EvidenceKind, Quote: p.Evidence},
			CreatedAt:  p.CreatedAt,
			Open:       p.Open,
			StaleCause: p.StaleCause,
		}
		if !p.EvidenceAt.IsZero() {
			at := p.EvidenceAt
			row.Evidence.Date = &at
		}
		rows[i] = row
	}
	c.JSON(200, gin.H{"data": rows})
}

// decide carries out one owner decision. Accept writes the skill. Dismiss
// records only the decision, and it needs a reason: that reason is how the
// owner teaches the loop. A proposal that was already decided answers 409.
func (s *server) decide(c *gin.Context) {
	id, ok := transport.PathID(c)
	if !ok {
		return
	}
	var body struct {
		Action string `json:"action"`
		Level  string `json:"level"`
		Reason string `json:"reason"`
	}
	if !transport.DecodeJSON(c, &body) {
		return
	}
	err := appskill.Decide(c.Request.Context(), s.decideDeps, appskill.DecideInput{
		ID: id, Action: body.Action, Level: body.Level, Reason: body.Reason,
		Workspace: c.Query("workspace"), Now: time.Now(),
	})
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.Status(204)
}
