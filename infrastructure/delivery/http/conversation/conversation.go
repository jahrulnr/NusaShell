// Package conversation is the WebUI conversation boundary — the HTTP
// surface over the appconversation use cases: list/detail/history reads,
// rename/delete/compact mutations, and the message DTO projections that
// keep provider replay artifacts and attachment sha256 pins off the wire
// while ordered message parts, tool I/O, and attachment uri/name cross
// to the local owner.
package conversation

import (
	"context"

	"github.com/gin-gonic/gin"

	appconversation "github.com/jahrulnr/nusashell/application/conversation"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// Reader is the read surface this boundary needs from the conversation
// broker — projections and ranged history.
// *appconversation.ConversationApp satisfies it.
type Reader interface {
	Info(ctx context.Context, id string) (drepo.ConversationInfo, error)
	CanCompact(ctx context.Context, id string) (bool, error)
	List(ctx context.Context) ([]drepo.ConversationInfo, error)
	History(ctx context.Context, id string, start, end int) ([]entity.Message, error)
	ContextFooterStats(ctx context.Context, convID, providerID, modelID string) (appconversation.ContextFooterStats, error)
	// Pending is the room's waiting pick, false when it waits for nothing.
	Pending(ctx context.Context, convID string) (drepo.PendingSelection, bool, error)
}

// Writer is the mutation surface: Rename is synchronous; Delete is an
// accepted job that must outlive the HTTP request — it runs under
// Deps.Jobs, the server-lifecycle context, so an abandoned request never
// half-applies it and server shutdown still cancels it. History operations
// (compact, clear, fork) are commands, not writer verbs.
// *appconversation.ConversationApp satisfies it.
type Writer interface {
	// Annotate applies or parks the change. It answers true when a pick is now
	// waiting for the room's next turn — the caller renders that as 202, never
	// as "done".
	Annotate(ctx context.Context, id string, a appconversation.Annotation) (parked bool, err error)
	Delete(ctx context.Context, id string) error
	// DeleteAll removes every conversation and purges the artifacts only
	// threads own (uploads, picks, recordings, views); it answers with
	// the deleted count, or a partial count plus error.
	DeleteAll(ctx context.Context) (int, error)
}

// Approver answers the permission requests a room's agent is waiting on.
// *appconversation.ConversationApp satisfies it.
type Approver interface {
	Approve(convID, requestID, optionID string) error
	PendingApprovals(convID string) []valueobject.ApprovalRequest
}

// LiveTurn is the websocket hub's read of a room's in-progress transport
// turn. The conversation projection does not own that fact; a nil lookup
// omits it. kind is "send" or "edit".
type LiveTurn interface {
	ActiveTurn(conversationID string) (turnID, kind string, ok bool)
}

// TurnLiveness reports whether work is executing in a room right now. It is not
// the same fact as LiveTurn: that names a turn a client can watch and stop, this
// one says the room is busy at all — a fold or a fork is work with no stream.
// Nil leaves the field off the projection.
type TurnLiveness interface {
	TurnActive(convID string) bool
}

// QueueDepth reports how many jobs wait behind a room's running turn —
// the worker queue `conversation.queued` announces. The projection owns no
// queue, and the frame is a nudge that a tab can miss: a read carries the
// number so an opened room starts from the truth. A nil lookup omits it.
// *appconversation.ConversationApp satisfies it.
type QueueDepth interface {
	QueueDepth(convID string) int
}

// Deps wires the use cases; Reads and Writes are optional — nil leaves
// the corresponding routes unregistered.
type Deps struct {
	Reads  Reader
	Writes Writer
	// LiveTurns reports a non-terminal transport turn for detail and
	// list reads. Nil leaves the field off the projection.
	LiveTurns LiveTurn
	// Queue reports how many jobs wait behind a room's running turn for
	// detail and list reads. Nil leaves the field off the projection.
	Queue QueueDepth
	// Turns reports whether work is executing in the room now — the busy
	// signal a surface needs to say "working" when no turn is watchable. Nil
	// leaves the field off the projection.
	Turns TurnLiveness
	// Approvals answers agent permission requests; nil leaves its routes
	// unregistered.
	Approvals Approver
	// Elicitations answers agents' requests for input; nil leaves its
	// routes unregistered.
	Elicitations Elicitor
	// Agent serves rooms' agent options; nil leaves its routes
	// unregistered.
	Agent AgentSurface
	// Commands serves the slash-command catalog and runs one native
	// command; nil leaves both routes unregistered.
	Commands CommandSurface
	// Workspace serves reference search and the folders a room works
	// in; nil leaves its routes unregistered.
	Workspace WorkspaceSurface
	// Uploads keeps files attached before sending; nil leaves its route
	// unregistered.
	Uploads Uploader
	// Views keeps each thread's draft and side pane across reloads; nil
	// leaves its routes unregistered.
	Views Viewer
	// Jobs is the server-lifecycle context for accepted background work
	// (delete, compact) — decoupled from any single request so a client
	// disconnect can't cancel accepted work; nil means Background.
	Jobs context.Context
	// ActivityClearer drops the cross-thread activity log after a
	// delete-all purge — entries pointing at threads that no longer
	// exist would only mislead; nil leaves the log.
	ActivityClearer interface{ Clear() }
}

// Register mounts this boundary's routes under /api. The router root
// calls it; this package never imports the root.
func Register(api *gin.RouterGroup, d Deps) {
	jobs := d.Jobs
	if jobs == nil {
		jobs = context.Background()
	}
	s := &server{reads: d.Reads, writes: d.Writes, jobs: jobs, live: d.LiveTurns, queue: d.Queue, turns: d.Turns, clearActivity: d.ActivityClearer, commands: d.Commands}
	if d.Approvals != nil {
		a := &approvals{svc: d.Approvals}
		api.GET("/conversations/:id/approvals", a.list)
		api.POST("/conversations/:id/approvals/:requestId", a.answer)
	}
	if d.Elicitations != nil {
		e := &elicitations{svc: d.Elicitations}
		api.GET("/conversations/:id/elicitations", e.list)
		api.POST("/conversations/:id/elicitations/:elicitationId", e.answer)
	}
	if d.Agent != nil {
		registerAgent(api, d.Agent, d.Reads)
	}
	if d.Commands != nil {
		api.GET("/commands", s.listCommands)
		// Running a command answers with a room projection, which a
		// writes-only composition has no read surface to build.
		if d.Reads != nil {
			api.POST("/conversations/:id/commands", s.runCommand)
		}
	}
	if d.Workspace != nil {
		registerWorkspace(api, d.Workspace)
	}
	if d.Uploads != nil {
		api.POST("/uploads", upload(d.Uploads))
	}
	if d.Views != nil {
		registerViews(api, d.Views)
	}
	if d.Reads != nil {
		api.GET("/conversations", s.list)
		api.GET("/conversations/:id", s.get)
		api.GET("/conversations/:id/messages", s.messages)
		api.GET("/conversations/:id/export", s.export)
		api.GET("/context", s.context)
	}
	if d.Writes != nil {
		api.DELETE("/conversations", s.deleteAll)
		api.DELETE("/conversations/:id", s.delete)
		// Rename answers with a fresh projection — a writes-only
		// composition has no read surface to build it from, so the route
		// stays unregistered rather than panic inside the handler.
		if d.Reads != nil {
			api.PATCH("/conversations/:id", s.annotate)
		}
	}
}

type server struct {
	reads         Reader
	writes        Writer
	commands      CommandSurface
	live          LiveTurn
	queue         QueueDepth
	turns         TurnLiveness
	jobs          context.Context
	clearActivity interface{ Clear() }
}
