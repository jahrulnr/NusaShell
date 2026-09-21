package application

import (
	"context"
	"time"

	"nusashell/application/memory"
	"nusashell/contracts"
	"nusashell/domain"
	clock "nusashell/pkg/time"
)

var clockNow = func() time.Time {
	return clock.NewTime().Time()
}

func (a *App) emitMemoryUpdated() {
	if a.Bus != nil {
		a.Bus.Emit(contracts.EventMemoryUpdated, map[string]any{"source": "rpc"})
	}
}

func (a *App) maybeAnnounceTaskMemory(conversation *domain.Conversation) {
	a.memoryService().MaybeAnnounceTaskMemory(conversation, clockNow)
}

// taskMemorySearcher adapts LearningSearcher.SearchMemoryWithOpts to the
// memory.Searcher port. It forces embedding off and graph expansion off
// (MaxHops 0) — BM25-only ranking for the task-memory announcement path.
// The LearningSearcher is resolved lazily so the wiring works regardless
// of init order between memorySvc and learnSvc.
type taskMemorySearcher struct{ resolve func() *LearningSearcher }

func (t taskMemorySearcher) SearchMemory(ctx context.Context, query string, topK int) ([]memory.MemorySearchResult, error) {
	if t.resolve == nil {
		return nil, nil
	}
	inner := t.resolve()
	if inner == nil {
		return nil, nil
	}
	opts := SearchOptions{DisableEmbedding: true, MaxHops: 0}
	results, err := inner.SearchMemoryWithOpts(ctx, query, topK, opts)
	if err != nil {
		return nil, err
	}
	out := make([]memory.MemorySearchResult, len(results))
	for i, r := range results {
		out[i] = memory.MemorySearchResult{ID: r.ID, Score: r.Score}
	}
	return out, nil
}
