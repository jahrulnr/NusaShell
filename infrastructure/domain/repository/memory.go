package repository

import (
	"context"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// MemoryFilter narrows Memories.List. Empty Scopes means every scope;
// empty Statuses means the live ones (active and stale). A Query ranks
// the result by full-text relevance (best first) and drops what does not
// match; without one the newest change comes first.
type MemoryFilter struct {
	Scopes   []valueobject.MemoryScope
	Types    []enum.MemoryType
	Statuses []enum.MemoryStatus
	Query    string
	Limit    int
}

// MemoryHit is one listed memory; Score is its full-text relevance in
// (0, 1] when the filter had a query, else 0.
type MemoryHit struct {
	Memory *aggregate.Memory
	Score  float64
}

// Memories keeps memories. A name is unique among a scope's live
// memories: saving a second live one under it is a 409. Errors follow the
// apperror contract — a missing memory is a 404.
type Memories interface {
	// Save upserts by id. Changing a memory's text drops its embedding.
	Save(ctx context.Context, m *aggregate.Memory) error
	Get(ctx context.Context, id string) (*aggregate.Memory, error)
	// FindByName answers the live memory of that name in the scope.
	FindByName(ctx context.Context, scope valueobject.MemoryScope, name string) (*aggregate.Memory, error)
	List(ctx context.Context, f MemoryFilter) ([]MemoryHit, error)
	Delete(ctx context.Context, id string) error
	// DeleteScope removes every memory of a scope — its project or thread
	// is gone.
	DeleteScope(ctx context.Context, scope valueobject.MemoryScope) (int64, error)
	// PurgeArchived removes memories forgotten before the time.
	PurgeArchived(ctx context.Context, before time.Time) (int64, error)
	// SetEmbedding stores a memory's vector, made by model from its
	// current text.
	SetEmbedding(ctx context.Context, id, model string, vector []float32) error
	// Embeddings answers the vectors model made for the live memories of
	// the scopes, by memory id; memories without one are absent.
	Embeddings(ctx context.Context, model string, scopes []valueobject.MemoryScope) (map[string][]float32, error)
}

// MemoryProposalKey is what makes two suggestions the same suggestion.
type MemoryProposalKey struct {
	Op    enum.MemoryProposalOp
	Scope valueobject.MemoryScope
	Name  string
}

// MemoryProposals keeps memory suggestions. Errors follow the apperror
// contract — a missing suggestion is a 404.
type MemoryProposals interface {
	// Add stores a new pending suggestion.
	Add(ctx context.Context, p *aggregate.MemoryProposal) error
	Get(ctx context.Context, id string) (*aggregate.MemoryProposal, error)
	// List answers suggestions with any of the statuses, newest first.
	List(ctx context.Context, statuses []enum.MemoryProposalStatus, limit int) ([]*aggregate.MemoryProposal, error)
	CountPending(ctx context.Context) (int, error)
	// Decide stores the decision on a suggestion that is still pending;
	// one decided meanwhile is a 409, so a decision is made once.
	Decide(ctx context.Context, p *aggregate.MemoryProposal) error
	// Seen reports whether the suggestion is pending already, or was
	// decided since the time for the same thread — the owner said no, and
	// the thread should not ask again at once.
	Seen(ctx context.Context, key MemoryProposalKey, convID string, since time.Time) (bool, error)
	// DismissFrom dismisses the pending suggestions read from a thread.
	DismissFrom(ctx context.Context, convID string, now time.Time) (int64, error)
	// DismissIn dismisses the pending suggestions for a scope.
	DismissIn(ctx context.Context, scope valueobject.MemoryScope, now time.Time) (int64, error)
	// PurgeDecided removes suggestions decided before the time.
	PurgeDecided(ctx context.Context, before time.Time) (int64, error)
}
