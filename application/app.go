package application

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"nusashell/application/agent"
	"nusashell/application/learn"
	"nusashell/application/logs"
	"nusashell/application/media"
	"nusashell/application/memory"
	"nusashell/application/pets"
	"nusashell/application/plugins"
	"nusashell/application/provider"
	"nusashell/application/service/learnedparams"
	"nusashell/application/service/modeloverrides"
	"nusashell/application/subagent"
	"nusashell/application/telemetry"
	"nusashell/application/tools"
	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/infrastructure/jsonstore"
	clock "nusashell/pkg/time"
)

// App is the application service: it owns the use cases and dispatches RPC
// methods to them. Transport layers only map wire traffic onto Dispatch.
type App struct {
	Version string
	DataDir string
	// learningTurn runs one learning-job LLM call and returns (text,
	// conversation id, error). Nil means use the real headless turn; tests
	// install a stub so job plumbing is testable without a provider.
	learningTurn func(ctx context.Context, kind AgentKind, model, prompt string) (string, string, error)

	Conversations ConversationStore
	Providers     ProviderStore
	Credentials   CredentialStore
	Skills        SkillStore
	Experiences   ExperienceStore
	MemoryRecords MemoryRecordStore
	LearningJobs  LearningJobStore
	LearningOps   LearningOpStore
	User          MemoryDocumentStore
	// Agent is the agent-tier memory document (soul.md). Humans edit it
	// from Learning → About Agent; agents write it with file_*.
	Agent           MemoryDocumentStore
	ProjectMemory   ProjectMemoryStore
	LearningEdges   LearningEdgeStore
	LearnedParams   LearnedParamStore
	ModelOverrides  ModelOverrideStore
	Todos           ConversationTodoPort
	AskQuestions    *AskQuestionService
	Plugins         PluginStore
	PluginInstaller PluginInstaller
	Logs            LogStore
	Settings        SettingsStore
	Attachments     AttachmentStore

	Docs                        DocsSource
	Bus                         *Bus
	RoundStreams                *RoundStreamRegistry
	Toolbox                     ToolExecutor
	MCPToolbox                  MCPToolbox
	Factory                     ProviderFactory
	CodexSearchFactory          CodexSearchFactory
	ImageGeneratorFactory       ImageGeneratorFactory
	SpeechTranscriberFactory    SpeechTranscriberFactory
	OfflineTranscriberFactory   OfflineTranscriberFactory
	SpeechSynthesizerFactory    SpeechSynthesizerFactory
	OfflineSynthesizer          OfflineSynthesizer
	ImageModelListerFactory     ImageModelListerFactory
	SpeechModelListerFactory    SpeechModelListerFactory
	VideoGeneratorFactory       VideoGeneratorFactory
	VideoModelListerFactory     VideoModelListerFactory
	EmbedderFactory             EmbedderFactory
	EmbeddingModelListerFactory EmbeddingModelListerFactory
	ModelCatalog                ModelCataloger
	TTSInstaller                TTSInstaller
	STTInstaller                STTInstaller
	PetsInstaller               PetsInstaller
	DirectoryBrowser            DirectoryBrowser
	// CodexRuntime manages the official Codex CLI binary (status/download).
	// Nil is safe: Codex RPC handlers return an internal error until wired.
	CodexRuntime CodexRuntime
	// CodexOAuth performs ChatGPT OAuth PKCE login for Codex providers.
	CodexOAuth CodexOAuth
	// CodexUsage fetches ChatGPT rate-limit usage for stored OAuth tokens.
	CodexUsage CodexUsage
	// CodexContextWindowCache reads ~/.codex/models_cache.json for the
	// runtime context window Codex enforces (used by compaction later).
	CodexContextWindowCache CodexContextWindowCache
	// CodexCLIAuth imports tokens from the Codex CLI auth.json.
	CodexCLIAuth CodexCLIAuthImporter
	// CodexRouter owns sticky multi-account routing and circuit breakers.
	CodexRouter *CodexAccountRouter
	// defaultWorkspace is the fallback workspace (wired from the host home
	// dir) applied when a conversation has not picked one yet. It keeps
	// workspace-gated tools such as memory_project usable before the user
	// chooses a folder, instead of resolving relative paths against ".".
	defaultWorkspace string
	AcpAgents        AcpAgentStore
	Acp              AcpRuntime
	AcpRunStorage    domain.AcpRunStorage
	retrySleeper     RetrySleeper

	// startedAt is the wall-clock time this process came up. Conversations
	// whose last activity predates it were used before the restart; the
	// first user message after restart injects a restart announcement
	// (see handleTurnsStart).
	startedAt time.Time

	// autostartOnce guards StartPetAutoLaunch so a test or boot path that
	// calls it twice does not spawn two pet overlays.
	autostartOnce sync.Once

	// learningMu guards lazy init of learningSearcher and graphService.
	learningMu       sync.RWMutex
	learningSearcher *LearningSearcher
	graphService     *LearningGraphService
	// learnedParamsCache mirrors the persisted dynamic 400-learning
	// registry in memory so the hot path (request building) doesn't hit
	// disk. Initialized once at App construction from LearnedParams store.
	learnedParams *learnedparams.Cache
	// modelOverrides mirrors the persisted manual model-override registry
	// in memory. Applied at resolve time AFTER learned 400-adaptations so
	// manual corrections always win. Initialized once at App construction
	// from the ModelOverrides store.
	modelOverrides  *modeloverrides.Cache
	lifecycle       *LifecycleManager
	lifecycleCancel context.CancelFunc
	EmbeddingCache  *jsonstore.EmbeddingCache

	// announcementLocksMu guards lazy creation of per-conversation mutexes
	// serializing pending-announcement load-modify-save between publishers
	// (RPC handlers) and the turn worker's round-boundary drain, so entries
	// are never lost or double-injected.
	announcementLocksMu sync.Mutex
	announcementLocks   map[string]*sync.Mutex

	// edgeBuilder pre-computes deterministic/semantic learning edges;
	// used_with edges are recorded by successful turn tool usage.
	// as a background job. Nil if not configured.
	edgeBuilder *EdgeBuilder
	// Trajectory records learning layer events to a JSONL log for
	// debugging and observability. Best-effort — nil = no-op.
	Trajectory *TrajectoryRecorder

	Automation *Automation

	pluginSvc    *plugins.Service
	logsSvc      *logs.Service
	telemetrySvc *telemetry.Service
	petsSvc      *pets.Service
	mediaSvc     *media.Service
	memorySvc    *memory.Service
	learnMu      sync.Mutex
	learnSvc     *learn.Service
	toolsSvc     *tools.Service
	subagentSvc  *subagent.Service
	agentMu      sync.Mutex
	agentSvc     *agent.Service
	rateLimiter  *provider.RateLimiter

	runsMu              sync.Mutex
	runs                map[string]*TurnRun
	startMu             sync.Mutex
	conversationTurnsMu sync.Mutex
	conversationTurns   map[string]*sync.Mutex

	// pendingRuns tracks active (not-yet-completed) background run IDs
	// per conversation — the shared push-completion registry. Today the
	// producers are ACP subagents and internal delegates; future async tools
	// may queue their completion here too. Used to
	// implement HasBackgroundJobs: while any run is pending, the parent
	// agent's auto-continue chain pauses with reason
	// "awaiting-background-jobs" instead of ending the turn. When a run
	// completes, its result is injected at the next steer-style turn
	// boundary (or a new turn if the parent is idle) and then removed
	// from this map.
	pendingRunsMu sync.Mutex
	pendingRuns   map[string]map[string]string // conversationID → set of runIDs → spawning tool

	// goSafeWG tracks in-flight source=="learning" goroutines (lifecycle
	// loop and background learner jobs) so Close can drain them before
	// tests remove t.TempDir. goSafeClosed prevents Add after Wait.
	goSafeMu     sync.Mutex
	goSafeWG     sync.WaitGroup
	goSafeClosed bool

	// Logger is an optional structured logger used for crash recovery
	// diagnostics from fire-and-forget goroutines. Nil = slog.Default().
	Logger *slog.Logger
}

// goSafe runs fn in a new goroutine with panic recovery. A panic is logged
// to both the in-app Logs view (via a.log) and the structured logger (so it
// is visible even when the UI is closed) and does not crash the process.
// Use it for fire-and-forget goroutines whose panic would otherwise take
// down the whole server (agent turns, review agents, background monitors).
func (a *App) goSafe(source string, fn func()) {
	tracked := source == "learning"
	if tracked && !a.beginTrackedGoSafe() {
		return
	}
	go func() {
		defer func() {
			defer func() {
				if tracked {
					a.goSafeWG.Done()
				}
			}()
			if r := recover(); r != nil {
				stack := debug.Stack()
				// Recovery diagnostics must not introduce a second panic. A
				// failing log store or event bus is an observability failure,
				// not a reason to lose the process-level containment guarantee.
				func() {
					defer func() { _ = recover() }()
					a.log("error", source, "goroutine panic recovered: %v\n%s", r, stack)
				}()
				logger := slog.Default()
				if a != nil && a.Logger != nil {
					logger = a.Logger
				}
				func() {
					defer func() { _ = recover() }()
					logger.Error("goroutine panic recovered", "source", source, "panic", r, "stack", string(stack))
				}()
			}
		}()
		fn()
	}()
}

// beginTrackedGoSafe records a learning goroutine so Close can wait for it.
// Returns false when Close has already started draining so WaitGroup is
// never Add-ed after Wait.
func (a *App) beginTrackedGoSafe() bool {
	a.goSafeMu.Lock()
	defer a.goSafeMu.Unlock()
	if a.goSafeClosed {
		return false
	}
	a.goSafeWG.Add(1)
	return true
}

// GoSafe starts a recovered background goroutine. The composition root
// (cmd/nusashell) uses this for fire-and-forget work that must not crash
// the process (same recover as the unexported goSafe helper).
func (a *App) GoSafe(source string, fn func()) {
	a.goSafe(source, fn)
}

const mcpAutostartTimeout = 20 * time.Second

// StartAutoUpdateLoop periodically checks catalog updates and upgrades
// plugins with AutoUpdate enabled. Interval defaults to 6h. Safe no-op when
// installer or store are unavailable.
func (a *App) StartAutoUpdateLoop(ctx context.Context, interval time.Duration) {
	if a.Plugins == nil || a.PluginInstaller == nil {
		return
	}
	if interval <= 0 {
		interval = 6 * time.Hour
	}
	a.goSafe("autoupdate", func() {
		a.runAutoUpdateOnce(ctx)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.runAutoUpdateOnce(ctx)
			}
		}
	})
	a.log("info", "plugin", "auto-update loop started (interval=%s)", interval)
}

// StartMCPAutostart connects every plugin whose manifest has mcp.autostart.
// It runs synchronously so automations and the agent toolbox see those
// tools before the first FireDue tick. A failed connect is logged and
// skipped; the process still starts.
func (a *App) StartMCPAutostart(ctx context.Context) {
	if a.Plugins == nil || a.MCPToolbox == nil {
		return
	}
	list, err := a.Plugins.List()
	if err != nil {
		a.log("warn", "plugin", "autostart list: %v", err)
		return
	}
	for _, p := range list {
		if p == nil || !p.Manifest.MCP.Autostart {
			continue
		}
		if err := a.connectPluginMCP(ctx, p); err != nil {
			a.log("warn", "plugin", "autostart connect %s: %v", p.Manifest.ID, err)
			continue
		}
		a.log("info", "plugin", "autostart connected: %s", p.Manifest.ID)
	}
}

func (a *App) connectPluginMCP(ctx context.Context, p *domain.Plugin) error {
	if a.MCPToolbox == nil || p == nil {
		return nil
	}
	connectCtx, cancel := context.WithTimeout(ctx, mcpAutostartTimeout)
	defer cancel()
	_, err := a.MCPToolbox.Connect(connectCtx, p)
	return err
}

func (a *App) runAutoUpdateOnce(ctx context.Context) {
	installed, err := a.Plugins.List()
	if err != nil {
		a.log("warn", "autoupdate", "list plugins: %v", err)
		return
	}
	var targets []*domain.Plugin
	for _, p := range installed {
		if p.Manifest.AutoUpdate {
			targets = append(targets, p)
		}
	}
	if len(targets) == 0 {
		return
	}
	checkCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	updates, err := a.PluginInstaller.CheckUpdates(checkCtx, installed)
	if err != nil {
		a.log("warn", "autoupdate", "check updates: %v", err)
		return
	}
	byID := map[string]domain.PluginCatalogEntry{}
	for _, u := range updates {
		byID[u.PluginID] = u
	}
	for _, p := range targets {
		entry, ok := byID[p.Manifest.ID]
		if !ok {
			continue
		}
		updateCtx, cancelUpd := context.WithTimeout(ctx, 5*time.Minute)
		updated, err := a.PluginInstaller.Update(updateCtx, entry.ID)
		cancelUpd()
		if err != nil {
			a.log("warn", "autoupdate", "update %s: %v", p.Manifest.ID, err)
			continue
		}
		a.MCPToolbox.Drop("plugin:" + updated.Manifest.ID)
		a.log("info", "autoupdate", "auto-updated %s → v%s", updated.Manifest.Name, updated.Manifest.Version)
	}
}

// StartLifecycle starts the lifecycle (decay/prune) loop. Safe to call
// once at server startup. No-op if no lifecycle manager is configured.
func (a *App) StartLifecycle() {
	if a.lifecycle == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.lifecycleCancel = cancel
	a.goSafe("learning", func() { a.lifecycle.Run(ctx) })
	a.log("info", "learning", "lifecycle manager started (decay=%s prune=%s)", domain.DefaultLifecycleConfig().DecayInterval, domain.DefaultLifecycleConfig().PruneInterval)
}

// CloseLifecycle stops the background decay/prune loop. Safe to call
// at server shutdown. No-op if not started.
func (a *App) CloseLifecycle() {
	if a.lifecycleCancel != nil {
		a.lifecycleCancel()
		a.lifecycleCancel = nil
	}
}

// Close releases resources held by the app (file handles, background
// goroutines). Safe to call multiple times. It cancels the lifecycle
// loop, then waits for in-flight learning jobs so tests can remove
// t.TempDir on Windows and Darwin without "directory not empty".
func (a *App) Close() {
	a.CloseLifecycle()
	a.goSafeMu.Lock()
	a.goSafeClosed = true
	a.goSafeMu.Unlock()
	a.goSafeWG.Wait()
	if a.Acp != nil {
		a.Acp.Close()
	}
	if a.EmbeddingCache != nil {
		_ = a.EmbeddingCache.Close()
	}
	if a.Trajectory != nil {
		_ = a.Trajectory.Close()
	}
}

func (a *App) log(level, source, format string, args ...any) {
	e := &domain.LogEntry{
		ID:      domain.NewID(domain.IDPrefixLog),
		Time:    clock.NewTime().Time(),
		Level:   level,
		Source:  source,
		Message: fmt.Sprintf(format, args...),
	}
	if a.Logs != nil {
		a.Logs.Append(e)
	}
	if a.Bus != nil {
		a.Bus.Emit(contracts.EventLogAppend, contracts.LogAppendEvent{Entry: contracts.LogEntryDTO{
			ID: e.ID, Time: clock.NewTime(e.Time).Format(timeRFC3339), Level: e.Level, Source: e.Source, Message: e.Message,
		}})
	}
}

// MCPToolbox gives use cases access to connected MCP servers and their tools.
type MCPToolbox interface {
	ToolsFor(serverID string) ([]contracts.MCPToolDTO, bool)
	Connect(ctx context.Context, p *domain.Plugin) ([]contracts.MCPToolDTO, error)
	Drop(serverID string)
}

// ProviderFactory builds a provider adapter for a stored config + key.
type ProviderFactory = provider.Factory

// Deps is the wiring for NewApp.
type Deps struct {
	Version                     string
	DataDir                     string
	Conversations               ConversationStore
	Providers                   ProviderStore
	Credentials                 CredentialStore
	Skills                      SkillStore
	Experiences                 ExperienceStore
	MemoryRecords               MemoryRecordStore
	LearningJobs                LearningJobStore
	LearningOps                 LearningOpStore
	User                        MemoryDocumentStore
	Agent                       MemoryDocumentStore
	ProjectMemory               ProjectMemoryStore
	LearningEdges               LearningEdgeStore
	LearnedParams               LearnedParamStore
	ModelOverrides              ModelOverrideStore
	Todos                       ConversationTodoPort
	AskQuestions                *AskQuestionService
	Plugins                     PluginStore
	PluginInstaller             PluginInstaller
	Logs                        LogStore
	Settings                    SettingsStore
	Attachments                 AttachmentStore
	Docs                        DocsSource
	Bus                         *Bus
	Toolbox                     ToolExecutor
	MCPToolbox                  MCPToolbox
	Factory                     ProviderFactory
	CodexSearchFactory          CodexSearchFactory          // optional; nil = Codex web search unavailable
	ImageGeneratorFactory       ImageGeneratorFactory       // optional; nil = generate_image unavailable
	SpeechTranscriberFactory    SpeechTranscriberFactory    // optional; nil = STT routing unavailable
	OfflineTranscriberFactory   OfflineTranscriberFactory   // optional; nil = local/offline STT disabled (doc §15: not fatal)
	SpeechSynthesizerFactory    SpeechSynthesizerFactory    // optional; nil = online TTS unavailable
	OfflineSynthesizer          OfflineSynthesizer          // optional; nil = offline TTS (piper) disabled
	ImageModelListerFactory     ImageModelListerFactory     // optional; nil = skip /images/models fetch
	SpeechModelListerFactory    SpeechModelListerFactory    // optional; nil = skip speech filter fetch
	VideoGeneratorFactory       VideoGeneratorFactory       // optional; nil = generate_video unavailable
	VideoModelListerFactory     VideoModelListerFactory     // optional; nil = skip /videos/models fetch
	EmbedderFactory             EmbedderFactory             // optional; nil = BM25-only search
	EmbeddingModelListerFactory EmbeddingModelListerFactory // optional; nil = skip /embeddings/models fetch
	ModelCatalog                ModelCataloger              // optional; nil = skip enrichment from models.dev
	TTSInstaller                TTSInstaller                // optional; nil = one-click offline TTS install unavailable
	STTInstaller                STTInstaller                // optional; nil = one-click offline STT install unavailable
	PetsInstaller               PetsInstaller               // optional; nil = desktop pet unavailable (macOS / Windows builds)
	DirectoryBrowser            DirectoryBrowser            // optional; nil = in-app workspace browser unavailable
	DefaultWorkspace            string                      // fallback workspace (host home dir) when a conversation has none
	CodexRuntime                CodexRuntime                // optional; nil = Codex runtime RPCs unavailable
	CodexOAuth                  CodexOAuth                  // optional; nil = Codex OAuth login unavailable
	CodexUsage                  CodexUsage                  // optional; nil = Codex usage/circuit RPCs unavailable
	CodexContextWindowCache     CodexContextWindowCache     // optional; nil = skip Codex runtime context cache
	CodexCLIAuth                CodexCLIAuthImporter        // optional; nil = Codex CLI import unavailable
	CodexRouter                 *CodexAccountRouter         // optional; nil = no multi-account sticky/circuit state
	RetrySleeper                RetrySleeper
	AcpAgents                   AcpAgentStore
	Acp                         AcpRuntime
	AcpRunStorage               domain.AcpRunStorage
	// Logger is an optional structured logger for crash recovery from
	// fire-and-forget goroutines. Nil = slog.Default().
	Logger     *slog.Logger
	Automation *Automation
}

// App is the application core. It wires together all the stores and
// factories needed to run a conversation turn.
func NewApp(deps Deps) *App {
	if deps.Bus == nil {
		deps.Bus = NewBus()
	}
	if deps.RetrySleeper == nil {
		deps.RetrySleeper = sleepForRetry
	}
	app := &App{
		Version:                     deps.Version,
		DataDir:                     deps.DataDir,
		Conversations:               deps.Conversations,
		Providers:                   deps.Providers,
		Credentials:                 deps.Credentials,
		Skills:                      deps.Skills,
		Experiences:                 deps.Experiences,
		MemoryRecords:               deps.MemoryRecords,
		LearningJobs:                deps.LearningJobs,
		LearningOps:                 deps.LearningOps,
		Agent:                       deps.Agent,
		User:                        deps.User,
		ProjectMemory:               deps.ProjectMemory,
		LearningEdges:               deps.LearningEdges,
		LearnedParams:               deps.LearnedParams,
		Todos:                       deps.Todos,
		AskQuestions:                deps.AskQuestions,
		Plugins:                     deps.Plugins,
		PluginInstaller:             deps.PluginInstaller,
		Logs:                        deps.Logs,
		Settings:                    deps.Settings,
		Attachments:                 deps.Attachments,
		Docs:                        deps.Docs,
		Bus:                         deps.Bus,
		RoundStreams:                NewRoundStreamRegistry(),
		Toolbox:                     deps.Toolbox,
		MCPToolbox:                  deps.MCPToolbox,
		announcementLocks:           map[string]*sync.Mutex{},
		Factory:                     deps.Factory,
		CodexSearchFactory:          deps.CodexSearchFactory,
		ImageGeneratorFactory:       deps.ImageGeneratorFactory,
		SpeechTranscriberFactory:    deps.SpeechTranscriberFactory,
		OfflineTranscriberFactory:   deps.OfflineTranscriberFactory,
		SpeechSynthesizerFactory:    deps.SpeechSynthesizerFactory,
		OfflineSynthesizer:          deps.OfflineSynthesizer,
		ModelOverrides:              deps.ModelOverrides,
		ImageModelListerFactory:     deps.ImageModelListerFactory,
		SpeechModelListerFactory:    deps.SpeechModelListerFactory,
		VideoGeneratorFactory:       deps.VideoGeneratorFactory,
		VideoModelListerFactory:     deps.VideoModelListerFactory,
		EmbedderFactory:             deps.EmbedderFactory,
		EmbeddingModelListerFactory: deps.EmbeddingModelListerFactory,
		DirectoryBrowser:            deps.DirectoryBrowser,
		defaultWorkspace:            strings.TrimSpace(deps.DefaultWorkspace),
		ModelCatalog:                deps.ModelCatalog,
		TTSInstaller:                deps.TTSInstaller,
		STTInstaller:                deps.STTInstaller,
		PetsInstaller:               deps.PetsInstaller,
		CodexRuntime:                deps.CodexRuntime,
		CodexOAuth:                  deps.CodexOAuth,
		CodexUsage:                  deps.CodexUsage,
		CodexContextWindowCache:     deps.CodexContextWindowCache,
		CodexCLIAuth:                deps.CodexCLIAuth,
		CodexRouter:                 deps.CodexRouter,
		AcpAgents:                   deps.AcpAgents,
		Acp:                         deps.Acp,
		AcpRunStorage:               deps.AcpRunStorage,
		retrySleeper:                deps.RetrySleeper,
		startedAt:                   clock.NewTime().Time(),
		Logger:                      deps.Logger,
		Automation:                  deps.Automation,
		runs:                        map[string]*TurnRun{},
		pendingRuns:                 map[string]map[string]string{},
		rateLimiter:                 provider.NewRateLimiter(),
		learnedParams:               learnedparams.New(deps.LearnedParams),
		modelOverrides:              modeloverrides.New(deps.ModelOverrides),
	}
	// Wire the ask_question service callback so pending asks emit an
	// EventAskPending over the bus. The UI renders a question card from
	// this event and answers via the agent.ask.answer RPC.
	if app.AskQuestions != nil {
		app.AskQuestions.SetOnAsk(func(runID, callID, conversationID string, req domain.AskQuestionRequest) {
			app.Bus.Emit(contracts.EventAskPending, askPendingEvent(conversationID, runID, callID, req))
		})
	}
	// Wire the ACP runtime callbacks so run updates, completion,
	// permission requests, and session mode changes reach the bus and the
	// async completion path.
	if sink, ok := app.Acp.(interface {
		SetCallbacks(
			onUpdate, onDone func(*domain.AcpRun),
			onPerm func(*domain.AcpRun, domain.AcpPermissionRequest),
			onMode func(*domain.AcpRun, string),
		)
	}); ok {
		sink.SetCallbacks(
			func(run *domain.AcpRun) { app.emitAcpRun(contracts.EventAcpRunUpdated, run) },
			func(run *domain.AcpRun) {
				app.emitAcpRun(contracts.EventAcpRunDone, run)
				app.goSafe("acp", func() { app.onAcpRunDone(run) })
			},
			func(run *domain.AcpRun, req domain.AcpPermissionRequest) {
				app.emitAcpRun(contracts.EventAcpRunUpdated, run)
				perm := contracts.AcpPermissionDTO{
					ID: req.ID, SessionID: req.SessionID, ToolTitle: req.ToolTitle, ToolKind: req.ToolKind,
					Paths: req.Paths, PathCount: len(req.Paths),
				}
				if !req.RequestedAt.IsZero() {
					perm.RequestedAt = clock.NewTime(req.RequestedAt).Format(timeRFC3339)
				}
				for _, o := range req.Options {
					perm.Options = append(perm.Options, contracts.AcpPermissionOptionDTO{ID: o.ID, Name: o.Name, Kind: o.Kind})
				}
				app.Bus.Emit(contracts.EventAcpPermissionRequested, contracts.AcpPermissionEvent{RunID: run.ID, Permission: perm})
			},
			func(run *domain.AcpRun, source string) {
				app.Bus.Emit(contracts.EventAcpSessionModeChanged, contracts.AcpModeChangedEvent{
					RunID: run.ID, ModeID: run.CurrentModeID, Source: source,
				})
				app.emitAcpRun(contracts.EventAcpRunUpdated, run)
			},
		)
	}
	if deps.DataDir != "" {
		if cache, err := jsonstore.NewEmbeddingCache(deps.DataDir); err == nil {
			app.EmbeddingCache = cache
		}
		app.Trajectory = NewTrajectoryRecorder(deps.DataDir)
	}
	app.wireFeatureServices()
	// Wire the lifecycle manager (decay + prune). Started by StartLifecycle,
	// stopped by CloseLifecycle.
	if deps.MemoryRecords != nil {
		app.lifecycle = NewLifecycleManager(deps.MemoryRecords, deps.Skills, domain.DefaultLifecycleConfig())
		app.lifecycle.SetLogger(app.log)
	}
	// Reconcile learning jobs abandoned by a previous instance's restart:
	// stale "running"/"queued" rows become error so the UI shows the truth
	// and later spawns are not shadowed by ghost jobs.
	app.RecoverStaleLearningJobs()
	if svc := app.learnService(); svc != nil {
		svc.InitEdgeBuilder()
	}
	return app
}

func (a *App) wireFeatureServices() {
	pluginDeps := plugins.Deps{
		Store:     a.Plugins,
		Installer: a.PluginInstaller,
		MCP:       a.MCPToolbox,
		Skills:    a.Skills,
		Log:       a.log,
	}
	if a.Automation != nil {
		pluginDeps.Caps = a.Automation.Caps
	}
	a.pluginSvc = plugins.New(pluginDeps)
	a.logsSvc = logs.New(logs.Deps{Store: a.Logs})
	a.telemetrySvc = telemetry.New(telemetry.Deps{
		Conversations: a.Conversations,
		Providers:     a.Providers,
	})
	a.petsSvc = pets.New(a.petsDeps())
	a.mediaSvc = media.New(a.mediaDeps())
	a.memorySvc = memory.New(a.memoryDeps())
	a.learnSvc = learn.New(a.learnDeps())
	a.toolsSvc = tools.New(a.toolsDeps())
	a.subagentSvc = subagent.New(a.subagentDeps())
	a.agentSvc = agent.New(a.agentDeps())
}

// Dispatch routes an RPC method to its use case. Transport handlers are the
// only other caller of this method, which keeps handler-level tests honest.
// Domain prefixes (agent.*, ai.*, acp.*, etc.) are delegated to per-domain
// dispatcher methods; the switch below handles the remaining leaf methods.
func (a *App) Dispatch(ctx context.Context, method string, payload json.RawMessage) (any, *contracts.RPCError) {
	// Domain prefix routing: delegate to per-domain dispatchers so each
	// domain owns its routing table in a separate file.
	switch {
	case strings.HasPrefix(method, "agent."):
		return a.dispatchAgent(ctx, method, payload)
	case strings.HasPrefix(method, "ai."):
		return a.dispatchAI(method, payload)
	case strings.HasPrefix(method, "acp."):
		return a.subagentService().Dispatch(method, payload)
	case strings.HasPrefix(method, "plugin."):
		return a.pluginService().Dispatch(method, payload)
	case strings.HasPrefix(method, "skills."):
		return a.skillsService().Dispatch(method, payload)
	case strings.HasPrefix(method, "memory."):
		return a.memoryService().Dispatch(method, payload)
	case strings.HasPrefix(method, "experience."):
		return a.dispatchExperience(method, payload)
	case strings.HasPrefix(method, "learning."):
		return a.dispatchLearning(method, payload)
	case strings.HasPrefix(method, "docs."):
		return a.toolsService().Dispatch(method, payload)
	case strings.HasPrefix(method, "settings."):
		return a.dispatchSettings(method, payload)
	case strings.HasPrefix(method, "logs."):
		return a.logsService().Dispatch(method, payload)
	case strings.HasPrefix(method, "telemetry."):
		return a.telemetryService().Dispatch(method, payload)
	case strings.HasPrefix(method, "automation."):
		return a.handleAutomation(ctx, method, payload)
	}
	switch method {
	case contracts.MethodAppInfo:
		return a.handleAppInfo()
	default:
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: fmt.Sprintf("unknown method: %s", method)}
	}
}

func (a *App) handleAppInfo() (any, *contracts.RPCError) {
	settings := a.Settings.Get()
	return contracts.AppInfoResult{
		Name:    "NusaShell",
		Version: a.Version,
		DataDir: a.DataDir,
		Features: contracts.Features{
			Tools:         true,
			MCP:           true,
			Compaction:    settings.CompactionEnabled,
			PromptCaching: settings.PromptCaching,
			Automation:    a.Automation != nil,
			Providers:     []string{"messages", "responses", "chat", "codex"},
		},
	}, nil
}

// resolveModel finds the provider owning a model id and its API key. It
// returns the bare model ID (without provider prefix) so callers can pass
// it to the provider API without leaking the qualified format.
func (a *App) resolveModel(model string) (*domain.Provider, string, string, *contracts.RPCError) {
	p, m, key, rpcErr := a.resolveModelWithMeta(model)
	if rpcErr != nil {
		return nil, "", "", rpcErr
	}
	bareID := model
	if m != nil {
		bareID = m.ID
	}
	return p, bareID, key, nil
}

// resolveModelWithMeta is like resolveModel but also returns the model
// metadata (capabilities, kind, etc.). Used by the agent runtime to
// check vision support before sending image attachments.
func (a *App) resolveModelWithMeta(model string) (*domain.Provider, *domain.Model, string, *contracts.RPCError) {
	// Support "provider_id:model_id" qualified model IDs so the user can
	// disambiguate when the same model is available on multiple providers
	// (e.g. deepseek-v4-flash on both tokenrouter and openrouter). When
	// unqualified, fall back to first-match for backward compatibility.
	if providerID, modelID, ok := domain.SplitQualifiedModel(model); ok {
		p, err := a.Providers.Get(providerID)
		if err != nil || p == nil || !p.Enabled {
			return nil, nil, "", &contracts.RPCError{
				Code:    contracts.CodeValidation,
				Message: fmt.Sprintf("provider %q is not available or not enabled", a.providerNameByID(providerID)),
			}
		}
		if !p.HasModel(modelID) {
			return nil, nil, "", &contracts.RPCError{
				Code:    contracts.CodeValidation,
				Message: fmt.Sprintf("model %q is not available on provider %q", modelID, p.Name),
			}
		}
		key, _, err := a.Credentials.Get(p.ID)
		if err != nil {
			return nil, nil, "", rpcInternal(err)
		}
		m := p.FindModel(modelID)
		a.applyModelOverrides(p, m)
		return p, m, key, nil
	}
	for _, p := range a.Providers.List() {
		if !p.Enabled || !p.HasModel(model) {
			continue
		}
		key, _, err := a.Credentials.Get(p.ID)
		if err != nil {
			return nil, nil, "", rpcInternal(err)
		}
		m := p.FindModel(model)
		a.applyModelOverrides(p, m)
		return p, m, key, nil
	}
	return nil, nil, "", &contracts.RPCError{
		Code:    contracts.CodeValidation,
		Message: fmt.Sprintf("model %q is not available on any enabled provider", model),
	}
}

// applyModelOverrides applies model-metadata corrections to a freshly
// resolved model in place, in precedence order:
//
//  1. Learned 400-adaptations (context cap, disabled modalities) — reactive,
//     restrict-only, derived from upstream errors.
//  2. Manual overrides — assertive, bidirectional, set by the review agent
//     or a user. Applied last so they always win over learned adaptations.
//
// Providers come from the store as deep clones, so the mutation only affects
// this resolution's copy and never leaks back into the persisted catalog.
// This is the canonical application point for models present in the catalog;
// modelCapabilitiesWithLearned and resolveContextWindow additionally cover
// models with no catalog metadata (FindModel == nil), and both are
// idempotent with this override.
func (a *App) applyModelOverrides(p *domain.Provider, m *domain.Model) {
	if m == nil {
		return
	}
	if a.learnedParams != nil && a.learnedParams.OverrideModel(m, p.ID, m.ID) {
		a.log("info", "learning", "applied learned overrides to %s/%s (context=%d vision=%v)", p.ID, m.ID, m.Context, m.Vision)
	}
	if a.modelOverrides != nil && a.modelOverrides.Apply(m, p.ID, m.ID) {
		a.log("info", "learning", "applied manual overrides to %s/%s (context=%d vision=%v)", p.ID, m.ID, m.Context, m.Vision)
	}
}

func rpcInternal(err error) *contracts.RPCError {
	return &contracts.RPCError{Code: contracts.CodeInternal, Message: err.Error()}
}
