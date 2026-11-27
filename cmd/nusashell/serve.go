package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	appautomation "github.com/jahrulnr/nusashell/application/automation"
	appbrowser "github.com/jahrulnr/nusashell/application/browser"
	appchannel "github.com/jahrulnr/nusashell/application/channel"
	appcontrol "github.com/jahrulnr/nusashell/application/control"
	appjob "github.com/jahrulnr/nusashell/application/job"
	appmcp "github.com/jahrulnr/nusashell/application/mcp"
	appnotify "github.com/jahrulnr/nusashell/application/notify"
	appprovider "github.com/jahrulnr/nusashell/application/provider"
	"github.com/jahrulnr/nusashell/application/setting"
	appupdate "github.com/jahrulnr/nusashell/application/update"
	"github.com/jahrulnr/nusashell/infrastructure/config"
	"github.com/jahrulnr/nusashell/infrastructure/delivery/cli/admin"
	httpdelivery "github.com/jahrulnr/nusashell/infrastructure/delivery/http"
	httpmcp "github.com/jahrulnr/nusashell/infrastructure/delivery/http/mcp"
	deliverymcp "github.com/jahrulnr/nusashell/infrastructure/delivery/mcp"
	wsdelivery "github.com/jahrulnr/nusashell/infrastructure/delivery/websocket"
	wsconversation "github.com/jahrulnr/nusashell/infrastructure/delivery/websocket/conversation"
	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	repoappicon "github.com/jahrulnr/nusashell/infrastructure/repository/appicon"
	repoapprovalrule "github.com/jahrulnr/nusashell/infrastructure/repository/approvalrule"
	repoautomation "github.com/jahrulnr/nusashell/infrastructure/repository/automation"
	repobrowserhistory "github.com/jahrulnr/nusashell/infrastructure/repository/browserhistory"
	repobrowserlogin "github.com/jahrulnr/nusashell/infrastructure/repository/browserlogin"
	repochannel "github.com/jahrulnr/nusashell/infrastructure/repository/channel"
	repochatindex "github.com/jahrulnr/nusashell/infrastructure/repository/chatindex"
	repoconfig "github.com/jahrulnr/nusashell/infrastructure/repository/config"
	repofiletree "github.com/jahrulnr/nusashell/infrastructure/repository/filetree"
	repomcpserver "github.com/jahrulnr/nusashell/infrastructure/repository/mcpserver"
	reporecording "github.com/jahrulnr/nusashell/infrastructure/repository/recording"
	repothreadview "github.com/jahrulnr/nusashell/infrastructure/repository/threadview"
	servicebrowser "github.com/jahrulnr/nusashell/infrastructure/service/browser"
	"github.com/jahrulnr/nusashell/infrastructure/service/channel/telegram"
	"github.com/jahrulnr/nusashell/infrastructure/service/channel/whatsapp"
	"github.com/jahrulnr/nusashell/infrastructure/service/mcp"
	"github.com/jahrulnr/nusashell/infrastructure/service/notify"
	"github.com/jahrulnr/nusashell/infrastructure/service/shell"
	toolmcp "github.com/jahrulnr/nusashell/infrastructure/service/tool/mcp"
	"github.com/jahrulnr/nusashell/infrastructure/system"
	"github.com/jahrulnr/nusashell/web"
)

const serveUsage = `Usage: ` + admin.CLIName + ` serve [--port N] [--open]
       ` + admin.CLIName + ` serve install|start|stop|status

Local WebUI — embedded browser UI, HTTP API, and WebSocket turn channel.
Binds server.bind (127.0.0.1 by default) on server.port. A login is
required when the server is reachable beyond this machine (server.auth).
install/start/stop/status manage the host service that runs this same
binary; ` + admin.CLIName + ` uninstall removes it.

Flags:
  --port N   port for this process (1..65535)
`

// serve hosts the WebUI — embedded browser UI, HTTP API, and the
// WebSocket turn channel — on 127.0.0.1. The port and accepted HTTP
// hostnames come from server settings; --port can override the configured
// port for one process invocation.
func serve(args []string, svc system.Service) (serveErr error) {
	if serveHelpRequested(args) {
		fmt.Print(serveUsage)
		return nil
	}
	b, err := admin.LoadBoot(true)
	if err != nil {
		return err
	}
	// Deferred before EnsureConfig so a config failure still closes the
	// secret store.
	defer func() {
		if closeErr := b.Close(); serveErr == nil {
			serveErr = closeErr
		}
	}()
	if err := admin.EnsureConfig(b.ConfigPath(), os.Stderr); err != nil {
		return err
	}
	port, open, err := serveFlags(args, b.Config().Server.Port)
	if err != nil {
		return err
	}
	// Ask before composing: a healthy server already owns the port, and
	// starting this process's browser manager first would reap the live
	// server's Chrome as an orphan. --open on a running WebUI only opens
	// the browser.
	srvCfg := b.Config().Server
	if open {
		if local := srvCfg.LocalAuthority(port); healthy(local) {
			openBrowser(openTargetURL(srvCfg, port))
			return nil
		}
	}
	// A WebUI room with no bound workspace runs its tools under the
	// owner's home — the same root the workspace picker opens on. The
	// process launch dir is not a workspace here: the systemd unit pins
	// it to the storage dir, and a manual serve cwd is incidental.
	fileTree := repofiletree.NewLocal()
	home, err := fileTree.HomeDir()
	if err != nil {
		return err
	}
	eng, err := compose(b.Config(), b.Secrets(), b.ConfigPath(), home)
	if err != nil {
		return err
	}
	// One close each: the barrier's storage phase runs them, and the defers
	// only cover an early return before the barrier exists.
	closeLogger := sync.OnceValue(func() error { return eng.logger.Close() })
	defer func() {
		if closeErr := closeLogger(); serveErr == nil {
			serveErr = closeErr
		}
	}()
	// Runs after the conversation app's shutdown below (defers unwind in
	// reverse), so no turn writes to a closed database.
	closeDB := sync.OnceValue(func() error { return eng.db.Close() })
	defer func() {
		if closeErr := closeDB(); serveErr == nil {
			serveErr = closeErr
		}
	}()
	// A server that needs a login and has no password gets a generated
	// one, shown once. `nusashell password` replaces it.
	if generated, err := eng.auth.EnsurePassword(context.Background()); err != nil {
		return err
	} else if generated != "" {
		fmt.Fprintf(os.Stderr, "%s: login is required; generated owner password (shown once): %s\n", admin.CLIName, generated)
		fmt.Fprintf(os.Stderr, "%s: change it with `%s password`\n", admin.CLIName, admin.CLIName)
	}
	defer func() {
		// Registered before Listen/Serve so an early failure or a
		// shutdown error still retires the conversation workers.
		if shutdownErr := eng.conversations.Shutdown(context.Background()); serveErr == nil {
			serveErr = shutdownErr
		}
	}()

	// SIGHUP joins the set because closing the terminal is the most common way
	// a foreground run dies, and without it the process gets no shutdown at
	// all — no persisted turn, no killed children.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	// Model catalogs refresh on an hourly cadence, first pass at boot —
	// newly advertised ids merge into provider records so the WebUI picker
	// sees them without a manual import. Outcomes land in the diagnostic
	// log; the loop dies with the signal context on shutdown.
	go b.Providers().StartAutoImport(ctx, time.Hour, func(r appprovider.AutoImportResult) {
		eng.logger.Append(context.Background(), "provider.autoimport", r)
	})

	gin.SetMode(gin.ReleaseMode)
	portText := strconv.Itoa(port)
	listenAuthority := net.JoinHostPort(srvCfg.Bind, portText)
	authorities := make([]string, 0, 2*len(srvCfg.Hosts))
	for _, host := range srvCfg.Hosts {
		authorities = append(authorities, net.JoinHostPort(host, portText))
		// A reverse proxy on the default port forwards the bare host.
		if srvCfg.Exposed() {
			authorities = append(authorities, host)
		}
	}
	// The hub's context ignores the signal — Hub.Shutdown owns turn
	// cancellation so terminal frames land before teardown, never on
	// signal arrival. Deferred so an early Serve failure still closes it.
	hub := wsconversation.NewHub(context.WithoutCancel(ctx), eng.conversations)
	hub.UseUploads(eng.conversations)
	// The queued notice names the worker's own queue depth, read when the
	// frame is emitted.
	hub.UseQueueDepth(eng.conversations.QueueDepth)
	eng.conversations.OnQueueChanged(hub.QueueChanged)
	hub.UsePlanEvents(eng.planEvents)
	eng.conversations.UseShells(shell.Local{})
	eng.agents.UseShells(shell.Local{})
	eng.conversations.UseThreadViews(repothreadview.New(eng.db))
	defer hub.Shutdown()
	// Chat channels run their turns through the hub, so the web sees them
	// live. They stop before the hub does (defers unwind in reverse).
	// Settings read on use (model roles, notifications) come from the file
	// each time, so a change in Settings applies without a restart.
	configStore := repoconfig.NewLocal()
	liveConfig := func() (config.Config, error) {
		var c config.Config
		err := c.Load(configStore, b.ConfigPath())
		return c, err
	}
	channelRepo := repochannel.New(eng.db)
	chats := appchannel.New(channelRepo, channelRepo, eng.uploads, eng.projectRepo, eng.conversations, hub, appchannel.ConfiguredCatalog{
		Agents: eng.agents, Providers: b.Providers(),
		Preferred: func() appchannel.Runtime {
			c, err := liveConfig()
			if err != nil {
				return appchannel.Runtime{}
			}
			r := c.Models.Chat
			return appchannel.Runtime{AgentID: r.Agent, ProviderID: r.Provider, ModelID: r.Model, Effort: r.Effort}
		},
	}, slog.Default())
	chats.Register(telegram.New(channelRepo, b.Secrets(), slog.Default()))
	chatIndex := repochatindex.New(eng.db)
	chats.UseChatIndex(chatIndex)
	chats.Register(whatsapp.New(filepath.Join(eng.storageDir, "whatsapp.db"), admin.ProductName, channelRepo, chatIndex, slog.Default()))
	hub.OnFinished(chats.TurnFinished)
	// The utility model names a thread after its first turn.
	eng.conversations.UseUtility(func() (string, string) {
		c, err := liveConfig()
		if err != nil || !c.Models.Utility.Assigned() {
			return "", ""
		}
		return c.Models.Utility.Provider, c.Models.Utility.Model
	})
	// After a compaction the utility model reads what the owner said in
	// the sealed part and suggests memories for Settings → Memory. It runs
	// beside the turn and never fails it.
	eng.memory.UseGenerator(eng.conversations.Utility)
	eng.conversations.OnCompacted(func(convID string, sealed []entity.Message) {
		go func() {
			memCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
			defer cancel()
			if n, err := eng.memory.Consolidate(memCtx, convID, sealed); err != nil {
				slog.Warn("could not read a compacted thread for memories", "conversation", convID, "err", err)
			} else if n > 0 {
				slog.Info("suggested memories", "conversation", convID, "count", n)
			}
		}()
	})
	hub.OnFinished(func(convID, kind string, _ valueobject.TurnOrigin, _ bool, err error) {
		if kind != "send" || err != nil {
			return
		}
		go func() {
			titleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
			defer cancel()
			if err := eng.conversations.AutoTitle(titleCtx, convID); err != nil {
				slog.Warn("could not name the thread", "conversation", convID, "err", err)
			}
		}()
	})
	if edits := wireSkillObserve(b.Config().Skills.Improvement, hub.OnFinished, eng.convs, slog.Default()); edits != nil {
		eng.conversations.UseEditEvidence(edits)
	}
	// suggest is the only mode that mounts the page and the candidate pass.
	// off and observe get the zero surface, so this process stays as it was.
	learned := eng.skillSurface(b.Config().Skills.Improvement, home)
	if learned.Hook != nil {
		hub.OnFinished(learned.Hook)
	}
	if learned.Edits != nil {
		eng.conversations.UseEditEvidence(learned.Edits)
	}
	eng.conversations.OnDeleted(chats.ForgetRoom)
	// A deleted thread takes its own memories with it; a deleted project's
	// are archived, since deleting a project can be undone.
	eng.conversations.OnDeleted(func(convID string) {
		if err := eng.memory.ForgetThread(context.Background(), convID); err != nil {
			slog.Warn("could not drop a deleted thread's memories", "conversation", convID, "err", err)
		}
	})
	eng.projects.OnDeleted(func(ctx context.Context, projectID string) {
		if err := eng.memory.ForgetProject(context.WithoutCancel(ctx), projectID); err != nil {
			slog.Warn("could not archive a deleted project's memories", "project", projectID, "err", err)
		}
	})
	eng.projects.OnRestored(func(ctx context.Context, projectID string) {
		if err := eng.memory.RestoreProject(context.WithoutCancel(ctx), projectID); err != nil {
			slog.Warn("could not restore a project's memories", "project", projectID, "err", err)
		}
	})
	// Forgotten memories are restorable for 30 days, then purged: at
	// start and daily after.
	go func() {
		for {
			if n, err := eng.memory.Purge(ctx); err != nil {
				slog.Warn("could not purge forgotten memories", "err", err)
			} else if n > 0 {
				slog.Info("purged forgotten memories", "count", n)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(24 * time.Hour):
			}
		}
	}()
	eng.conversations.OnChanged(hub.RoomChanged)
	// A command runs only in an idle room: the hub is the one place that knows
	// whether a transport turn is live, so commands read it from there.
	// A queued turn still has an audience — its room. The hub journals it like a
	// turn a socket admitted, so a relay-driven room is watchable instead of
	// looking idle behind "N waiting to run".
	eng.conversations.UseTurnSurface(hub)
	eng.conversations.UseLiveTurns(func(convID string) bool {
		_, _, live := hub.ActiveTurn(convID)
		return live
	})
	// A journaled turn's execution belongs to the room's worker, so the hub
	// reaches it through the application — one direction, never back. Cancel
	// is honest: true only when a live worker took the interruption.
	hub.UseJobControl(
		func(convID string) bool {
			return eng.conversations.Cancel(context.Background(), convID) == nil
		},
		func(convID, text string) bool {
			taken, err := eng.conversations.SteerInput(context.Background(), convID, valueobject.UserInput{Text: text})
			return err == nil && taken
		},
	)
	// Threads work with each other and drive a browser through two
	// built-in MCP servers. The model runtime reaches them over an
	// in-process connection; agents start this binary as a relay that
	// calls back here with the thread's token.
	recordings := reporecording.NewStore(filepath.Join(eng.storageDir, "recordings"), slog.Default())
	stopRecordings := sync.OnceFunc(func() { recordings.Shutdown() })
	defer stopRecordings()
	profileDir := browserProfileDir(b.Config().Browser.Dir, eng.storageDir)
	if strings.TrimSpace(b.Config().Browser.Dir) == "" {
		var moved bool
		if profileDir, moved = servicebrowser.ProfileRoot(profileDir); moved {
			slog.Info("browser profiles are kept outside the data folder, whose filesystem cannot hold the symlink Chrome's lock needs",
				"dir", profileDir)
		}
	}
	browserMgr := servicebrowser.New(servicebrowser.Options{
		ExecPath: b.Config().Browser.Exec, InstallDir: filepath.Join(eng.storageDir, "chrome"),
		ProfileDir: profileDir, Idle: 30 * time.Minute, Log: slog.Default(),
	})
	if offer := browserMgr.InstallOffer(); offer.Missing {
		slog.Warn("no Chrome or Chromium found; threads cannot use a browser until one is installed",
			"hint", "Settings → Tools, or `nusashell browser install`")
	}
	// Chrome starts on the first browser tool call, never at boot. A measured
	// idle instance used to hold 148 MiB cgroup / 113 MiB PSS across 13 Chrome
	// processes for a browser nobody opened; the cold start it bought is
	// 208 ms–1.95 s, paid once, by the thread that asked. On a machine that has
	// already lost a session to memory pressure that trade is not close.
	stopBrowser := sync.OnceFunc(func() { browserMgr.Shutdown() })
	defer stopBrowser()
	// [[env:NAME]] in typed text resolves against the thread's agent's
	// saved environment; a model thread has none.
	browsers := appbrowser.New(browserMgr, func(ctx context.Context, convID string) (map[string]string, error) {
		info, err := eng.conversations.Info(ctx, convID)
		if err != nil || info.AgentID == "" {
			return nil, err
		}
		a, err := eng.agentRepo.Load(ctx, info.AgentID)
		if err != nil {
			return nil, err
		}
		return a.Env(), nil
	}, repobrowserlogin.New(eng.db, b.Secrets()), recordings)
	browsers.UseAnnotations(eng.uploads, eng.picks)
	eng.conversations.UseRecordings(recordings)
	browsers.UseInstaller(browserMgr)
	browsers.UseHistory(repobrowserhistory.New(eng.db))
	control := appcontrol.New(context.WithoutCancel(ctx), eng.conversations, hub, eng.conversations, eng.conversations, eng.uploads, eng.projectRepo, chats, eng.toolOutputs, slog.Default())
	catalogs := []deliverymcp.Catalog{deliverymcp.Conversation(control), deliverymcp.Browser(browsers), deliverymcp.Memory(eng.memory)}
	mcpPool := toolmcp.NewPool()
	closeMCPPool := sync.OnceFunc(func() { mcpPool.Close() })
	defer closeMCPPool()
	serverNames := make([]string, 0, len(catalogs))
	for _, cat := range catalogs {
		serverNames = append(serverNames, cat.Name)
		prefix := ""
		if cat.Name == "conversation" || cat.Name == "memory" {
			prefix = cat.Name + "_"
		}
		tools, err := toolmcp.Tools(ctx, toolmcp.Server{Name: cat.Name, Prefix: prefix,
			Connect: func(ctx context.Context, convID string) (*mcp.Client, error) {
				return mcp.Local(ctx, cat.Server(convID), admin.CLIName, version)
			}}, mcpPool, eng.toolOutputs)
		if err != nil {
			return err
		}
		for _, t := range tools {
			eng.tools[t.Schema().Name()] = t
		}
	}
	// The owner's MCP servers: the model runtime connects to them in the
	// background, agents get them with the built-in ones in each session.
	mcpServers := appmcp.New(repomcpserver.New(eng.db, b.Secrets()), mcpRuntime{eng.mcpTools}, slog.Default())
	closeMCPTools := sync.OnceFunc(func() { eng.mcpTools.Close() })
	defer closeMCPTools()
	go mcpServers.Reload(context.WithoutCancel(ctx))
	go mcpServers.Watch(ctx, 30*time.Second)
	grants := deliverymcp.NewGrants()
	local := srvCfg.LocalAuthority(port)
	launch := deliverymcp.Launch{URL: "http://" + local, Grants: grants, Servers: serverNames}
	// Children — agents, shells — find this server for `nusashell channel`
	// and the other commands that call it, whatever --port said.
	os.Setenv(admin.EnvAddr, local)
	if exe, err := os.Executable(); err == nil {
		launch.Exec = exe
	} else {
		slog.Warn("agents get no built-in tools: this binary's path is unknown", "error", err)
	}
	eng.conversations.UseMCPServers(func(convID string) []dservice.MCPServer {
		var out []dservice.MCPServer
		if launch.Exec != "" {
			out = launch.For(convID)
		}
		return append(out, mcpServers.ForAgents(context.Background())...)
	})
	eng.conversations.UseReferenceSources(eng.skills, func(ctx context.Context) []string {
		var names []string
		if launch.Exec != "" {
			names = append(names, serverNames...)
		}
		for _, s := range mcpServers.ForAgents(ctx) {
			names = append(names, s.Name)
		}
		return names
	})
	approvalRules := repoapprovalrule.New(eng.db)
	eng.conversations.UseApprovalRules(approvalRules)
	eng.conversations.OnDeleted(func(convID string) {
		_ = approvalRules.DeleteByConversation(context.Background(), convID)
		browsers.Close(convID)
		mcpPool.Release(convID)
		grants.Revoke(convID)
	})
	// Desktop notifications read their setting on each send, so turning
	// them off applies at once.
	// Every notification carries the NusaShell icon, a file the notification
	// daemon reads; without one the system shows its stock icon.
	iconPath, err := repoappicon.Write(eng.storageDir)
	if err != nil {
		slog.Warn("notifications go out without the NusaShell icon", "err", err)
	}
	alerts := appnotify.New(notify.Detect(admin.ProductName, iconPath), func() bool {
		c, err := liveConfig()
		if err != nil {
			return true
		}
		return c.Notifications.DesktopOn()
	}, eng.conversations, admin.ProductName, slog.Default())
	hub.OnFinished(alerts.TurnFinished)
	hub.OnEvent(alerts.TurnEvent)
	chats.Start(context.WithoutCancel(ctx))
	stopChats := sync.OnceFunc(func() { chats.Stop() })
	defer stopChats()
	// Agents kept resident start with the server; the others stop after a
	// while without a turn, and their rooms resume where they were.
	eng.agents.WarmResident(ctx)
	go func() {
		tick := time.NewTicker(time.Minute)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				// Read on each pass, so a change in Settings applies
				// without a restart; an unreadable file skips the pass.
				if c, err := liveConfig(); err == nil {
					eng.agentRuntime.ReapIdle(time.Duration(c.Agents.IdleTimeout))
				}
			}
		}
	}()
	// Automations run through the hub like chats do; on shutdown they stop
	// before it (the explicit Stop below — this defer covers early exits).
	automations := appautomation.New(repoautomation.New(eng.db), eng.projectRepo, hub, eng.conversations, chats, alerts,
		system.LocalTimezone(), slog.Default())
	automations.Start(context.WithoutCancel(ctx))
	stopAutomations := sync.OnceFunc(func() { automations.Stop() })
	defer stopAutomations()
	settings := setting.New(repoconfig.NewLocal(), b.ConfigPath())
	// The barrier is built here — before any update can run and long before a
	// signal — so the update job's restart hook always has it. Its socket
	// phase needs the listener, which is the one piece that arrives later; the
	// closure reads it when the phase runs, and by then Serve has long started.
	var srv *http.Server
	// restarting marks a shutdown the update job started: once the drain
	// closes the listener, serve must not return and let main exit — the
	// replacement is about to exec, and only it can finish the restart.
	var restarting atomic.Bool
	shutdown := appjob.NewBarrier(appjob.BarrierSteps{
		// Nothing may start once shutdown does.
		CloseAdmission: func(context.Context) error {
			alerts.Mute()
			stopAutomations()
			eng.conversations.CloseAdmission()
			return nil
		},
		// Workers terminate with the restart cause and their turns unwind —
		// a tool is killed, its head and restart suffix written, the reply
		// sealed interrupted. The hub cuts the transport turns with the same
		// cause and lands their terminal frames.
		StopTurns: func(phaseCtx context.Context) error {
			err := eng.conversations.Shutdown(phaseCtx)
			hub.StopTurns()
			return err
		},
		// Everything still holding a file or a process. Child processes die
		// here because syscall.Exec keeps the process tree and a Windows exit
		// leaves it.
		FlushWriters: func(phaseCtx context.Context) error {
			stopChats()
			stopRecordings()
			stopBrowser()
			closeMCPPool()
			closeMCPTools()
			return eng.conversations.TerminateChildren(phaseCtx)
		},
		// Hijacked WebSocket conns are invisible to http.Server.Shutdown, so
		// the hub drains them first; a client that missed a frame reconnects
		// and resyncs. An error here ends the phase, not the barrier.
		DrainSockets: func(phaseCtx context.Context) error {
			hub.DrainSockets(phaseCtx)
			if srv == nil {
				return nil
			}
			return srv.Shutdown(phaseCtx)
		},
		CloseStorage: func(context.Context) error {
			if err := closeDB(); err != nil {
				return err
			}
			return closeLogger()
		},
	})
	updates, err := newUpdates(svc, ctx, restartHook(shutdown, &restarting))
	if err != nil {
		return err
	}
	// The release check also runs unattended: a newer release raises one
	// desktop notification per version.
	go updates.Watch(ctx, func(info appupdate.Info) { alerts.UpdateAvailable(info.Latest) })
	terminals := wsdelivery.NewTerminalHandler(eng.conversations, eng.agents, authorities)
	handler := httpdelivery.NewHandler(httpdelivery.Deps{
		Conversations:      eng.conversations,
		ConversationWrites: eng.conversations,
		Providers:          b.Providers(),
		ProviderWrites:     b.Providers(),
		Settings:           settings,
		SettingsWriter:     settings,
		Tools:              eng.conversations,
		Agents:             eng.agents,
		Auth:               eng.auth,
		Projects:           eng.projects,
		Memories:           eng.memory,
		WebSearch:          eng.webSearch,
		Media:              eng.media,
		Channels:           chats,
		Automations:        automations,
		Notifications:      alerts,
		Browser:            browsers,
		BrowserWS:          wsdelivery.NewBrowserHandler(browsers, authorities).Serve,
		TerminalWS:         terminals.Serve,
		SignInWS:           terminals.ServeSignIn,
		MCP:                httpmcp.Deps{Grants: grants, Catalogs: catalogs},
		Control:            control,
		MCPServers:         mcpServers,
		Approvals:          eng.conversations,
		Elicitations:       eng.conversations,
		AgentSurface:       eng.conversations,
		Commands:           eng.conversations,
		Workspace:          eng.conversations,
		Uploads:            eng.conversations,
		Views:              eng.conversations,
		Updates:            updates,
		FileTree:           fileTree,
		Skills:             learned.Page,
		// Accepted delete/compaction jobs ride the server lifecycle —
		// a client that abandons a request can't cancel them, but
		// shutdown does.
		Jobs:        ctx,
		LiveTurns:   hub,
		Queue:       eng.conversations,
		TurnActive:  eng.conversations,
		WS:          wsdelivery.NewHandler(hub, authorities).Serve,
		Authorities: authorities,
		WebAssets:   webAssets(),
	})
	// Host aliases only affect request validation; the socket binds
	// server.bind.
	ln, err := net.Listen("tcp", listenAuthority)
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			// A port already held by a healthy NusaShell answers --open
			// with a browser tab instead of an error — the user asked for
			// the UI, and it already runs.
			if open && healthy(local) {
				openBrowser(openTargetURL(srvCfg, port))
				return nil
			}
			// The holder is not identified here: the installed service may be
			// active on another port, so it is named only as a hint. It is
			// skipped when this process is the service itself, or a unit
			// losing its own bind race would blame itself.
			if svc.ActiveElsewhere(admin.CLIName) {
				return fmt.Errorf("listen on %s: address already in use — if the %s holds this port, `%s serve stop` it or pass --port", listenAuthority, svc.Name(), admin.CLIName)
			}
			return fmt.Errorf("listen on %s: address already in use by another process", listenAuthority)
		}
		return fmt.Errorf("listen on %s: %w", listenAuthority, err)
	}
	srv = &http.Server{Handler: handler}
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve(ln) }()

	login := "no login"
	if eng.auth.Required() {
		login = "login required"
	}
	fmt.Fprintf(os.Stderr, "%s: listening on %s (%s)\n", admin.CLIName, listenAuthority, login)
	for _, host := range srvCfg.Hosts {
		fmt.Fprintf(os.Stderr, "%s: WebUI available at http://%s\n", admin.CLIName, net.JoinHostPort(host, portText))
	}
	if srvCfg.PublicURL != "" {
		fmt.Fprintf(os.Stderr, "%s: public URL %s\n", admin.CLIName, srvCfg.PublicURL)
	}
	if open {
		openBrowser(openTargetURL(srvCfg, port))
	}
	select {
	case err := <-serveDone:
		// A listener closed by our own drain — a signal or a restart — is not
		// a failure: the sequence owns what happens next.
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		if !restarting.Load() {
			return nil
		}
	case <-ctx.Done():
	}

	// The signal path runs the same sequence the update path does: one pass,
	// save first, sockets after.
	shutdown.Run(ctx)
	if restarting.Load() {
		// The update job is replacing this image now. Returning here would let
		// main exit 0 while syscall.Exec is still on its way — the binary
		// installed, no process running it. Park instead: the exec ends this
		// process, or its failure exits 1 with a message.
		//
		// While parked this process handles no signal: a second SIGTERM (or
		// SIGHUP) is ignored — signal.NotifyContext already used its one
		// notification and the handler does nothing now — so only SIGKILL
		// stops it before the exec lands. That window is the exec itself, and
		// a supervisor's stop timeout covers it.
		select {}
	}
	return nil
}

// restartHook is what the update job runs before a process-mode restart: the
// one shutdown barrier, created at composition time and passed by value to the
// hook, so a restart can never find it missing. A service-mode restart skips
// it — the manager's stop sends the signal that runs the same sequence.
func restartHook(shutdown *appjob.Barrier, restarting *atomic.Bool) func(context.Context) error {
	return func(ctx context.Context) error {
		restarting.Store(true)
		shutdown.Run(ctx)
		return nil
	}
}

// serveFlags parses the optional one-process overrides. With no flag,
// the effective port comes from server.port in config.json; --open asks
// a desktop browser to open the WebUI once the listener is up (or the
// URL of the NusaShell server already holding the port).
func serveFlags(args []string, configuredPort int) (port int, open bool, err error) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	p := fs.Int("port", configuredPort, "port to bind for this process")
	o := fs.Bool("open", false, "open the WebUI in a browser")
	if err := fs.Parse(args); err != nil {
		return 0, false, err
	}
	if fs.NArg() > 0 {
		return 0, false, fmt.Errorf("serve: unexpected argument %q", fs.Arg(0))
	}
	if *p < 1 || *p > 65535 {
		return 0, false, fmt.Errorf("serve: --port must be 1..65535, got %d", *p)
	}
	return *p, *o, nil
}

// openTargetURL is where --open points the browser: the configured
// public URL when one exists, else this machine's accepted authority.
func openTargetURL(c config.ServerConfig, port int) string {
	if c.PublicURL != "" {
		return c.PublicURL
	}
	return "http://" + c.LocalAuthority(port)
}

// healthy reports whether a NusaShell server already answers on the
// authority — a bound port with a healthy API means --open just needs a
// browser, not a second listener.
func healthy(authority string) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	res, err := client.Get("http://" + authority + "/api/health")
	if err != nil {
		return false
	}
	defer res.Body.Close()
	return res.StatusCode == http.StatusOK
}

// openBrowser asks the desktop to open url and returns at once — the
// server must not block on a browser that may never answer.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		slog.Warn("could not open the browser", "url", url, "error", err)
	}
}

// serveHelpRequested detects a bareword/flag help request. A token
// consumed as a flag value — the arg after --port/-port — is not a help
// token (`--port=help` already fails in flag.Int).
func serveHelpRequested(args []string) bool {
	for i, a := range args {
		if i > 0 && (args[i-1] == "--port" || args[i-1] == "-port") {
			continue
		}
		switch a {
		case "-h", "--help", "help":
			return true
		}
	}
	return false
}

// webAssets is the embedded WebUI, or the directory named by
// NUSASHELL_WEB_DIR so UI edits show on reload without rebuilding.
func webAssets() fs.FS {
	if dir := os.Getenv("NUSASHELL_WEB_DIR"); dir != "" {
		return os.DirFS(dir)
	}
	return web.Assets
}

// mcpRuntime hands the owner's enabled MCP servers to the model runtime's
// registry.
type mcpRuntime struct{ reg *toolmcp.Registry }

func (m mcpRuntime) Load(ctx context.Context, list []aggregate.MCPServer) {
	ext := make([]toolmcp.External, 0, len(list))
	for _, s := range list {
		ext = append(ext, toolmcp.External{Name: s.Name(), Transport: s.Transport(), Command: s.Command(), Args: s.Args(),
			Env: s.Env(), URL: s.URL(), Headers: s.Headers()})
	}
	m.reg.Load(ctx, ext)
}

func (m mcpRuntime) Status() map[string]appmcp.Status {
	out := map[string]appmcp.Status{}
	for _, st := range m.reg.Status() {
		out[st.Name] = appmcp.Status{Connected: st.Connected, Tools: st.Tools, Error: st.Error}
	}
	return out
}

// browserProfileDir resolves where profile folders live: the configured
// directory when set, else `<storage.dir>/browser`. A leading `~` names the
// owner's home, since this is the one setting likely to leave the data
// folder entirely.
func browserProfileDir(dir, storageDir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return filepath.Join(storageDir, "browser")
	}
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(dir, "~"), "/"))
		}
	}
	return dir
}
