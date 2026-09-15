package memory

import (
	"context"

	"nusashell/domain"
)

// RecordStore persists typed memory records (JSONL catalog).
type RecordStore interface {
	List() []*domain.MemoryRecord
	Get(id string) (*domain.MemoryRecord, error)
	Save(e *domain.MemoryRecord) error
	Delete(id string) error
}

// OpStore persists consolidator operations for the audit log.
type OpStore interface {
	List() []*domain.LearningOperation
	Save(op *domain.LearningOperation) error
	Delete(id string) error
}

// DocumentStore is the handler-side view of user.md / soul.md.
// Replace/Path stay on learn.DocumentStore (root MemoryDocumentStore alias).
type DocumentStore interface {
	Load() *domain.MemoryDocument
	Update(entries []domain.DocumentEntry) error
}

// Emitter publishes memory catalog events.
type Emitter interface {
	Emit(typ string, v any)
}

// ChangedHook announces a user/soul document mutation to rooms.
type ChangedHook func(tier, op string)

// DeletedHook runs after a memory record row is removed (learning graph, searcher).
type DeletedHook func(id string)

// MemorySearchResult is a ranked memory hit from the Searcher port.
type MemorySearchResult struct {
	ID    string
	Score float64
}

// Searcher ranks memory records by relevance to a query. Implemented by a
// thin wrapper around LearningSearcher.SearchMemoryWithOpts with embedding
// disabled and graph expansion off (MaxHops 0) — BM25-only ranking for the
// task-memory announcement path. When nil, MaybeAnnounceTaskMemory falls
// back to a local token-overlap heuristic with minTokenLen 3.
type Searcher interface {
	SearchMemory(ctx context.Context, query string, topK int) ([]MemorySearchResult, error)
}

// Deps is the narrow wiring for New. Feature packages never receive *App.
type Deps struct {
	Records         RecordStore
	Ops             OpStore
	User            DocumentStore
	Agent           DocumentStore
	Bus             Emitter
	OnChanged       ChangedHook
	OnRecordDeleted DeletedHook
	Searcher        Searcher
}
