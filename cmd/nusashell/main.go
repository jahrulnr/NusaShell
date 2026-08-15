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
	"nusashell/infrastructure/config"
	"nusashell/infrastructure/docs"
	"nusashell/infrastructure/jsonstore"
	"nusashell/infrastructure/mcpclient"
	"nusashell/infrastructure/sqlitestore"
	"nusashell/infrastructure/tools"
	"nusashell/infrastructure/workspacepicker"
	"nusashell/transport"
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
	todoStore := jsonstore.NewTodoStore(filepath.Join(dataDir, "conversation-todos.json"))
	app := application.NewApp(application.Deps{
		Version:       version,
		DataDir:       dataDir,
		Conversations: store,
		Providers:     &jsonstore.Providers{S: store},
		Credentials:   credentials,
		Skills:        &jsonstore.Skills{S: store},
		Memory:        &jsonstore.Memory{S: store},
		LearningEdges: &jsonstore.LearningEdges{S: store},
		Todos:         todoStore,
		MCP:           &jsonstore.MCP{S: store},
		Logs:          &jsonstore.Logs{S: store},
		Settings:      &jsonstore.Settings{S: store},
		Docs:          docSource,
		Bus:           bus,
		Toolbox: &tools.Toolbox{
			Skills:     &jsonstore.Skills{S: store},
			Memory:     &jsonstore.Memory{S: store},
			Docs:       docSource,
			MCPServers: &jsonstore.MCP{S: store},
			Todos:      todoStore,
			MCP:        mcpManager,
		},
		MCPToolbox:                  mcpManager,
		Factory:                     ai.NewFactory(credentials),
		EmbedderFactory:             ai.NewEmbedderFactory(),
		EmbeddingModelListerFactory: ai.NewEmbeddingModelListerFactory(),
		WorkspacePicker:             workspacepicker.Zenity{},
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
