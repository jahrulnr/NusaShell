package application

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"nusashell/application/agent"
	"nusashell/application/learn"
	"nusashell/application/memory"
	"nusashell/domain"
)

// fakeMemorySearcher is a test double for memory.Searcher used by the
// turn-start scan (MaybeAnnounceTaskMemory). It returns a fixed list of
// result IDs, ignoring the query.
type fakeMemorySearcher struct {
	ids []string
}

func (f *fakeMemorySearcher) SearchMemory(_ context.Context, _ string, topK int) ([]memory.MemorySearchResult, error) {
	if topK <= 0 || topK > len(f.ids) {
		topK = len(f.ids)
	}
	out := make([]memory.MemorySearchResult, topK)
	for i := 0; i < topK; i++ {
		out[i] = memory.MemorySearchResult{ID: f.ids[i], Score: float64(len(f.ids) - i)}
	}
	return out, nil
}

// markerConvStore is a thread-safe, cloning conversation store for the
// marker tests. Get returns a deep copy (messages + slices copied) so the
// lane snapshot and the repo load see independent state; Save stores a
// deep copy so concurrent operations under the announcementLock never
// alias the same slice headers.
type markerConvStore struct {
	mu   sync.Mutex
	conv *domain.Conversation
}

func (s *markerConvStore) List() []*domain.Conversation {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conv == nil {
		return nil
	}
	return []*domain.Conversation{s.conv}
}

func (s *markerConvStore) Get(id string) (*domain.Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conv == nil || s.conv.ID != id {
		return nil, errNotFound
	}
	return cloneConvForMarker(s.conv), nil
}

func (s *markerConvStore) Save(c *domain.Conversation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conv = cloneConvForMarker(c)
	return nil
}

func (s *markerConvStore) Delete(id string) error { return nil }
func (s *markerConvStore) ArchiveChunk(id string, messages []domain.Message) (int, error) {
	return 0, nil
}
func (s *markerConvStore) GetChunk(id string, index int) ([]domain.Message, error) {
	return nil, errNotFound
}

// cloneConvForMarker deep-copies the slices that the announcement path
// mutates (Messages, PendingAnnouncements, LastAnnouncedRecords) so the
// store never aliases the caller's slice headers.
func cloneConvForMarker(c *domain.Conversation) *domain.Conversation {
	cp := *c
	cp.Messages = append([]domain.Message(nil), c.Messages...)
	cp.PendingAnnouncements = append([]domain.PendingAnnouncement(nil), c.PendingAnnouncements...)
	cp.LastAnnouncedRecords = append([]domain.AnnouncedRecord(nil), c.LastAnnouncedRecords...)
	return &cp
}

// TestTaskMemoryLanePublishWritesMarkerToRepo proves the async semantic
// lane, when it publishes a task_memory announcement, also writes dedup
// markers (LastAnnouncedRecords) to the conversation persisted in the
// store — not just the lane's in-memory snapshot. The markers are read
// back from a freshly loaded conversation, proving they survive the
// load-modify-save cycle under the announcementLock.
func TestTaskMemoryLanePublishWritesMarkerToRepo(t *testing.T) {
	now := time.Now()
	rec := &domain.MemoryRecord{
		ID:            "rec-marker",
		Type:          domain.MemoryTypeFact,
		Status:        domain.MemoryStatusLearned,
		Body:          "openclaw compatible server setup guide",
		LastConfirmed: now,
	}
	records := &fakeMemoryRecordStore{items: []*domain.MemoryRecord{rec}}
	embed := &laneTestEmbedder{}
	searcher := newLaneSearcher(t, records, embed)

	conv := &domain.Conversation{
		ID:    "c-marker",
		Title: "gateway configuration",
		Type:  domain.ConversationTypeConversation,
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "how do I set up the gateway"},
		},
	}
	store := &markerConvStore{conv: cloneConvForMarker(conv)}
	svc := agent.New(agent.Deps{Conversations: store})

	d := taskMemoryLaneDeps{
		searcher:     searcher,
		resolveEmbed: func() (Embedder, string) { return embed, "test-model" },
		filter:       laneFilter(records),
		breaker:      newTaskMemoryBreaker(),
		records:      records,
		publish: func(convID, args, message string, markers []domain.AnnouncedRecord) {
			svc.PublishTaskMemoryAnnouncement(convID, args, message, markers)
		},
		now: func() time.Time { return now },
	}
	runTaskMemorySemanticLane(context.Background(), cloneConvForMarker(conv), d)

	// Reload from store — NOT the lane's snapshot.
	got, err := store.Get("c-marker")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.PendingAnnouncements) != 1 {
		t.Fatalf("pending = %+v, want 1 task_memory announcement", got.PendingAnnouncements)
	}
	pa := got.PendingAnnouncements[0]
	if pa.Type != memory.TaskMemoryAnnounceType {
		t.Fatalf("type = %q, want %q", pa.Type, memory.TaskMemoryAnnounceType)
	}
	if !strings.Contains(pa.Args, "rec-marker") {
		t.Fatalf("args missing rec-marker: %s", pa.Args)
	}
	if len(got.LastAnnouncedRecords) != 1 || got.LastAnnouncedRecords[0].ID != "rec-marker" {
		t.Fatalf("dedup marker = %+v, want rec-marker", got.LastAnnouncedRecords)
	}
	if !got.LastAnnouncedRecords[0].LastConfirmedAt.Equal(now) {
		t.Fatalf("marker LastConfirmedAt = %v, want %v", got.LastAnnouncedRecords[0].LastConfirmedAt, now)
	}
}

// TestTaskMemoryLanePublishPreventsReannounceByTurnStartScan proves that
// after the async semantic lane publishes and writes markers, the
// turn-start scan (MaybeAnnounceTaskMemory) on the next turn does NOT
// re-announce the same record — the marker covers it. The conversation is
// reloaded from the store (not the lane snapshot) to prove the marker
// persists.
func TestTaskMemoryLanePublishPreventsReannounceByTurnStartScan(t *testing.T) {
	now := time.Now()
	rec := &domain.MemoryRecord{
		ID:            "rec-no-reannounce",
		Type:          domain.MemoryTypeFact,
		Status:        domain.MemoryStatusLearned,
		Body:          "gateway server configuration guide for openclaw",
		LastConfirmed: now,
	}
	records := &fakeMemoryRecordStore{items: []*domain.MemoryRecord{rec}}
	embed := &laneTestEmbedder{}
	searcher := newLaneSearcher(t, records, embed)

	conv := &domain.Conversation{
		ID:    "c-no-reannounce",
		Title: "gateway server",
		Type:  domain.ConversationTypeConversation,
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "how do I configure the gateway server"},
		},
	}
	store := &markerConvStore{conv: cloneConvForMarker(conv)}
	svc := agent.New(agent.Deps{Conversations: store})

	// Lane publishes and writes markers.
	d := taskMemoryLaneDeps{
		searcher:     searcher,
		resolveEmbed: func() (Embedder, string) { return embed, "test-model" },
		filter:       laneFilter(records),
		breaker:      newTaskMemoryBreaker(),
		records:      records,
		publish: func(convID, args, message string, markers []domain.AnnouncedRecord) {
			svc.PublishTaskMemoryAnnouncement(convID, args, message, markers)
		},
		now: func() time.Time { return now },
	}
	runTaskMemorySemanticLane(context.Background(), cloneConvForMarker(conv), d)

	// Reload from store and drain the pending announcement the lane queued.
	loaded, _ := store.Get("c-no-reannounce")
	loaded.DrainPendingAnnouncements()
	store.Save(loaded)

	// Turn-start scan with a searcher fake that returns the same record.
	memSvc := memory.New(memory.Deps{
		Records:  records,
		Searcher: &fakeMemorySearcher{ids: []string{"rec-no-reannounce"}},
	})
	again, _ := store.Get("c-no-reannounce")
	memSvc.MaybeAnnounceTaskMemory(again, func() time.Time { return now })

	final, _ := store.Get("c-no-reannounce")
	if len(final.PendingAnnouncements) != 0 {
		t.Fatalf("turn-start scan must NOT re-announce after lane wrote marker, pending = %+v", final.PendingAnnouncements)
	}
}

// TestTaskMemoryLanePublishConcurrentWithDrainNoLostUpdate proves that
// concurrent PublishTaskMemoryAnnouncement (lane) and DrainAnnouncements
// (round boundary) under the same announcementLock do not lose markers
// or panic. Run with -race to detect data races.
func TestTaskMemoryLanePublishConcurrentWithDrainNoLostUpdate(t *testing.T) {
	now := time.Now()
	conv := &domain.Conversation{
		ID:    "c-concurrent",
		Title: "concurrent test",
		Type:  domain.ConversationTypeConversation,
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "concurrent drain and publish test"},
		},
	}
	store := &markerConvStore{conv: cloneConvForMarker(conv)}
	svc := agent.New(agent.Deps{Conversations: store})

	const n = 50
	var wg sync.WaitGroup
	wg.Add(2 * n)

	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			marker := domain.AnnouncedRecord{
				ID:              "rec-" + string(rune('a'+i%26)) + string(rune('a'+i/26)),
				LastConfirmedAt: now,
			}
			svc.PublishTaskMemoryAnnouncement("c-concurrent",
				`{"type":"task_memory","hits":[]}`,
				"test message",
				[]domain.AnnouncedRecord{marker},
			)
		}(i)
		go func() {
			defer wg.Done()
			svc.DrainAnnouncements(&agent.TurnRun{
				ID:             "run-drain",
				ConversationID: "c-concurrent",
				Ctx:            context.Background(),
			})
		}()
	}
	wg.Wait()

	got, _ := store.Get("c-concurrent")
	// Markers must not be lost: every published marker that was not
	// already covered should be in LastAnnouncedRecords.
	if len(got.LastAnnouncedRecords) == 0 {
		t.Fatalf("markers lost: LastAnnouncedRecords is empty after concurrent publish+drain")
	}
}

// learn import guard: ensure the lane searcher helper compiles.
var _ learn.Embedder = (*laneTestEmbedder)(nil)
