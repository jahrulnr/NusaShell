// Package http is the WebUI's HTTP delivery composition root — a
// loopback-only Gin router that mounts the per-boundary packages under
// this tree (conversation, provider, setting, tool, system, webui)
// behind the shared Host/Origin security envelope. The HTTP contract
// and the WebSocket turn channel are live; the embedded WebUI assets
// mount here too so browser, API, and socket share one origin.
//
// The transport serializes projections, never domain entities: key
// material, raw key refs, provider replay artifacts, attachment sha256
// pins, and error causes stay server-side. Normalized message parts,
// tool I/O, and attachment uri/name are owner-visible content.
package http

import (
	"context"
	"io/fs"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/agent"
	authboundary "github.com/jahrulnr/nusashell/infrastructure/delivery/http/auth"
	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/automation"
	httpbrowser "github.com/jahrulnr/nusashell/infrastructure/delivery/http/browser"
	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/channel"
	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/control"
	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/conversation"
	fsboundary "github.com/jahrulnr/nusashell/infrastructure/delivery/http/fs"
	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/internal/transport"
	httpmcp "github.com/jahrulnr/nusashell/infrastructure/delivery/http/mcp"
	httpmcpserver "github.com/jahrulnr/nusashell/infrastructure/delivery/http/mcpserver"
	httpmedia "github.com/jahrulnr/nusashell/infrastructure/delivery/http/media"
	httpmemory "github.com/jahrulnr/nusashell/infrastructure/delivery/http/memory"
	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/notify"
	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/project"
	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/provider"
	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/setting"
	httpskill "github.com/jahrulnr/nusashell/infrastructure/delivery/http/skill"
	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/system"
	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/tool"
	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/update"
	httpwebsearch "github.com/jahrulnr/nusashell/infrastructure/delivery/http/websearch"
	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/webui"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
)

// Deps wires the boundary use cases plus the accepted request authorities
// (host:port) the security model validates against. Reader and writer
// fields are optional: a nil surface leaves its routes unregistered.
type Deps struct {
	Conversations      conversation.Reader
	ConversationWrites conversation.Writer
	Providers          provider.Reader
	ProviderWrites     provider.Writer
	Settings           setting.Reader
	SettingsWriter     setting.Writer
	Tools              tool.Catalog
	// Agents manages external agents; Approvals answers their permission
	// requests. Nil leaves the routes unregistered.
	Agents agent.Service
	// Projects manages projects; nil leaves the routes unregistered.
	Projects project.Service
	// Memories lets the owner review and edit memory; nil leaves the
	// routes unregistered.
	Memories httpmemory.Service
	// Skills serves the Skills page: the joined catalog and the owner's
	// level moves. Nil fields leave the routes unregistered.
	Skills httpskill.Deps
	// WebSearch edits the web tools' settings; nil leaves the routes
	// unregistered.
	WebSearch httpwebsearch.Service
	// Media transcribes the composer's voice input; nil leaves the route
	// unregistered.
	Media httpmedia.Service
	// Channels serves chat channels; nil leaves the routes unregistered.
	Channels channel.Service
	// Automations manages scheduled prompts; nil leaves the routes
	// unregistered.
	Automations automation.Service
	// Notifications reports and tests desktop notifications; nil leaves
	// the routes unregistered.
	Notifications notify.Service
	// Browser shows threads' browser sessions; nil leaves the routes
	// unregistered. BrowserWS upgrades the live view.
	Browser   httpbrowser.Service
	BrowserWS gin.HandlerFunc
	// TerminalWS upgrades the owner's terminal and SignInWS an agent's
	// interactive sign-in; nil leaves each unregistered.
	TerminalWS gin.HandlerFunc
	SignInWS   gin.HandlerFunc
	// MCP serves the built-in tool servers to agents' relays.
	MCP httpmcp.Deps
	// Control serves the cross-thread activity log and clears it after a
	// delete-all purge; nil leaves the route unregistered and the log kept.
	Control control.Service
	// MCPServers manages the owner's MCP servers; nil leaves the routes
	// unregistered.
	MCPServers httpmcpserver.Service
	// Auth admits requests when the server requires a login and serves
	// the login routes. Nil admits every request.
	Auth      authboundary.Service
	Approvals conversation.Approver
	// Elicitations answers agents' requests for input.
	Elicitations conversation.Elicitor
	// Workspace serves reference search and a room's folders.
	Workspace conversation.WorkspaceSurface
	// Uploads keeps files attached before sending.
	Uploads conversation.Uploader
	// Views keeps each thread's draft and side pane.
	Views conversation.Viewer
	// AgentSurface serves rooms' agent options and approval rules.
	AgentSurface conversation.AgentSurface
	// Commands serves the slash-command catalog and runs one native command.
	Commands conversation.CommandSurface
	// Updates answers GET /api/update and POST /api/update/check; nil
	// leaves both routes unregistered.
	Updates  update.Checker
	FileTree drepo.FileTree
	// Jobs is the server-lifecycle context for accepted background work
	// (delete, compact) — decoupled from any single request so a client
	// disconnect can't cancel accepted work; nil means Background.
	Jobs context.Context
	// LiveTurns is the hub's in-progress transport turn, projected onto
	// conversation detail. Nil omits the field.
	LiveTurns conversation.LiveTurn
	// Queue is the worker queue depth, projected onto conversation detail
	// and list rows — the state behind the `conversation.queued` nudge.
	// Nil omits the field.
	Queue conversation.QueueDepth
	// TurnActive reports whether work executes in a room now — a turn, a fold,
	// a fork. It is what a surface reads to say "working" when no turn is
	// watchable. Nil omits the field.
	TurnActive conversation.TurnLiveness
	// Authorities are configured host:port pairs accepted in Host and
	// Origin headers. The listener address remains a separate loopback bind.
	Authorities []string
	// WS upgrades GET /api/ws into the conversation hub socket.
	WS gin.HandlerFunc
	// WebAssets is the embedded WebUI filesystem served on this origin;
	// nil keeps the binary API-only.
	WebAssets fs.FS
}

// NewHandler builds the contract router. All responses beyond the
// envelope are JSON projections; errors render as RFC 9457 problem+json.
// Callers that care about gin's mode set it at their own entry point —
// a constructor must not mutate process-global state.
func NewHandler(d Deps) *gin.Engine {
	r := gin.New()
	// No proxy trust: the listener is loopback and RemoteIP is unused.
	_ = r.SetTrustedProxies(nil)
	// Redirects would answer an unmatched path before the guard runs —
	// send them through NoRoute instead, which the guard still covers.
	r.RedirectTrailingSlash = false
	r.RedirectFixedPath = false
	r.Use(recoverProblem(), guard(d.Authorities), authboundary.Middleware(d.Auth))

	api := r.Group("/api")
	system.Register(api)
	authboundary.Register(api, d.Auth)
	conversation.Register(api, conversation.Deps{
		Reads:           d.Conversations,
		Writes:          d.ConversationWrites,
		Jobs:            d.Jobs,
		LiveTurns:       d.LiveTurns,
		Queue:           d.Queue,
		Turns:           d.TurnActive,
		Approvals:       d.Approvals,
		Elicitations:    d.Elicitations,
		Workspace:       d.Workspace,
		Uploads:         d.Uploads,
		Views:           d.Views,
		Agent:           d.AgentSurface,
		Commands:        d.Commands,
		ActivityClearer: d.Control,
	})
	control.Register(api, control.Deps{Activity: d.Control})
	agent.Register(api, d.Agents)
	project.Register(api, d.Projects)
	httpmemory.Register(api, d.Memories)
	httpskill.Register(api, d.Skills)
	httpwebsearch.Register(api, d.WebSearch)
	httpmedia.Register(api, d.Media)
	channel.Register(api, d.Channels)
	automation.Register(api, d.Automations)
	notify.Register(api, d.Notifications)
	provider.Register(api, provider.Deps{Reads: d.Providers, Writes: d.ProviderWrites, Jobs: d.Jobs})
	setting.Register(api, setting.Deps{Reads: d.Settings, Writes: d.SettingsWriter})
	tool.Register(api, tool.Deps{Tools: d.Tools})
	update.Register(api, update.Deps{Checks: d.Updates})
	httpmcp.Register(api, d.MCP)
	httpbrowser.Register(api, d.Browser)
	if d.TerminalWS != nil {
		api.GET("/conversations/:id/terminal", d.TerminalWS)
		api.GET("/terminal", d.TerminalWS)
	}
	if d.SignInWS != nil {
		api.GET("/agents/:id/sign-in", d.SignInWS)
	}
	if d.BrowserWS != nil {
		api.GET("/conversations/:id/browser/ws", d.BrowserWS)
	}
	httpmcpserver.Register(api, d.MCPServers)
	if d.FileTree != nil {
		fsboundary.Register(api, d.FileTree)
	}
	if d.WS != nil {
		api.GET("/ws", d.WS)
	}
	webui.Register(r, d.WebAssets)

	// A browser that opens an unknown path gets the app, which shows its
	// not-found view; everything else keeps the contract's problem+json.
	r.NoRoute(func(c *gin.Context) {
		if webui.Shell(c, d.WebAssets) {
			return
		}
		transport.WriteProblem(c, http.StatusNotFound, "not_found", "no such route")
	})
	return r
}
