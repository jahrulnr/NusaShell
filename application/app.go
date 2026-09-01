package application

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"nusashell/application/service/learnedparams"
	"nusashell/application/service/modeloverrides"
	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/infrastructure/ai/core"
	"nusashell/infrastructure/jsonstore"
	clock "nusashell/pkg/time"
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
	Journal                     ChangeJournal
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
	learnedParams *learnedparams.Cache
	// modelOverrides mirrors the persisted manual model-override registry
	// in memory. Applied at resolve time AFTER learned 400-adaptations so
	// manual corrections always win. Initialized once at App construction
	// from the ModelOverrides store.
	modelOverrides  *modeloverrides.Cache
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
	// journalRootsMu guards lazy creation of per-workspace-root mutexes used
	// to serialize mutating tool calls against the same root.
	journalRootsMu sync.Mutex
	journalRoots   map[string]*sync.Mutex

	// edgeBuilder pre-computes deterministic/semantic learning edges;
	// used_with edges are recorded by successful turn tool usage.
	// as a background job. Nil if not configured.
	edgeBuilder *EdgeBuilder
	// Trajectory records learning layer events to a JSONL log for
	// debugging and observability. Best-effort — nil = no-op.
	Trajectory *TrajectoryRecorder

	Automation *Automation

	runsMu              sync.Mutex
	runs                map[string]*TurnRun
	startMu             sync.Mutex
	conversationTurnsMu sync.Mutex
	conversationTurns   map[string]*sync.Mutex

	// pendingRuns tracks active (not-yet-completed) background run IDs
	// per conversation — the shared push-completion registry. Today the
	// only producer is ACP subagents; future async tools (delegated
	// agents, long-running jobs) queue their completion here too. Used to
	// implement HasBackgroundJobs: while any run is pending, the parent
	// agent's auto-continue chain pauses with reason
	// "awaiting-background-jobs" instead of ending the turn. When a run
	// completes, its result is injected at the next steer-style turn
	// boundary (or a new turn if the parent is idle) and then removed
	// from this map.
	pendingRunsMu sync.Mutex
	pendingRuns   map[string]map[string]bool // conversationID → set of runIDs

	// rlMu guards per-provider rate-limit windows (see rate_limit.go).
	// MarkProviderRateLimited records when a 429 window clears so client
	// requests can be gated and messages can be user-friendly.
	rlMu      sync.Mutex
	rlWindows map[string]time.Time // providerID → next allowed request time

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
type ProviderFactory func(ctx context.Context, p *domain.Provider, apiKey string) (core.Provider, error)

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
	Journal                     ChangeJournal
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
		Journal:                     deps.Journal,
		MCPToolbox:                  deps.MCPToolbox,
		journalRoots:                map[string]*sync.Mutex{},
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
		startedAt:                   clock.NewTime().Time(),
		Logger:                      deps.Logger,
		Automation:                  deps.Automation,
		runs:                        map[string]*TurnRun{},
		turnsSinceReview:            map[string]int{},
		toolCallsSinceReview:        map[string]int{},
		pendingRuns:                 map[string]map[string]bool{},
		learnedParams:               learnedparams.New(deps.LearnedParams),
		modelOverrides:              modeloverrides.New(deps.ModelOverrides),
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
	// Wire the lifecycle manager (decay + prune). Started by StartLifecycle,
	// stopped by CloseLifecycle.
	if deps.Memory != nil {
		app.lifecycle = NewLifecycleManager(deps.Memory, deps.Skills, domain.DefaultLifecycleConfig())
		app.lifecycle.SetLogger(app.log)
	}
	// Wire the embedding cache + edge builder. The cache persists to
	// learning/embeddings.jsonl and avoids re-embedding on every search.
	// The edge builder pre-computes similarity, metadata, and token-overlap
	// edges as a background job; successful turns record used_with edges.
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
		app.edgeBuilder.SetPrimaryStore(deps.Primary)
	}
	return app
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
		key, has, err := a.Credentials.Get(p.ID)
		if err != nil {
			return nil, nil, "", rpcInternal(err)
		}
		if !has && domain.RequiresKey(p.Kind) {
			return nil, nil, "", &contracts.RPCError{Code: contracts.CodeConflict, Message: fmt.Sprintf("provider %q has no API key", p.Name)}
		}
		m := p.FindModel(modelID)
		a.applyModelOverrides(p, m)
		return p, m, key, nil
	}
	for _, p := range a.Providers.List() {
		if !p.Enabled || !p.HasModel(model) {
			continue
		}
		key, has, err := a.Credentials.Get(p.ID)
		if err != nil {
			return nil, nil, "", rpcInternal(err)
		}
		if !has && domain.RequiresKey(p.Kind) {
			return nil, nil, "", &contracts.RPCError{Code: contracts.CodeConflict, Message: fmt.Sprintf("provider %q has no API key", p.Name)}
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
