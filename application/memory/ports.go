package memory

import (
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

// Announcer publishes a harness announcement into one conversation.
type Announcer func(conversationID, typ, args, msg string)

// PersistAnnounced writes LastAnnouncedRecords after a task-memory announcement.
type PersistAnnounced func(conversationID string, ids []string) error

// Deps is the narrow wiring for New. Feature packages never receive *App.
type Deps struct {
	Records          RecordStore
	Ops              OpStore
	User             DocumentStore
	Agent            DocumentStore
	Bus              Emitter
	OnChanged        ChangedHook
	OnRecordDeleted  DeletedHook
	Announce         Announcer
	PersistAnnounced PersistAnnounced
}
