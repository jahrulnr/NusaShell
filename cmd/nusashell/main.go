// Command nusashell is the composition root: it wires configuration,
// persistence, providers, tools, transports and the embedded frontend.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"nusashell/application"
	"nusashell/frontend"
	"nusashell/infrastructure/ai"
	"nusashell/infrastructure/ai/codex"
	"nusashell/infrastructure/ai/modelcatalog"
	"nusashell/infrastructure/attachmentfs"
	"nusashell/infrastructure/config"
	"nusashell/infrastructure/docs"
	"nusashell/infrastructure/jsonstore"
	"nusashell/infrastructure/mcpclient"
	"nusashell/infrastructure/pluginfs"
	"nusashell/infrastructure/plugininstall"
	"nusashell/infrastructure/pluginruntime"
	"nusashell/infrastructure/skillfs"
	"nusashell/infrastructure/sqlitestore"
	"nusashell/infrastructure/tools"
	"nusashell/infrastructure/workspacepicker"
	"nusashell/transport"

	"github.com/jahrulnr/searchwire"
)

// version is the single source of truth for the Go port until a VERSION file
// is introduced at the release boundary.
const version = "0.1.0"

func main() {
	if err := run(); err != nil {
		slog.Error("nusashell exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	host := envOr("NUSASHELL_HOST", "127.0.0.1")
	port := envOr("NUSASHELL_PORT", "9999")
	dataDir := envOr("NUSASHELL_DATA_DIR", defaultDataDir())
	dev := os.Getenv("NUSASHELL_DEV") != ""

	level := slog.LevelInfo
	if dev {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	// Security guard: the server has no auth and exposes MCP command
	// execution (RCE via stdio MCP servers). Binding to a non-loopback
	// address without explicit consent is a misconfiguration that can
	// expose the shell to the local network. Require
	// NUSASHELL_ALLOW_REMOTE=1 to proceed.
	if !isLoopbackHost(host) && os.Getenv("NUSASHELL_ALLOW_REMOTE") != "1" {
		return fmt.Errorf("refusing to bind to non-loopback address %q: set NUSASHELL_ALLOW_REMOTE=1 to explicitly allow remote access (WARNING: no auth, MCP command execution is exposed)", host)
	}
	if !isLoopbackHost(host) {
		logger.Warn("remote access enabled — no auth, MCP command execution is exposed to the network", "host", host)
	}

	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(dataDir, 0o700)

	store, err := jsonstore.New(dataDir)
	if err != nil {
		return err
	}
	credentials, err := sqlitestore.NewCredentials(filepath.Join(dataDir, "credentials.db"))
	if err != nil {
		return err
	}
	defer credentials.Close()

	docSource, err := docs.New(filepath.Join(dataDir, "docs"))
	if err != nil {
		return err
	}

	mcpManager := mcpclient.NewManager()
	bus := application.NewBus()
	askService := application.NewAskQuestionService()
	todoStore := jsonstore.NewTodoStore(filepath.Join(dataDir, "conversation-todos.json"))
	providerStore := &jsonstore.Providers{S: store}
	searcher := searchwire.New(tools.SearchwireConfigFromProviders(providerStore, credentials))
	// Seed builtin skills from the embedded resources/agent/skills/ tree
	// into the user data directory, then create the filesystem-backed
	// skill store. Skill content (SKILL.md) lives on disk; metadata
	// (state, usage, provenance) is cataloged in skills.json.
	skillsRoot := filepath.Join(dataDir, "agent", "skills")
	if err := skillfs.SeedBuiltinSkills(skillsRoot); err != nil {
		slog.Warn("builtin skill seed failed", "error", err)
	}
	skillStore, err := skillfs.New(skillsRoot)
	if err != nil {
		slog.Warn("skill store init failed, using empty store", "error", err)
		skillStore, _ = skillfs.New(skillsRoot)
	}
	// Plugin store: installed plugins live under <datadir>/plugins/<id>/.
	// Each plugin has manifest.json + mcp/ (stdio server) + ui/ (static
	// HTML/CSS/JS). The runtime manager wraps the MCP manager to start
	// plugin MCP servers and route tool calls from plugin UIs.
	pluginsRoot := filepath.Join(dataDir, "plugins")
	pluginStore, err := pluginfs.New(pluginsRoot)
	if err != nil {
		slog.Warn("plugin store init failed", "error", err)
	}
	pluginInstaller := plugininstall.New(pluginStore, logger)
	pluginRuntime := pluginruntime.New(pluginStore, mcpManager)
	// Attachment store: saves image/file attachments to disk so file-based
	// tools can access them by absolute path.
	attachmentStore, err := attachmentfs.New(filepath.Join(dataDir, "attachments"))
	if err != nil {
		slog.Warn("attachment store init failed", "error", err)
	}
	app := application.NewApp(application.Deps{
		Version:         version,
		DataDir:         dataDir,
		Conversations:   store,
		Providers:       providerStore,
		Credentials:     credentials,
		Skills:          skillStore,
		Memory:          &jsonstore.Memory{S: store},
		LearningEdges:   &jsonstore.LearningEdges{S: store},
		Todos:           todoStore,
		Plugins:         pluginStore,
		PluginInstaller: pluginInstaller,
		Logs:            &jsonstore.Logs{S: store},
		Settings:        &jsonstore.Settings{S: store},
		Attachments:     attachmentStore,
		Docs:            docSource,
		Bus:             bus,
		AskQuestions:    askService,
		Toolbox: &tools.Toolbox{
			Skills:       skillStore,
			Memory:       &jsonstore.Memory{S: store},
			Docs:         docSource,
			Plugins:      pluginStore,
			Todos:        todoStore,
			Searcher:     searcher,
			Settings:     &jsonstore.Settings{S: store},
			Credentials:  credentials,
			AskQuestions: askService,
			MCP:          mcpManager,
		},
		MCPToolbox:                  mcpManager,
		Factory:                     ai.NewFactory(credentials),
		EmbedderFactory:             ai.NewEmbedderFactory(),
		EmbeddingModelListerFactory: ai.NewEmbeddingModelListerFactory(),
		ModelCatalog:                modelcatalog.New(nil),
		WorkspacePicker:             workspacepicker.Zenity{},
		Logger:                      logger,
	})
	// Wire Codex runtime + OAuth adapters (optional — nil-safe if unavailable)
	if rt, err := codex.NewRuntimeAdapter(); err == nil {
		app.CodexRuntime = rt
	}
	app.CodexOAuth = codex.NewOAuthAdapter()
	app.CodexUsage = codex.NewUsageAdapter()
	app.CodexCLIAuth = codex.NewCLIAuthImporterAdapter()
	app.CodexRouter = application.NewCodexAccountRouter()

	srv := transport.New(app, logger, transport.StaticHandler(frontend.FS, dev), dev)
	// Register plugin routes: serve plugin UI static files and route
	// tool calls from plugin UIs to the plugin's MCP server.
	if pluginStore != nil {
		pluginHandler := transport.NewPluginHandler(pluginStore, pluginRuntime)
		pluginHandler.RegisterRoutes(srv.RoutesMux())
	}
	httpServer := &http.Server{
		Addr:              host + ":" + port,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	// Request contexts derive from the signal context, so SSE/WebSocket
	// handlers unblock as soon as shutdown begins.
	httpServer.BaseContext = func(net.Listener) context.Context { return ctx }
	app.StartCodexCircuitMonitor(ctx)
	app.StartAutoModelImport(ctx)
	app.StartLifecycle()
	defer app.CloseLifecycle()

	go func() {
		logger.Info("nusashell-light listening", "addr", httpServer.Addr, "data_dir", dataDir, "dev", dev, "version", version)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server failed", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")
	stop() // cancel request contexts so streaming handlers exit promptly
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdownCtx)
}

func defaultDataDir() string {
	return config.DefaultDataDir()
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// isLoopbackHost returns true if the host is a loopback address
// (127.0.0.1, ::1, localhost). Used to guard against accidental remote
// binding when the server has no auth.
func isLoopbackHost(host string) bool {
	if host == "localhost" || host == "::1" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
