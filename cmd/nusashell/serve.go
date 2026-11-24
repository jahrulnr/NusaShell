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
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	appautomation "github.com/jahrulnr/nusashell/application/automation"
	appbrowser "github.com/jahrulnr/nusashell/application/browser"
	appchannel "github.com/jahrulnr/nusashell/application/channel"
	appcontrol "github.com/jahrulnr/nusashell/application/control"
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
	defer func() {
		if closeErr := eng.logger.Close(); serveErr == nil {
			serveErr = closeErr
		}
	}()
	// Runs after the conversation app's shutdown below (defers unwind in
	// reverse), so no turn writes to a closed database.
	defer func() {
		if closeErr := eng.db.Close(); serveErr == nil {
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
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
	hub.OnFinished(func(convID, kind string, _ bool, err error) {
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
	eng.conversations.UseLiveTurns(func(convID string) bool {
		_, _, live := hub.ActiveTurn(convID)
		return live
	})
	// Threads work with each other and drive a browser through two
	// built-in MCP servers. The model runtime reaches them over an
	// in-process connection; agents start this binary as a relay that
	// calls back here with the thread's token.
	recordings := reporecording.NewStore(filepath.Join(eng.storageDir, "recordings"), slog.Default())
	defer recordings.Shutdown()
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
	} else {
		// A cold Chrome start can eat a first tool call's whole deadline;
		// warming it here makes the first browser_navigate hit a live one.
		browserMgr.Preheat(ctx)
	}
	defer browserMgr.Shutdown()
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
	control := appcontrol.New(context.WithoutCancel(ctx), eng.conversations, hub, eng.projectRepo, chats, eng.toolOutputs, slog.Default())
	catalogs := []deliverymcp.Catalog{deliverymcp.Conversation(control), deliverymcp.Browser(browsers), deliverymcp.Memory(eng.memory)}
	mcpPool := toolmcp.NewPool()
	defer mcpPool.Close()
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
	defer eng.mcpTools.Close()
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
	defer chats.Stop()
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
	defer automations.Stop()
	settings := setting.New(repoconfig.NewLocal(), b.ConfigPath())
	updates, err := newUpdates(svc, ctx)
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
		// Accepted delete/compaction jobs ride the server lifecycle —
		// a client that abandons a request can't cancel them, but
		// shutdown does.
		Jobs:        ctx,
		LiveTurns:   hub,
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
	srv := &http.Server{Handler: handler}
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
		return err // Serve only returns early on a listener failure
	case <-ctx.Done():
	}

	// Turns cancelled by the shutdown are no news.
	alerts.Mute()
	// Automations stop before the hub does: a run cut now ends as
	// interrupted — not cancelled by the hub's own teardown — and the
	// ticker stops claiming firings the hub would refuse. The defer
	// above only covers an early Serve failure.
	automations.Stop()
	// Hijacked WebSocket conns are invisible to http.Server.Shutdown —
	// the hub drains sockets and lands terminal frames first, on its own
	// context that outlived the signal.
	hub.Shutdown()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("server shutdown: %w", err)
	}
	if err := <-serveDone; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
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
