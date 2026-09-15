package application

import (
	"context"
	"strings"
	"testing"
	"time"

	"nusashell/application/learn"
	"nusashell/application/memory"
	"nusashell/domain"
)

// laneTestEmbedder returns a fixed vector for all texts so every doc has
// cosine similarity 1.0 with the query — the semantic channel always finds
// all docs. This isolates the test from embedding quality.
type laneTestEmbedder struct{}

func (e *laneTestEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return []float32{1, 0}, nil
}
func (e *laneTestEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = []float32{1, 0}
	}
	return out, nil
}
func (e *laneTestEmbedder) Dim() int { return 2 }

func newLaneSearcher(t *testing.T, records memory.RecordStore, embed learn.Embedder) *LearningSearcher {
	t.Helper()
	return newRootLearningSearcher(nil, records, embed, nil)
}

func laneFilter(records memory.RecordStore) func(*domain.Conversation, []memory.MemorySearchResult, func() time.Time) []memory.TaskMemoryHit {
	svc := memory.New(memory.Deps{Records: records})
	return svc.FilterTaskMemoryResults
}

// TestLanePublishesWhenEmbedderPresentAndBM25Empty proves the async lane
// publishes a task_memory announcement when the embedder is available and
// BM25 finds nothing (fallback path).
func TestLanePublishesWhenEmbedderPresentAndBM25Empty(t *testing.T) {
	now := time.Now()
	rec := &domain.MemoryRecord{
		ID:            "rec-sem",
		Type:          domain.MemoryTypeFact,
		Status:        domain.MemoryStatusLearned,
		Body:          "openclaw compatible server setup",
		LastConfirmed: now,
	}
	records := &fakeMemoryRecordStore{items: []*domain.MemoryRecord{rec}}
	embed := &laneTestEmbedder{}
	searcher := newLaneSearcher(t, records, embed)
	conv := &domain.Conversation{
		ID:    "c1",
		Title: "gateway configuration",
		Type:  domain.ConversationTypeConversation,
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "how do I set up the gateway"},
		},
	}
	var published bool
	var pubArgs string
	d := taskMemoryLaneDeps{
		searcher:     searcher,
		resolveEmbed: func() (Embedder, string) { return embed, "test-model" },
		filter:       laneFilter(records),
		breaker:      newTaskMemoryBreaker(),
		publish: func(convID, args, message string, markers []domain.AnnouncedRecord) {
			published = true
			pubArgs = args
		},
		now: func() time.Time { return now },
	}
	runTaskMemorySemanticLane(context.Background(), conv, d)
	if !published {
		t.Fatal("must publish when embedder present and BM25 finds nothing")
	}
	if !strings.Contains(pubArgs, "rec-sem") {
		t.Fatalf("published args must contain rec-sem: %s", pubArgs)
	}
}

// TestLaneSkipsWhenEmbedderNil proves the async lane does NOT publish when
// no embedder is configured (BM25 turn-start is enough).
func TestLaneSkipsWhenEmbedderNil(t *testing.T) {
	now := time.Now()
	rec := &domain.MemoryRecord{
		ID:            "rec-sem",
		Type:          domain.MemoryTypeFact,
		Status:        domain.MemoryStatusLearned,
		Body:          "openclaw compatible server setup",
		LastConfirmed: now,
	}
	records := &fakeMemoryRecordStore{items: []*domain.MemoryRecord{rec}}
	embed := &laneTestEmbedder{}
	searcher := newLaneSearcher(t, records, embed)
	conv := &domain.Conversation{
		ID:    "c1",
		Title: "gateway configuration",
		Type:  domain.ConversationTypeConversation,
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "how do I set up the gateway"},
		},
	}
	var published bool
	d := taskMemoryLaneDeps{
		searcher:     searcher,
		resolveEmbed: func() (Embedder, string) { return nil, "" },
		filter:       laneFilter(records),
		breaker:      newTaskMemoryBreaker(),
		publish:      func(convID, args, message string, markers []domain.AnnouncedRecord) { published = true },
		now:          func() time.Time { return now },
	}
	runTaskMemorySemanticLane(context.Background(), conv, d)
	if published {
		t.Fatal("must NOT publish when embedder is nil")
	}
}

// TestLaneSkipsWhenNoIntentAndBM25HasHits proves the async lane skips when
// the user has no recall intent and BM25 already found hits (fallback hemat).
func TestLaneSkipsWhenNoIntentAndBM25HasHits(t *testing.T) {
	now := time.Now()
	rec := &domain.MemoryRecord{
		ID:            "rec-bm25",
		Type:          domain.MemoryTypeFact,
		Status:        domain.MemoryStatusLearned,
		Body:          "gateway server configuration guide",
		LastConfirmed: now,
	}
	records := &fakeMemoryRecordStore{items: []*domain.MemoryRecord{rec}}
	embed := &laneTestEmbedder{}
	searcher := newLaneSearcher(t, records, embed)
	conv := &domain.Conversation{
		ID:    "c1",
		Title: "gateway server",
		Type:  domain.ConversationTypeConversation,
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "how to configure the gateway server"},
		},
	}
	var published bool
	d := taskMemoryLaneDeps{
		searcher:     searcher,
		resolveEmbed: func() (Embedder, string) { return embed, "test-model" },
		filter:       laneFilter(records),
		breaker:      newTaskMemoryBreaker(),
		publish:      func(convID, args, message string, markers []domain.AnnouncedRecord) { published = true },
		now:          func() time.Time { return now },
	}
	runTaskMemorySemanticLane(context.Background(), conv, d)
	if published {
		t.Fatal("must NOT publish when no recall intent and BM25 already found hits")
	}
}

// TestLanePublishesWhenIntentPresentAndBM25HasHits proves the async lane
// runs a deeper semantic search when the user shows recall intent, even
// though BM25 already found hits.
func TestLanePublishesWhenIntentPresentAndBM25HasHits(t *testing.T) {
	now := time.Now()
	rec := &domain.MemoryRecord{
		ID:            "rec-deep",
		Type:          domain.MemoryTypeFact,
		Status:        domain.MemoryStatusLearned,
		Body:          "gateway server configuration guide",
		LastConfirmed: now,
	}
	records := &fakeMemoryRecordStore{items: []*domain.MemoryRecord{rec}}
	embed := &laneTestEmbedder{}
	searcher := newLaneSearcher(t, records, embed)
	conv := &domain.Conversation{
		ID:    "c1",
		Title: "gateway server",
		Type:  domain.ConversationTypeConversation,
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "do you remember the gateway server config we discussed"},
		},
	}
	var published bool
	d := taskMemoryLaneDeps{
		searcher:     searcher,
		resolveEmbed: func() (Embedder, string) { return embed, "test-model" },
		filter:       laneFilter(records),
		breaker:      newTaskMemoryBreaker(),
		publish:      func(convID, args, message string, markers []domain.AnnouncedRecord) { published = true },
		now:          func() time.Time { return now },
	}
	runTaskMemorySemanticLane(context.Background(), conv, d)
	if !published {
		t.Fatal("must publish when recall intent present even if BM25 found hits")
	}
}

// TestLaneSkipsWhenDedupMarkerCoversResult proves the async lane does NOT
// publish when the only candidate is already in LastAnnouncedRecords (not
// re-confirmed since).
func TestLaneSkipsWhenDedupMarkerCoversResult(t *testing.T) {
	now := time.Now()
	rec := &domain.MemoryRecord{
		ID:            "rec-dup",
		Type:          domain.MemoryTypeFact,
		Status:        domain.MemoryStatusLearned,
		Body:          "openclaw compatible server setup",
		LastConfirmed: now,
	}
	records := &fakeMemoryRecordStore{items: []*domain.MemoryRecord{rec}}
	embed := &laneTestEmbedder{}
	searcher := newLaneSearcher(t, records, embed)
	conv := &domain.Conversation{
		ID:    "c1",
		Title: "gateway configuration",
		Type:  domain.ConversationTypeConversation,
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "how do I set up the gateway"},
		},
		LastAnnouncedRecords: domain.AnnouncedRecords{
			{ID: "rec-dup", LastConfirmedAt: now},
		},
	}
	var published bool
	d := taskMemoryLaneDeps{
		searcher:     searcher,
		resolveEmbed: func() (Embedder, string) { return embed, "test-model" },
		filter:       laneFilter(records),
		breaker:      newTaskMemoryBreaker(),
		publish:      func(convID, args, message string, markers []domain.AnnouncedRecord) { published = true },
		now:          func() time.Time { return now },
	}
	runTaskMemorySemanticLane(context.Background(), conv, d)
	if published {
		t.Fatal("must NOT publish when the only result is already announced (dedup)")
	}
}

// TestLaneSkipsWhenBreakerTripped proves the async lane does NOT run when
// the breaker has tripped after N consecutive failures.
func TestLaneSkipsWhenBreakerTripped(t *testing.T) {
	now := time.Now()
	rec := &domain.MemoryRecord{
		ID:            "rec-sem",
		Type:          domain.MemoryTypeFact,
		Status:        domain.MemoryStatusLearned,
		Body:          "openclaw compatible server setup",
		LastConfirmed: now,
	}
	records := &fakeMemoryRecordStore{items: []*domain.MemoryRecord{rec}}
	embed := &laneTestEmbedder{}
	searcher := newLaneSearcher(t, records, embed)
	conv := &domain.Conversation{
		ID:    "c1",
		Title: "gateway configuration",
		Type:  domain.ConversationTypeConversation,
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "how do I set up the gateway"},
		},
	}
	breaker := newTaskMemoryBreaker()
	breaker.RecordFailure()
	breaker.RecordFailure()
	breaker.RecordFailure()
	if !breaker.Tripped() {
		t.Fatal("breaker must be tripped")
	}
	var published bool
	d := taskMemoryLaneDeps{
		searcher:     searcher,
		resolveEmbed: func() (Embedder, string) { return embed, "test-model" },
		filter:       laneFilter(records),
		breaker:      breaker,
		publish:      func(convID, args, message string, markers []domain.AnnouncedRecord) { published = true },
		now:          func() time.Time { return now },
	}
	runTaskMemorySemanticLane(context.Background(), conv, d)
	if published {
		t.Fatal("must NOT publish when breaker is tripped")
	}
}
