package application

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/infrastructure/ai/core"
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
	Primary         PrimaryStore
	Fragments       FragmentStore
	LearningEdges   LearningEdgeStore
	LearnedParams   LearnedParamStore
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
	WorkspacePicker             WorkspacePicker
	AcpAgents                   AcpAgentStore
	Acp                         AcpRuntime
	AcpRunStorage               domain.AcpRunStorage
	retrySleeper                RetrySleeper
	imageGenSem                 chan struct{}

	// startedAt is the wall-clock time this process came up. Conversations
	// whose last activity predates it were used before the restart; the
	// first user message after restart injects a restart announcement
	// (see handleTurnsStart).
	startedAt time.Time

	// ttsInstallMu guards the single in-flight offline TTS install
	// (settings.tts_install_start is single-flight).
	ttsInstallMu     sync.Mutex
	ttsInstallActive bool

	// sttInstallMu guards the single in-flight offline STT install plus
	// its cancel handle (settings.stt_install_cancel).
	sttInstallMu     sync.Mutex
	sttInstallActive bool
	sttInstallCancel context.CancelFunc
	sttInstallDoneCh chan struct{}

	// learningMu guards lazy init of learningSearcher and graphService,
	// plus the per-conversation turn counter for threshold-based review.
	learningMu       sync.RWMutex
	learningSearcher *LearningSearcher
	graphService     *LearningGraphService
	// learnedParamsCache mirrors the persisted dynamic 400-learning
	// registry in memory so the hot path (request building) doesn't hit
	// disk. Initialized once at App construction from LearnedParams store.
	learnedParams   *learnedParamsCache
	ReviewAgent     *BackgroundReviewAgent
	lifecycle       *LifecycleManager
	lifecycleCancel context.CancelFunc
	// turnsSinceReview tracks turns since the last learning review per
	// conversation. When the count reaches LearningReviewThreshold, the
	// review fires and the counter resets.
	turnsSinceReview map[string]int
	// toolCallsSinceReview tracks tool calls since the last learning
	// review per conversation. When the count reaches SkillNudgeInterval,
	// the review fires and the counter resets. Independent of the turn
	// counter so tool-heavy but user-turn-light coding sessions still
	// trigger skill review.
	toolCallsSinceReview map[string]int
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

	Automation *Automation

	runsMu  sync.Mutex
	runs    map[string]*TurnRun
	startMu sync.Mutex

	// pendingSubagents tracks active (not-yet-completed) ACP subagent run
	// IDs per conversation. Used to implement HasBackgroundJobs: while any
	// subagent is running, the parent agent's auto-continue chain pauses
	// with reason "awaiting-background-jobs" instead of ending the turn.
	// When a subagent completes, the OnDone callback removes it from this
	// map and triggers a new turn so the parent agent processes the result.
	pendingSubagentsMu sync.Mutex
	pendingSubagents   map[string]map[string]bool // conversationID → set of runIDs

	// rlMu guards per-provider rate-limit windows (see rate_limit.go).
	// MarkProviderRateLimited records when a 429 window clears so client
	// requests can be gated and messages can be user-friendly.
	rlMu      sync.Mutex
	rlWindows map[string]time.Time // providerID → next allowed request time

	// Logger is an optional structured logger used for crash recovery
	// diagnostics from fire-and-forget goroutines. Nil = slog.Default().
	Logger *slog.Logger
}

// trackPendingSubagent records that a subagent run is active for a
// conversation. Called when the subagent tool spawns a run.
func (a *App) trackPendingSubagent(conversationID, runID string) {
	if conversationID == "" || runID == "" {
		return
	}
	a.pendingSubagentsMu.Lock()
	defer a.pendingSubagentsMu.Unlock()
	if a.pendingSubagents == nil {
		a.pendingSubagents = map[string]map[string]bool{}
	}
	if a.pendingSubagents[conversationID] == nil {
		a.pendingSubagents[conversationID] = map[string]bool{}
	}
	a.pendingSubagents[conversationID][runID] = true
}

// untrackPendingSubagent removes a completed subagent run. Returns true
// if the run was found and removed, false if it was not tracked.
func (a *App) untrackPendingSubagent(conversationID, runID string) bool {
	a.pendingSubagentsMu.Lock()
	defer a.pendingSubagentsMu.Unlock()
	set := a.pendingSubagents[conversationID]
	if set == nil {
		return false
	}
	if !set[runID] {
		return false
	}
	delete(set, runID)
	if len(set) == 0 {
		delete(a.pendingSubagents, conversationID)
	}
	return true
}

// hasPendingSubagents reports whether any subagent runs are still active
// for the given conversation. Used by the auto-continue policy to decide
// whether to pause (awaiting-background-jobs) or proceed.
func (a *App) hasPendingSubagents(conversationID string) bool {
	a.pendingSubagentsMu.Lock()
	defer a.pendingSubagentsMu.Unlock()
	return len(a.pendingSubagents[conversationID]) > 0
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
type ProviderFactory func(ctx context.Context, p *domain.Provider, apiKey string) (core.Provider, error)

// TurnRun tracks one streaming agent turn.
type TurnRun struct {
	ID             string
	ConversationID string
	MessageID      string
	Ctx            context.Context
	Cancel         context.CancelFunc
	// ProviderID is the resolved provider for this turn, used by the
	// dynamic 400-learning classifier to key learned param rules.
	ProviderID string
	// Headless marks unattended turns (pipeline agent steps). When true,
	// ACP subagent tools are filtered from the tool set so permission
	// prompts never stall a headless run.
	Headless bool
	// RiskTierCap is the maximum ACP RiskTier that may be promoted to
	// during a headless turn. Derived from the workflow TrustLevel via
	// domain.TrustLevelToRiskTierCap. Empty means no cap (interactive turns).
	RiskTierCap domain.RiskTier

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
	Primary                     PrimaryStore
	Fragments                   FragmentStore
	LearningEdges               LearningEdgeStore
	LearnedParams               LearnedParamStore
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
	WorkspacePicker             WorkspacePicker
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
		Memory:                      deps.Memory,
		Primary:                     deps.Primary,
		Fragments:                   deps.Fragments,
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
		Toolbox:                     deps.Toolbox,
		MCPToolbox:                  deps.MCPToolbox,
		Factory:                     deps.Factory,
		ImageGeneratorFactory:       deps.ImageGeneratorFactory,
		SpeechTranscriberFactory:    deps.SpeechTranscriberFactory,
		OfflineTranscriberFactory:   deps.OfflineTranscriberFactory,
		OfflineSynthesizer:          deps.OfflineSynthesizer,
		ImageModelListerFactory:     deps.ImageModelListerFactory,
		SpeechModelListerFactory:    deps.SpeechModelListerFactory,
		VideoGeneratorFactory:       deps.VideoGeneratorFactory,
		VideoModelListerFactory:     deps.VideoModelListerFactory,
		EmbedderFactory:             deps.EmbedderFactory,
		EmbeddingModelListerFactory: deps.EmbeddingModelListerFactory,
		WorkspacePicker:             deps.WorkspacePicker,
		ModelCatalog:                deps.ModelCatalog,
		TTSInstaller:                deps.TTSInstaller,
		STTInstaller:                deps.STTInstaller,
		AcpAgents:                   deps.AcpAgents,
		Acp:                         deps.Acp,
		AcpRunStorage:               deps.AcpRunStorage,
		retrySleeper:                deps.RetrySleeper,
		imageGenSem:                 make(chan struct{}, maxConcurrentImageGens),
		startedAt:                   time.Now().UTC(),
		Logger:                      deps.Logger,
		Automation:                  deps.Automation,
		runs:                        map[string]*TurnRun{},
		turnsSinceReview:            map[string]int{},
		toolCallsSinceReview:        map[string]int{},
		pendingSubagents:            map[string]map[string]bool{},
		learnedParams:               newLearnedParamsCache(deps.LearnedParams),
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
				app.onAcpRunDone(run)
			},
			func(run *domain.AcpRun, req domain.AcpPermissionRequest) {
				app.emitAcpRun(contracts.EventAcpRunUpdated, run)
				perm := contracts.AcpPermissionDTO{
					ID: req.ID, SessionID: req.SessionID, ToolTitle: req.ToolTitle, ToolKind: req.ToolKind,
					Paths: req.Paths, PathCount: len(req.Paths),
				}
				if !req.RequestedAt.IsZero() {
					perm.RequestedAt = req.RequestedAt.Format(timeRFC3339)
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
	// Wire the lifecycle manager (decay + prune). Started by StartLifecycle,
	// stopped by CloseLifecycle.
	if deps.Memory != nil {
		app.lifecycle = NewLifecycleManager(deps.Memory, deps.Skills, DefaultLifecycleConfig())
		app.lifecycle.SetLogger(app.log)
	}
	// Wire the embedding cache + edge builder. The cache persists to
	// learning/embeddings.jsonl and avoids re-embedding on every search.
	// The edge builder pre-computes similarity + token overlap edges as
	// a background job.
	if deps.DataDir != "" {
		if cache, err := jsonstore.NewEmbeddingCache(deps.DataDir); err == nil {
			app.EmbeddingCache = cache
		}
		app.Trajectory = NewTrajectoryRecorder(deps.DataDir)
		// Load persisted turn counters so review thresholds survive restarts.
		app.loadTurnCounters(deps.DataDir)
	}
	if deps.Fragments != nil && deps.Skills != nil {
		app.edgeBuilder = NewEdgeBuilder(
			deps.Fragments, deps.Skills, app.graph(),
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

// SearchSkills implements SkillSearcher for the skill tool (op=search). It ranks
// with BM25 + graph + recency but forces embedding off — per-call embedding
// cost in the agent loop is not justified; the Learning UI keeps the full
// hybrid path.
func (a *App) SearchSkills(ctx context.Context, query string, topK int) ([]SearchResult, error) {
	s := a.learningSearch()
	if s == nil {
		return nil, nil
	}
	opts := defaultSearchOptions()
	opts.DisableEmbedding = true
	return s.SearchSkillsWithOpts(ctx, query, topK, opts)
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

const mcpAutostartTimeout = 20 * time.Second

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
			a.flushLearningReview(ce.ConversationID, "compaction")
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
	// Persist turn counters so review thresholds survive restarts.
	a.saveTurnCounters()
}

// turnCountersPath returns the path to the persisted turn counter file.
func (a *App) turnCountersPath() string {
	if a.DataDir == "" {
		return ""
	}
	return filepath.Join(a.DataDir, "learning", "turns.json")
}

// loadTurnCounters restores per-conversation review counters from disk so
// that the learning review thresholds survive server restarts. Without
// this, a user who restarts frequently never reaches the threshold and
// the review agent never fires. The file stores both turn counters and
// tool-call counters; the legacy flat map[string]int format (turns only)
// is migrated on load.
func (a *App) loadTurnCounters(dataDir string) {
	path := filepath.Join(dataDir, "learning", "turns.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return // file doesn't exist yet — fresh start
	}
	// Try the new struct format first.
	var persisted struct {
		Turns     map[string]int `json:"turns"`
		ToolCalls map[string]int `json:"tool_calls"`
	}
	if err := json.Unmarshal(data, &persisted); err == nil && (persisted.Turns != nil || persisted.ToolCalls != nil) {
		a.learningMu.Lock()
		if persisted.Turns != nil {
			a.turnsSinceReview = persisted.Turns
		}
		if persisted.ToolCalls != nil {
			a.toolCallsSinceReview = persisted.ToolCalls
		}
		a.learningMu.Unlock()
		a.log("info", "learning", "loaded %d turn + %d tool-call counter(s) from disk", len(persisted.Turns), len(persisted.ToolCalls))
		return
	}
	// Legacy flat map[string]int format (turns only).
	var counters map[string]int
	if err := json.Unmarshal(data, &counters); err != nil {
		return
	}
	a.learningMu.Lock()
	a.turnsSinceReview = counters
	a.learningMu.Unlock()
	if len(counters) > 0 {
		a.log("info", "learning", "migrated %d legacy turn counter(s) from disk", len(counters))
	}
}

// saveTurnCounters persists turn and tool-call counters to disk. Called on
// lifecycle shutdown and after each counter update.
func (a *App) saveTurnCounters() {
	path := a.turnCountersPath()
	if path == "" {
		return
	}
	a.learningMu.RLock()
	turns := make(map[string]int, len(a.turnsSinceReview))
	for k, v := range a.turnsSinceReview {
		turns[k] = v
	}
	toolCalls := make(map[string]int, len(a.toolCallsSinceReview))
	for k, v := range a.toolCallsSinceReview {
		toolCalls[k] = v
	}
	a.learningMu.RUnlock()
	if len(turns) == 0 && len(toolCalls) == 0 {
		return
	}
	persisted := struct {
		Turns     map[string]int `json:"turns"`
		ToolCalls map[string]int `json:"tool_calls"`
	}{Turns: turns, ToolCalls: toolCalls}
	data, err := json.Marshal(persisted)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, data, 0o600)
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
	if a.Logs != nil {
		a.Logs.Append(e)
	}
	if a.Bus != nil {
		a.Bus.Emit(contracts.EventLogAppend, contracts.LogAppendEvent{Entry: contracts.LogEntryDTO{
			ID: e.ID, Time: e.Time.Format(timeRFC3339), Level: e.Level, Source: e.Source, Message: e.Message,
		}})
	}
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
		return a.dispatchAcp(method, payload)
	case strings.HasPrefix(method, "plugin."):
		return a.dispatchPlugin(method, payload)
	case strings.HasPrefix(method, "skills."):
		return a.dispatchSkills(method, payload)
	case strings.HasPrefix(method, "memory."):
		return a.dispatchMemory(method, payload)
	case strings.HasPrefix(method, "learning."):
		return a.dispatchLearning(method, payload)
	case strings.HasPrefix(method, "docs."):
		return a.dispatchDocs(method, payload)
	case strings.HasPrefix(method, "settings."):
		return a.dispatchSettings(method, payload)
	case strings.HasPrefix(method, "logs."):
		return a.dispatchLogs(method, payload)
	case strings.HasPrefix(method, "telemetry."):
		return a.dispatchTelemetry(method, payload)
	case strings.HasPrefix(method, "ci."), strings.HasPrefix(method, "automation."):
		return a.handleCI(ctx, method, payload)
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
			Providers:     []string{"messages", "responses", "chat"},
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
				Message: fmt.Sprintf("provider %q is not available or not enabled", a.providerNameByID(providerID)),
			}
		}
		if !p.HasModel(modelID) {
			return nil, nil, "", &contracts.RPCError{
				Code:    contracts.CodeValidation,
				Message: fmt.Sprintf("model %q is not available on provider %q", modelID, p.Name),
			}
		}
		key, has, err := a.Credentials.Get(p.ID)
		if err != nil {
			return nil, nil, "", rpcInternal(err)
		}
		if !has && requiresKey(p.Kind) {
			return nil, nil, "", &contracts.RPCError{Code: contracts.CodeConflict, Message: fmt.Sprintf("provider %q has no API key", p.Name)}
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
	return domain.SplitQualifiedModel(s)
}

func requiresKey(kind domain.ProviderKind) bool {
	return domain.RequiresKey(kind)
}

func rpcInternal(err error) *contracts.RPCError {
	return &contracts.RPCError{Code: contracts.CodeInternal, Message: err.Error()}
}
