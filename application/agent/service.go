package agent

import (
	"sync"
	"time"

	"nusashell/application/provider"
	"nusashell/application/service/learnedparams"
	"nusashell/application/service/modeloverrides"
	"nusashell/application/tools"
)

// Service owns the agent turn engine: in-flight TurnRuns, round streams,
// ask-question pause, announcements, and the round loop. App holds a cached
// instance so the registry is shared with RPC and background completion.
type Service struct {
	deps Deps

	Conversations    ConversationStore
	Providers        ProviderStore
	Credentials      CredentialStore
	Settings         SettingsStore
	Factory          provider.Factory
	Toolbox          tools.ToolExecutor
	Attachments      AttachmentStore
	Todos            TodoPort
	User             DocumentPath
	Agent            DocumentPath
	ProjectMemory    ProjectMemoryStore
	MemoryRecords    MemoryRecordList
	Bus              Emitter
	AskQuestions     *AskQuestionService
	RoundStreams     *RoundStreamRegistry
	learnedParams    *learnedparams.Cache
	modelOverrides   *modeloverrides.Cache
	DataDir          string
	defaultWorkspace string
	startedAt        time.Time

	runsMu              sync.Mutex
	runs                map[string]*TurnRun
	startMu             sync.Mutex
	conversationTurnsMu sync.Mutex
	conversationTurns   map[string]*sync.Mutex
	pendingRunsMu       sync.Mutex
	pendingRuns         map[string]map[string]string
	announcementLocksMu sync.Mutex
	announcementLocks   map[string]*sync.Mutex
}

// New builds an agent Service from Deps. Runs / RoundStreams / AskQuestions
// are shared with App when those pointers are supplied.
func New(d Deps) *Service {
	goFn := d.Go
	if goFn == nil {
		goFn = func(_ string, fn func()) { go fn() }
	}
	d.Go = goFn
	runs := d.Runs
	if runs == nil {
		runs = map[string]*TurnRun{}
	}
	pending := d.PendingRuns
	if pending == nil {
		pending = map[string]map[string]string{}
	}
	s := &Service{
		deps:              d,
		Conversations:     d.Conversations,
		Providers:         d.Providers,
		Credentials:       d.Credentials,
		Settings:          d.Settings,
		Factory:           d.Factory,
		Toolbox:           d.Toolbox,
		Attachments:       d.Attachments,
		Todos:             d.Todos,
		User:              d.User,
		Agent:             d.Agent,
		ProjectMemory:     d.ProjectMemory,
		MemoryRecords:     d.MemoryRecords,
		Bus:               d.Bus,
		AskQuestions:      d.AskQuestions,
		RoundStreams:      d.RoundStreams,
		learnedParams:     d.LearnedParams,
		modelOverrides:    d.ModelOverrides,
		DataDir:           d.DataDir,
		defaultWorkspace:  d.DefaultWorkspace,
		startedAt:         d.StartedAt,
		runs:              runs,
		conversationTurns: map[string]*sync.Mutex{},
		pendingRuns:       pending,
		announcementLocks: map[string]*sync.Mutex{},
	}
	return s
}

func (a *Service) log(level, source, format string, args ...any) {
	defer func() { _ = recover() }()
	if a != nil && a.deps.Log != nil {
		a.deps.Log(level, source, format, args...)
	}
}

func (a *Service) emitBus(typ string, payload any) {
	if a == nil || a.Bus == nil {
		return
	}
	defer func() { _ = recover() }()
	a.Bus.Emit(typ, payload)
}

func (a *Service) goSafe(name string, fn func()) {
	if a == nil || a.deps.Go == nil {
		go func() {
			defer func() { _ = recover() }()
			fn()
		}()
		return
	}
	a.deps.Go(name, fn)
}
