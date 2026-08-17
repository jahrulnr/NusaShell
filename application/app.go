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

	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/infrastructure/ai/modelcatalog"
	"nusashell/infrastructure/jsonstore"
)

// App is the application service: it owns the use cases and dispatches RPC
// methods to them. Transport layers only map wire traffic onto Dispatch.
type App struct {
	Version string
	DataDir string

	Conversations   ConversationStore
	Providers       ProviderStore
	Credentials     CredentialStore
	Skills          SkillStore
	Memory          MemoryStore
	LearningEdges   LearningEdgeStore
	Todos           ConversationTodoPort
	AskQuestions    *AskQuestionService
	Plugins         PluginStore
	PluginInstaller PluginInstaller
	Logs            LogStore
	Settings        SettingsStore
	Attachments     AttachmentStore

	Docs                        DocsSource
	Bus                         *Bus
	Toolbox                     ToolExecutor
	MCPToolbox                  MCPToolbox
	Factory                     ProviderFactory
	EmbedderFactory             EmbedderFactory
	EmbeddingModelListerFactory EmbeddingModelListerFactory
	ModelCatalog                *modelcatalog.Catalog
	WorkspacePicker             WorkspacePicker
	CodexRuntime                CodexRuntime
	CodexOAuth                  CodexOAuth
	CodexUsage                  CodexUsage
	CodexCLIAuth                CodexCLIAuthImporter
	CodexRouter                 *CodexAccountRouter
	AcpAgents                   AcpAgentStore
	Acp                         AcpRuntime
	retrySleeper                RetrySleeper

	// learningMu guards lazy init of learningSearcher and graphService,
	// plus the per-conversation turn counter for threshold-based review.
	learningMu       sync.Mutex
	learningSearcher *LearningSearcher
	graphService     *LearningGraphService
	ReviewAgent      *BackgroundReviewAgent
	lifecycle        *LifecycleManager
	lifecycleCancel  context.CancelFunc
	// turnsSinceReview tracks turns since the last learning review per
	// conversation. When the count reaches LearningReviewThreshold, the
	// review fires and the counter resets.
	turnsSinceReview map[string]int
	// EmbeddingCache stores computed embedding vectors to avoid
	// re-embedding the same content on every search. Content-addressed
	// by (model_id, sha256(normalized_text)).
	EmbeddingCache *jsonstore.EmbeddingCache
	// edgeBuilder pre-computes learning edges (similarity + token overlap)
	// as a background job. Nil if not configured.
	edgeBuilder *EdgeBuilder
	// Trajectory records learning layer events to a JSONL log for
	// debugging and observability. Best-effort — nil = no-op.
	Trajectory *TrajectoryRecorder

	runsMu  sync.Mutex
	runs    map[string]*TurnRun
	startMu sync.Mutex

	// Logger is an optional structured logger used for crash recovery
	// diagnostics from fire-and-forget goroutines. Nil = slog.Default().
	Logger *slog.Logger
}

// MCPToolbox gives use cases access to connected MCP servers and their tools.
type MCPToolbox interface {
	ToolsFor(serverID string) ([]contracts.MCPToolDTO, bool)
	Connect(ctx context.Context, p *domain.Plugin) ([]contracts.MCPToolDTO, error)
	Drop(serverID string)
}

type DocsSource interface {
	List() []DocMeta
	Search(query string, limit int) []DocHit
	Read(id string) (DocFull, error)
}

type DocMeta struct {
	ID    string
	Title string
	Path  string
}

type DocHit struct {
	DocMeta
	Snippet string
}

type DocFull struct {
	DocMeta
	Content string
}

// ProviderFactory builds a provider adapter for a stored config + key.
type ProviderFactory func(ctx context.Context, p *domain.Provider, apiKey string) (AIProvider, error)

// TurnRun tracks one streaming agent turn.
type TurnRun struct {
	ID             string
	ConversationID string
	MessageID      string
	Ctx            context.Context
	Cancel         context.CancelFunc

	steerMu     sync.Mutex
	steerQueued *SteerEntry
}

// SteerEntry is a user message queued for injection at the next tool round
// boundary while a turn is running.
type SteerEntry struct {
	ID      string
	Text    string
	Status  string // "queued" | "applied" | "cancelled"
	Message domain.Message
}

// queueSteer stores a steer entry for this run. Returns false if a steer is
// already queued (only one at a time).
func (r *TurnRun) queueSteer(entry *SteerEntry) bool {
	r.steerMu.Lock()
	defer r.steerMu.Unlock()
	if r.steerQueued != nil {
		return false
	}
	r.steerQueued = entry
	return true
}

// cancelSteer removes a queued steer. Returns false if no queued steer exists
// or it has already been applied.
func (r *TurnRun) cancelSteer() bool {
	r.steerMu.Lock()
	defer r.steerMu.Unlock()
	if r.steerQueued == nil || r.steerQueued.Status != "queued" {
		return false
	}
	r.steerQueued.Status = "cancelled"
	r.steerQueued = nil
	return true
}

// cancelSteerEntry removes a queued steer and returns it (with its text) so the
// caller can emit a cancel event that lets the frontend restore the draft to
// the composer. Returns nil if no queued steer exists.
func (r *TurnRun) cancelSteerEntry() *SteerEntry {
	r.steerMu.Lock()
	defer r.steerMu.Unlock()
	if r.steerQueued == nil || r.steerQueued.Status != "queued" {
		return nil
	}
	r.steerQueued.Status = "cancelled"
	entry := r.steerQueued
	r.steerQueued = nil
	return entry
}

// drainSteer returns the queued steer entry and marks it applied, or nil if
// no steer is queued. Called by the agent loop at a safe boundary.
func (r *TurnRun) drainSteer() *SteerEntry {
	r.steerMu.Lock()
	defer r.steerMu.Unlock()
	if r.steerQueued == nil || r.steerQueued.Status != "queued" {
		return nil
	}
	r.steerQueued.Status = "applied"
	entry := r.steerQueued
	r.steerQueued = nil
	return entry
}

// queuedSteer returns the current queued steer without consuming it.
func (r *TurnRun) queuedSteer() *SteerEntry {
	r.steerMu.Lock()
	defer r.steerMu.Unlock()
	if r.steerQueued == nil || r.steerQueued.Status != "queued" {
		return nil
	}
	return r.steerQueued
}

// Deps is the wiring for NewApp.
type Deps struct {
	Version                     string
	DataDir                     string
	Conversations               ConversationStore
	Providers                   ProviderStore
	Credentials                 CredentialStore
	Skills                      SkillStore
	Memory                      MemoryStore
	LearningEdges               LearningEdgeStore
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
	EmbedderFactory             EmbedderFactory             // optional; nil = BM25-only search
	EmbeddingModelListerFactory EmbeddingModelListerFactory // optional; nil = skip /embeddings/models fetch
	ModelCatalog                *modelcatalog.Catalog       // optional; nil = skip enrichment from models.dev
	WorkspacePicker             WorkspacePicker
	RetrySleeper                RetrySleeper
	AcpAgents                   AcpAgentStore
	Acp                         AcpRuntime
	// Logger is an optional structured logger for crash recovery from
	// fire-and-forget goroutines. Nil = slog.Default().
	Logger *slog.Logger
}

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
		Memory:                      deps.Memory,
		LearningEdges:               deps.LearningEdges,
		Todos:                       deps.Todos,
		AskQuestions:                deps.AskQuestions,
		Plugins:                     deps.Plugins,
		PluginInstaller:             deps.PluginInstaller,
		Logs:                        deps.Logs,
		Settings:                    deps.Settings,
		Attachments:                 deps.Attachments,
		Docs:                        deps.Docs,
		Bus:                         deps.Bus,
		Toolbox:                     deps.Toolbox,
		MCPToolbox:                  deps.MCPToolbox,
		Factory:                     deps.Factory,
		EmbedderFactory:             deps.EmbedderFactory,
		EmbeddingModelListerFactory: deps.EmbeddingModelListerFactory,
		WorkspacePicker:             deps.WorkspacePicker,
		ModelCatalog:                deps.ModelCatalog,
		AcpAgents:                   deps.AcpAgents,
		Acp:                         deps.Acp,
		retrySleeper:                deps.RetrySleeper,
		Logger:                      deps.Logger,
		runs:                        map[string]*TurnRun{},
		turnsSinceReview:            map[string]int{},
	}
	// Wire the background LLM review agent. Uses the conversation's
	// configured model ("global LLM") with a restricted toolset and the
	// review prompt (single pass for both memory and skills).
	app.ReviewAgent = NewBackgroundReviewAgent(app, DefaultReviewSettings())
	// Wire the ask_question service callback so pending asks emit an
	// EventAskPending over the bus. The UI renders a question card from
	// this event and answers via the agent.ask.answer RPC.
	if app.AskQuestions != nil {
		app.AskQuestions.SetOnAsk(func(runID, callID, conversationID string, req domain.AskQuestionRequest) {
			app.Bus.Emit(contracts.EventAskPending, askPendingEvent(conversationID, runID, callID, req))
		})
	}
	app.wireAcpCallbacks()
	// Wire the lifecycle manager (decay + prune). Started by StartLifecycle,
	// stopped by CloseLifecycle.
	if deps.Memory != nil {
		app.lifecycle = NewLifecycleManager(deps.Memory, deps.Skills, DefaultLifecycleConfig())
	}
	// Wire the embedding cache + edge builder. The cache persists to
	// learning_embeddings.jsonl and avoids re-embedding on every search.
	// The edge builder pre-computes similarity + token overlap edges as
	// a background job.
	if deps.DataDir != "" {
		if cache, err := jsonstore.NewEmbeddingCache(deps.DataDir); err == nil {
			app.EmbeddingCache = cache
		}
		app.Trajectory = NewTrajectoryRecorder(deps.DataDir)
	}
	if deps.Memory != nil && deps.Skills != nil {
		app.edgeBuilder = NewEdgeBuilder(
			deps.Memory, deps.Skills, app.graph(),
			nil, // embedder is resolved lazily via ResolveEmbedder
			app.EmbeddingCache,
			DefaultEdgeBuilderConfig(),
			"", // model ID resolved lazily
		)
	}
	return app
}

// learningSearch returns the lazy-initialized LearningSearcher. The
// searcher is built on first call so it sees the latest embedding settings
// (which may be configured after App construction via the settings UI).
func (a *App) learningSearch() *LearningSearcher {
	a.learningMu.Lock()
	defer a.learningMu.Unlock()
	if a.learningSearcher != nil {
		return a.learningSearcher
	}
	var embed Embedder
	if a.EmbedderFactory != nil {
		st := a.Settings.Get()
		embed = ResolveEmbedder(a.Providers, a.Credentials, a.EmbedderFactory, st.EmbeddingProviderID)
	}
	// Inline graph init to avoid re-locking learningMu (graph() also locks).
	if a.graphService == nil {
		if a.LearningEdges != nil {
			a.graphService = NewLearningGraphService(a.LearningEdges)
		}
	}
	a.learningSearcher = NewLearningSearcher(a.Skills, a.Memory, embed, a.graphService)
	return a.learningSearcher
}

// graph returns the lazy-initialized LearningGraphService.
func (a *App) graph() *LearningGraphService {
	a.learningMu.Lock()
	defer a.learningMu.Unlock()
	if a.graphService != nil {
		return a.graphService
	}
	if a.LearningEdges == nil {
		return nil
	}
	a.graphService = NewLearningGraphService(a.LearningEdges)
	return a.graphService
}

// resolveEmbedder returns the configured embedder and its model ID.
// Returns nil, "" if no embedder is available.
func (a *App) resolveEmbedder() (Embedder, string) {
	if a.EmbedderFactory == nil {
		return nil, ""
	}
	st := a.Settings.Get()
	embed := ResolveEmbedder(a.Providers, a.Credentials, a.EmbedderFactory, st.EmbeddingProviderID)
	if embed == nil {
		return nil, ""
	}
	return embed, st.EmbeddingModelID
}

// InvalidateLearningSearcher forces the next learningSearch() call to
// rebuild the searcher with fresh embedding settings. Called when the
// embedding model selection changes in settings.
func (a *App) InvalidateLearningSearcher() {
	a.learningMu.Lock()
	defer a.learningMu.Unlock()
	a.learningSearcher = nil
}

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

// StartLifecycle starts the lifecycle (decay/prune) loop and the
// compaction-triggered learning review subscriber. Safe to call once at
// server startup. No-op if no lifecycle manager is configured.
func (a *App) StartLifecycle() {
	if a.lifecycle == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.lifecycleCancel = cancel
	a.goSafe("learning", func() { a.lifecycle.Run(ctx) })
	a.log("info", "learning", "lifecycle manager started (decay=%s prune=%s)", DefaultLifecycleConfig().DecayInterval, DefaultLifecycleConfig().PruneInterval)

	// Subscribe to compaction events: when a conversation is compacted,
	// flush the learning review for that conversation. Compaction is a
	// natural checkpoint — the full context is being summarized, so
	// extracting learnings at the same time is free context-wise.
	if a.ReviewAgent != nil && a.Bus != nil {
		a.goSafe("learning", func() { a.subscribeCompactionReview(ctx) })
	}
}

// subscribeCompactionReview listens for compaction events and triggers
// a learning review for the compacted conversation. Exits when ctx is
// cancelled.
func (a *App) subscribeCompactionReview(ctx context.Context) {
	_, events, unsubscribe := a.Bus.Subscribe()
	defer unsubscribe()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			if ev.Type != contracts.EventCompacted {
				continue
			}
			var ce contracts.CompactedEvent
			if err := json.Unmarshal(ev.Payload, &ce); err != nil {
				continue
			}
			a.flushLearningReview(ce.ConversationID)
		}
	}
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
// goroutines). Safe to call multiple times. Tests should call this via
// t.Cleanup so that Windows does not fail TempDir removal with "file
// in use" errors from the embedding cache and trajectory log handles.
func (a *App) Close() {
	a.CloseLifecycle()
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
		ID:      domain.NewID("log"),
		Time:    time.Now().UTC(),
		Level:   level,
		Source:  source,
		Message: fmt.Sprintf(format, args...),
	}
	a.Logs.Append(e)
	a.Bus.Emit(contracts.EventLogAppend, contracts.LogAppendEvent{Entry: contracts.LogEntryDTO{
		ID: e.ID, Time: e.Time.Format(timeRFC3339), Level: e.Level, Source: e.Source, Message: e.Message,
	}})
}

// goSafe runs fn in a new goroutine with panic recovery. A panic is logged
// to both the in-app Logs view (via a.log) and the structured logger (so it
// is visible even when the UI is closed) and does not crash the process.
// Use it for fire-and-forget goroutines whose panic would otherwise take
// down the whole server (agent turns, review agents, background monitors).
func (a *App) goSafe(source string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				a.log("error", source, "goroutine panic recovered: %v\n%s", r, stack)
				logger := a.Logger
				if logger == nil {
					logger = slog.Default()
				}
				logger.Error("goroutine panic recovered", "source", source, "panic", r, "stack", string(stack))
			}
		}()
		fn()
	}()
}

// Dispatch routes an RPC method to its use case. Transport handlers are the
// only other caller of this method, which keeps handler-level tests honest.
func (a *App) Dispatch(ctx context.Context, method string, payload json.RawMessage) (any, *contracts.RPCError) {
	switch method {
	case contracts.MethodAppInfo:
		return a.handleAppInfo()
	case contracts.MethodConversationsList:
		return a.handleConversationsList()
	case contracts.MethodConversationsCreate:
		var req contracts.ConversationCreateRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleConversationsCreate(req)
	case contracts.MethodConversationsGet:
		var req contracts.ConversationIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleConversationsGet(req)
	case contracts.MethodConversationsChunk:
		var req contracts.ConversationChunkRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleConversationsChunk(req)
	case contracts.MethodConversationsRename:
		var req contracts.ConversationRenameRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleConversationsRename(req)
	case contracts.MethodConversationsDelete:
		var req contracts.ConversationIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleConversationsDelete(req)
	case contracts.MethodConversationsPickWorkspace:
		var req contracts.ConversationIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleConversationsPickWorkspace(req)
	case contracts.MethodTurnsStart:
		var req contracts.TurnStartRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleTurnsStart(ctx, req)
	case contracts.MethodTurnsStop:
		var req contracts.TurnStopRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleTurnsStop(req)
	case contracts.MethodTurnsRetry:
		var req contracts.TurnRetryRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleTurnsRetry(ctx, req)
	case contracts.MethodTurnsSteer:
		var req contracts.TurnSteerRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleTurnsSteer(ctx, req)
	case contracts.MethodTurnsCancelSteer:
		var req contracts.TurnCancelSteerRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleTurnsCancelSteer(req)
	case contracts.MethodTurnsActive:
		var req contracts.ConversationIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleTurnsActive(req)
	case contracts.MethodAskAnswer:
		var req contracts.AskAnswerRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleAskAnswer(req)
	case contracts.MethodAskCancel:
		var req contracts.AskCancelRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleAskCancel(req)
	case contracts.MethodAskPending:
		var req contracts.AskPendingListRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleAskPendingList(req)
	case contracts.MethodProvidersList:
		return a.handleProvidersList()
	case contracts.MethodProvidersSave:
		var req contracts.ProviderSaveRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleProvidersSave(req)
	case contracts.MethodProvidersDelete:
		var req contracts.ProviderIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleProvidersDelete(req)
	case contracts.MethodProvidersTest:
		var req contracts.ProviderIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleProvidersTest(req)
	case contracts.MethodProvidersImport:
		var req contracts.ProviderIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleProvidersImport(req)
	case contracts.MethodModelsList:
		return a.handleModelsList()
	case contracts.MethodCodexLogin:
		var req contracts.CodexLoginRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleCodexLogin(req)
	case contracts.MethodCodexImport:
		var req contracts.CodexImportRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleCodexImport(req)
	case contracts.MethodCodexLogout:
		var req contracts.CodexLogoutRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleCodexLogout(req)
	case contracts.MethodCodexAccountsList:
		var req contracts.CodexAccountsListRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleCodexAccountsList(req)
	case contracts.MethodCodexAccountsSwitch:
		var req contracts.CodexAccountsSwitchRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleCodexAccountsSwitch(req)
	case contracts.MethodCodexRefreshCircuits:
		return a.handleCodexRefreshCircuits()
	case contracts.MethodCodexRuntimeStatus:
		return a.handleCodexRuntimeStatus()
	case contracts.MethodCodexRuntimeDownload:
		var req contracts.CodexRuntimeDownloadRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleCodexRuntimeDownload(req)
	case contracts.MethodCodexUsage:
		var req contracts.CodexUsageRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleCodexUsage(req)
	case contracts.MethodSkillsList:
		return a.handleSkillsList()
	case contracts.MethodSkillsRead:
		var req contracts.SkillIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleSkillsRead(req)
	case contracts.MethodSkillsSave:
		var req contracts.SkillSaveRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleSkillsSave(req)
	case contracts.MethodSkillsDelete:
		var req contracts.SkillIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleSkillsDelete(req)
	case contracts.MethodSkillsRun:
		var req contracts.SkillIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleSkillsRun(req)
	case contracts.MethodPluginList:
		return a.handlePluginList()
	case contracts.MethodPluginSave:
		var req contracts.PluginSaveRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handlePluginSave(req)
	case contracts.MethodPluginDelete:
		var req contracts.PluginIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handlePluginDelete(req)
	case contracts.MethodPluginTest:
		var req contracts.PluginIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handlePluginTest(req)
	case contracts.MethodPluginStop:
		var req contracts.PluginIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handlePluginStop(req)
	case contracts.MethodPluginToolsList:
		return a.handlePluginToolsList()
	case contracts.MethodPluginCatalog:
		return a.handlePluginCatalog()
	case contracts.MethodPluginInstall:
		var req contracts.PluginInstallRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handlePluginInstall(req)
	case contracts.MethodPluginUninstall:
		var req contracts.PluginIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handlePluginUninstall(req)
	case contracts.MethodPluginCheckUpdates:
		return a.handlePluginCheckUpdates()
	case contracts.MethodPluginSetAutoUpdate:
		var req contracts.PluginSetFlagRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handlePluginSetAutoUpdate(req)
	case contracts.MethodPluginSetAutoStart:
		var req contracts.PluginSetFlagRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handlePluginSetAutoStart(req)
	case contracts.MethodPluginUpdate:
		var req contracts.PluginIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handlePluginUpdate(req)
	case contracts.MethodMemoryList:
		return a.handleMemoryList()
	case contracts.MethodMemorySave:
		var req contracts.MemorySaveRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleMemorySave(req)
	case contracts.MethodMemorySearch:
		var req contracts.MemorySearchRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleMemorySearch(req)
	case contracts.MethodMemoryDelete:
		var req contracts.MemoryIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleMemoryDelete(req)
	case contracts.MethodLearningSearch:
		var req contracts.LearningSearchRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleLearningSearch(req)
	case contracts.MethodLearningGraph:
		return a.handleLearningGraph()
	case contracts.MethodTodosGet:
		var req contracts.TodosGetRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleTodosGet(req)
	case contracts.MethodTodosDelete:
		var req contracts.TodosDeleteRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleTodosDelete(req)
	case contracts.MethodDocsList:
		return a.handleDocsList()
	case contracts.MethodDocsSearch:
		var req contracts.DocsSearchRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleDocsSearch(req)
	case contracts.MethodDocsRead:
		var req contracts.DocReadRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleDocsRead(req)
	case contracts.MethodLogsList:
		var req contracts.LogsListRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleLogsList(req)
	case contracts.MethodLogsClear:
		return a.handleLogsClear()
	case contracts.MethodSettingsGet:
		return a.handleSettingsGet()
	case contracts.MethodSettingsSet:
		var req contracts.SettingsSetRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleSettingsSet(req)
	case contracts.MethodAcpAgentsList:
		return a.handleAcpAgentsList()
	case contracts.MethodAcpAgentsSave:
		var req contracts.AcpAgentSaveRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleAcpAgentsSave(req)
	case contracts.MethodAcpAgentsDelete:
		var req contracts.AcpAgentIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleAcpAgentsDelete(req)
	case contracts.MethodAcpAgentsProbe:
		var req contracts.AcpAgentIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleAcpAgentsProbe(req)
	case contracts.MethodAcpAgentsAuthenticate:
		var req contracts.AcpAuthenticateRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleAcpAgentsAuthenticate(req)
	case contracts.MethodAcpAgentsRefreshCatalog:
		var req contracts.AcpAgentIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleAcpAgentsRefreshCatalog(req)
	case contracts.MethodAcpRunsList:
		var req contracts.AcpRunsListRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleAcpRunsList(req)
	case contracts.MethodAcpRunsGet:
		var req contracts.AcpRunIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleAcpRunsGet(req)
	case contracts.MethodAcpRunsSteer:
		var req contracts.AcpRunSteerRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleAcpRunsSteer(req)
	case contracts.MethodAcpRunsStop:
		var req contracts.AcpRunIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleAcpRunsStop(req)
	case contracts.MethodAcpRunsWait:
		var req contracts.AcpRunWaitRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleAcpRunsWait(req)
	case contracts.MethodAcpRunsPromote:
		var req contracts.AcpRunPromoteRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleAcpRunsPromote(req)
	case contracts.MethodAcpRunsSetMode:
		var req contracts.AcpRunSetModeRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleAcpRunsSetMode(req)
	case contracts.MethodAcpPermissionDecide:
		var req contracts.AcpPermissionDecideRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleAcpPermissionDecide(req)
	default:
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: fmt.Sprintf("unknown method: %s", method)}
	}
}

func (a *App) handleAppInfo() (any, *contracts.RPCError) {
	settings := a.Settings.Get()
	return contracts.AppInfoResult{
		Name:       "NusaShell Light",
		Version:    a.Version,
		DataDir:    a.DataDir,
		Transports: []string{"http", "ws", "sse"},
		Features: contracts.Features{
			Tools:         true,
			MCP:           true,
			Compaction:    settings.CompactionEnabled,
			PromptCaching: settings.PromptCaching,
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
	if providerID, modelID, ok := splitQualifiedModel(model); ok {
		p, err := a.Providers.Get(providerID)
		if err != nil || p == nil || !p.Enabled {
			return nil, nil, "", &contracts.RPCError{
				Code:    contracts.CodeValidation,
				Message: fmt.Sprintf("provider %q is not available or not enabled", providerID),
			}
		}
		if !p.HasModel(modelID) {
			return nil, nil, "", &contracts.RPCError{
				Code:    contracts.CodeValidation,
				Message: fmt.Sprintf("model %q is not available on provider %q", modelID, providerID),
			}
		}
		key, has, err := a.Credentials.Get(p.ID)
		if err != nil {
			return nil, nil, "", rpcInternal(err)
		}
		if !has && requiresKey(p.Kind) {
			return nil, nil, "", &contracts.RPCError{Code: contracts.CodeConflict, Message: fmt.Sprintf("provider %q has no API key", p.Name)}
		}
		if !has && p.Kind == domain.ProviderCodex {
			return nil, nil, "", &contracts.RPCError{Code: contracts.CodeConflict, Message: fmt.Sprintf("provider %q is not logged in — use the Codex login command to authenticate with your ChatGPT account", p.Name)}
		}
		return p, p.FindModel(modelID), key, nil
	}
	for _, p := range a.Providers.List() {
		if !p.Enabled || !p.HasModel(model) {
			continue
		}
		key, has, err := a.Credentials.Get(p.ID)
		if err != nil {
			return nil, nil, "", rpcInternal(err)
		}
		if !has && requiresKey(p.Kind) {
			return nil, nil, "", &contracts.RPCError{Code: contracts.CodeConflict, Message: fmt.Sprintf("provider %q has no API key", p.Name)}
		}
		if !has && p.Kind == domain.ProviderCodex {
			return nil, nil, "", &contracts.RPCError{Code: contracts.CodeConflict, Message: fmt.Sprintf("provider %q is not logged in — use the Codex login command to authenticate with your ChatGPT account", p.Name)}
		}
		return p, p.FindModel(model), key, nil
	}
	return nil, nil, "", &contracts.RPCError{
		Code:    contracts.CodeValidation,
		Message: fmt.Sprintf("model %q is not available on any enabled provider", model),
	}
}

// splitQualifiedModel splits a "provider_id:model_id" string on the first
// colon. Returns ok=false when the string is a bare model ID (no colon).
func splitQualifiedModel(s string) (providerID, modelID string, ok bool) {
	idx := strings.IndexByte(s, ':')
	if idx <= 0 {
		return "", "", false
	}
	return s[:idx], s[idx+1:], true
}

func requiresKey(kind domain.ProviderKind) bool {
	// local endpoints (Ollama, LM Studio via chat kind) work without a key
	// Codex uses OAuth tokens stored in CredentialStore, not a user-supplied key
	return kind == domain.ProviderMessages || kind == domain.ProviderResponses
}

func rpcInternal(err error) *contracts.RPCError {
	return &contracts.RPCError{Code: contracts.CodeInternal, Message: err.Error()}
}
