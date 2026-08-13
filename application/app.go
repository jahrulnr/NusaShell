package application

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
)

// App is the application service: it owns the use cases and dispatches RPC
// methods to them. Transport layers only map wire traffic onto Dispatch.
type App struct {
	Version string
	DataDir string

	Conversations ConversationStore
	Providers     ProviderStore
	Credentials   CredentialStore
	Skills        SkillStore
	Memory        MemoryStore
	MCP           MCPServerStore
	Logs          LogStore
	Settings      SettingsStore

	Docs            DocsSource
	Bus             *Bus
	Toolbox         ToolExecutor
	MCPToolbox      MCPToolbox
	Factory         ProviderFactory
	WorkspacePicker WorkspacePicker
	retrySleeper    RetrySleeper

	runsMu sync.Mutex
	runs   map[string]*TurnRun
}

// MCPToolbox gives use cases access to connected MCP servers and their tools.
type MCPToolbox interface {
	ToolsFor(serverID string) ([]contracts.MCPToolDTO, bool)
	Connect(ctx context.Context, s *domain.MCPServer) ([]contracts.MCPToolDTO, error)
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
	Version         string
	DataDir         string
	Conversations   ConversationStore
	Providers       ProviderStore
	Credentials     CredentialStore
	Skills          SkillStore
	Memory          MemoryStore
	MCP             MCPServerStore
	Logs            LogStore
	Settings        SettingsStore
	Docs            DocsSource
	Bus             *Bus
	Toolbox         ToolExecutor
	MCPToolbox      MCPToolbox
	Factory         ProviderFactory
	WorkspacePicker WorkspacePicker
	RetrySleeper    RetrySleeper
}

func NewApp(deps Deps) *App {
	if deps.Bus == nil {
		deps.Bus = NewBus()
	}
	if deps.RetrySleeper == nil {
		deps.RetrySleeper = sleepForRetry
	}
	return &App{
		Version:         deps.Version,
		DataDir:         deps.DataDir,
		Conversations:   deps.Conversations,
		Providers:       deps.Providers,
		Credentials:     deps.Credentials,
		Skills:          deps.Skills,
		Memory:          deps.Memory,
		MCP:             deps.MCP,
		Logs:            deps.Logs,
		Settings:        deps.Settings,
		Docs:            deps.Docs,
		Bus:             deps.Bus,
		Toolbox:         deps.Toolbox,
		MCPToolbox:      deps.MCPToolbox,
		Factory:         deps.Factory,
		WorkspacePicker: deps.WorkspacePicker,
		retrySleeper:    deps.RetrySleeper,
		runs:            map[string]*TurnRun{},
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

// Dispatch routes an RPC method to its use case. Transport handlers are the
// only other caller of this method, which keeps handler-level tests honest.
func (a *App) Dispatch(method string, payload json.RawMessage) (any, *contracts.RPCError) {
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
		return a.handleTurnsStart(req)
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
		return a.handleTurnsRetry(req)
	case contracts.MethodTurnsSteer:
		var req contracts.TurnSteerRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleTurnsSteer(req)
	case contracts.MethodTurnsCancelSteer:
		var req contracts.TurnCancelSteerRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleTurnsCancelSteer(req)
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
	case contracts.MethodMCPServersList:
		return a.handleMCPServersList()
	case contracts.MethodMCPServersSave:
		var req contracts.MCPSaveRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleMCPServersSave(req)
	case contracts.MethodMCPServersDelete:
		var req contracts.MCPIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleMCPServersDelete(req)
	case contracts.MethodMCPServersTest:
		var req contracts.MCPIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleMCPServersTest(req)
	case contracts.MethodMCPToolsList:
		return a.handleMCPToolsList()
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
			Providers:     []string{"messages", "responses", "chat"},
		},
	}, nil
}

// resolveModel finds the provider owning a model id and its API key.
func (a *App) resolveModel(model string) (*domain.Provider, string, *contracts.RPCError) {
	for _, p := range a.Providers.List() {
		if !p.Enabled || !p.HasModel(model) {
			continue
		}
		key, has, err := a.Credentials.Get(p.ID)
		if err != nil {
			return nil, "", rpcInternal(err)
		}
		if !has && requiresKey(p.Kind) {
			return nil, "", &contracts.RPCError{Code: contracts.CodeConflict, Message: fmt.Sprintf("provider %q has no API key", p.Name)}
		}
		return p, key, nil
	}
	return nil, "", &contracts.RPCError{
		Code:    contracts.CodeValidation,
		Message: fmt.Sprintf("model %q is not available on any enabled provider", model),
	}
}

func requiresKey(kind domain.ProviderKind) bool {
	// local Chat endpoints (Ollama, LM Studio, …) work without a key
	return kind == domain.ProviderMessages || kind == domain.ProviderResponses
}

func rpcInternal(err error) *contracts.RPCError {
	return &contracts.RPCError{Code: contracts.CodeInternal, Message: err.Error()}
}
