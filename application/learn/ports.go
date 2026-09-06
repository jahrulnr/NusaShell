package learn

import (
	"context"

	"nusashell/domain"
)

// ExperienceStore persists extracted conversation episodes.
type ExperienceStore interface {
	List() []*domain.Experience
	Get(id string) (*domain.Experience, error)
	Save(e *domain.Experience) error
	ListByConversation(conversationID string) []*domain.Experience
	Delete(id string) error
}

// JobStore persists background learner jobs.
type JobStore interface {
	List() []*domain.LearningJob
	Get(id string) (*domain.LearningJob, error)
	Save(j *domain.LearningJob) error
	Delete(id string) error
}

// EdgeStore persists bitemporal edges between learning nodes.
type EdgeStore interface {
	List() []*domain.LearningEdge
	Save(e *domain.LearningEdge) error
	Delete(id string) error
}

// LearnedParamStore persists the dynamic 400-learning registry.
type LearnedParamStore interface {
	Load() *domain.LearnedParamRegistry
	Save(r *domain.LearnedParamRegistry) error
}

// RecordStore persists typed memory records. Identical to memory.RecordStore
// so App.MemoryRecords assigns; learn does not import application/memory.
type RecordStore interface {
	List() []*domain.MemoryRecord
	Get(id string) (*domain.MemoryRecord, error)
	Save(e *domain.MemoryRecord) error
	Delete(id string) error
}

// DocumentStore is the full user.md / soul.md contract (Load/Update/Replace/Path).
// Root aliases this as MemoryDocumentStore so jsonstore adapters still compile.
type DocumentStore interface {
	Load() *domain.MemoryDocument
	Update(entries []domain.DocumentEntry) error
	Replace(oldText, content string) error
	Path() string
}

// SkillCatalog is the subset of the skill store the learner needs.
// Root SkillStore stays in ports.go; learn does not own it.
type SkillCatalog interface {
	List() []*domain.Skill
	Get(id, ownedBy string) (*domain.Skill, error)
	Save(s *domain.Skill) error
}

// ConversationStore is the subset of the conversation catalog learn reads
// and deletes (transcripts, log titles, source handoff). Cursor writes go
// through PersistConversation so learn does not import application/conversation.
type ConversationStore interface {
	List() []*domain.Conversation
	Get(id string) (*domain.Conversation, error)
	Delete(id string) error
}

// ProviderStore lists providers for embedder resolution.
type ProviderStore interface {
	List() []*domain.Provider
}

// CredentialStore reads API keys for embedder construction.
type CredentialStore interface {
	Get(providerID string) (string, bool, error)
}

// Settings reads review-model and embedding configuration.
type Settings interface {
	Get() domain.Settings
}

// Embedder produces embedding vectors for hybrid search and edge building.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
	Dim() int
}

// EmbedderFactory builds an Embedder for a configured provider.
type EmbedderFactory func(p *domain.Provider, apiKey string) (Embedder, error)

// EmbeddingCache is the consumer-side port for cached embedding vectors.
// *jsonstore.EmbeddingCache already has these methods.
type EmbeddingCache interface {
	GetBatch(modelID string, texts []string) (vectors [][]float32, missIdx []int)
	Put(modelID, text string, vector []float32) error
}

// KeywordDoc is one BM25 document. The factory lives on the wiring side
// so learn does not import infrastructure/jsonstore.
type KeywordDoc struct {
	ID   string
	Text string
}

// KeywordIndex ranks documents for a query.
type KeywordIndex interface {
	Search(query string, topK int) []SearchResult
}

// NewKeywordIndex builds a KeywordIndex over docs. App wraps jsonstore.NewBM25.
type NewKeywordIndex func([]KeywordDoc) KeywordIndex

// ApplyMemory applies one consolidator operation. App wires
// memory.NewMemoryService(records, ops).Apply so learn does not import
// application/memory.
type ApplyMemory func(op *domain.LearningOperation) error

// RejectMemory records a durability-gate rejection. App wires
// memory.Service.Reject.
type RejectMemory func(op *domain.LearningOperation, reason string)

// HeadlessTurnRunner runs one learner LLM turn. Duplicated from
// application/automation so learn does not import that package.
// App implements both: automation uses AgentAutomation; learn wiring
// uses AgentLearner via runHeadlessTurnKind.
type HeadlessTurnRunner interface {
	RunHeadlessTurn(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any) (map[string]any, string, error)
}

// LearningTurn is the test seam that replaces a real headless turn.
type LearningTurn func(ctx context.Context, model, prompt string) (string, string, error)

// Emitter publishes learning and experience events. Root *Bus assigns.
type Emitter interface {
	Emit(typ string, v any)
}

// Logger records a structured application log line.
type Logger func(level, source, format string, args ...any)

// GoFunc runs fire-and-forget work. App wires this to goSafe.
type GoFunc func(name string, fn func())

// SkillChangedHook notifies App so it can emit bus/announcement/trajectory
// events without learn importing skills or the root package.
type SkillChangedHook func(op, id, status, conversationID string)

// PersistConversation writes conversation metadata (cursor) through the
// append-only repository. App wires conversation.Bind(...).Save.
type PersistConversation func(c *domain.Conversation) error

// LockConversation serializes cursor updates against the turn loop.
type LockConversation func(id string) func()

// ConversationPath resolves a persisted conversation JSON path.
type ConversationPath func(id string) string

// WithWorkspace annotates a context with the experience workspace.
type WithWorkspace func(ctx context.Context, workspace string) context.Context

// MemoryUpdatedHook notifies App after a job mutates memory records.
type MemoryUpdatedHook func()

// Deps is the narrow wiring for New. Feature packages never receive *App.
type Deps struct {
	Experiences         ExperienceStore
	Records             RecordStore
	Jobs                JobStore
	Edges               EdgeStore
	Skills              SkillCatalog
	User                DocumentStore
	Conversations       ConversationStore
	Providers           ProviderStore
	Credentials         CredentialStore
	Settings            Settings
	EmbedderFactory     EmbedderFactory
	EmbeddingCache      EmbeddingCache
	NewKeywordIndex     NewKeywordIndex
	ApplyMemory         ApplyMemory
	RejectMemory        RejectMemory
	Headless            HeadlessTurnRunner
	LearningTurn        LearningTurn
	Bus                 Emitter
	Log                 Logger
	Go                  GoFunc
	DataDir             string
	Trajectory          *TrajectoryRecorder
	OnSkillChanged      SkillChangedHook
	OnMemoryUpdated     MemoryUpdatedHook
	PersistConversation PersistConversation
	LockConversation    LockConversation
	ConversationPath    ConversationPath
	WithWorkspace       WithWorkspace
}
