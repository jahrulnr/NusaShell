package application

import (
	"strings"
	"testing"
	"time"

	"nusashell/domain"
)

func TestMaybeAnnounceTaskMemoryAnnouncesNewRecordOnce(t *testing.T) {
	now := time.Now()
	oldClock := clockNow
	clockNow = func() time.Time { return now }
	defer func() { clockNow = oldClock }()

	conv := &domain.Conversation{ID: "c1", Title: "Fix memory announcement queue", Workspace: "/w/nusashell"}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	recs := &fakeMemoryRecordStore{items: []*domain.MemoryRecord{{
		ID:            "rec-new",
		Type:          domain.MemoryTypeFact,
		Status:        domain.MemoryStatusLearned,
		Body:          "Fix memory announcement queue nusashell: the queue dedupes exact duplicates by Type+Args+Message.",
		LastConfirmed: now,
	}}}
	app := &App{Conversations: store, MemoryRecords: recs, Bus: NewBus(), Logs: &fakeLogStore{}}

	app.maybeAnnounceTaskMemory("c1", conv)

	got, err := store.Get("c1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.PendingAnnouncements) != 1 {
		t.Fatalf("pending = %+v, want 1 task_memory announcement", got.PendingAnnouncements)
	}
	pa := got.PendingAnnouncements[0]
	if pa.Type != taskMemoryAnnounceType {
		t.Fatalf("type = %q, want %q", pa.Type, taskMemoryAnnounceType)
	}
	if !strings.Contains(pa.Args, `"type":"task_memory"`) || !strings.Contains(pa.Args, "rec-new") {
		t.Errorf("args missing type/hits: %s", pa.Args)
	}
	if !strings.Contains(pa.Message, "task memory") {
		t.Errorf("message = %q", pa.Message)
	}
	if len(got.LastAnnouncedRecords) != 1 || got.LastAnnouncedRecords[0] != "rec-new" {
		t.Fatalf("dedup marker = %+v, want rec-new", got.LastAnnouncedRecords)
	}

	again, _ := store.Get("c1")
	app.maybeAnnounceTaskMemory("c1", again)
	got2, _ := store.Get("c1")
	if len(got2.PendingAnnouncements) != 1 {
		t.Fatalf("record must be announced once, pending = %+v", got2.PendingAnnouncements)
	}
}

func TestMaybeAnnounceTaskMemorySkipsOldRecords(t *testing.T) {
	now := time.Now()
	oldClock := clockNow
	clockNow = func() time.Time { return now }
	defer func() { clockNow = oldClock }()

	conv := &domain.Conversation{ID: "c1", Title: "Brief chit-chat room"}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	recs := &fakeMemoryRecordStore{items: []*domain.MemoryRecord{{
		ID:            "rec-old",
		Type:          domain.MemoryTypeFact,
		Status:        domain.MemoryStatusLearned,
		Body:          "ancient fact",
		LastConfirmed: now.Add(-200 * time.Hour),
	}}}
	app := &App{Conversations: store, MemoryRecords: recs, Bus: NewBus(), Logs: &fakeLogStore{}}

	app.maybeAnnounceTaskMemory("c1", conv)
	got, _ := store.Get("c1")
	if len(got.PendingAnnouncements) != 0 {
		t.Fatalf("old record must not be announced, pending = %+v", got.PendingAnnouncements)
	}
}

func TestTaskMemorySkipsPipelineOrigin(t *testing.T) {
	conv := &domain.Conversation{ID: "c1", Origin: domain.ConversationOriginPipeline, Title: "pipeline step"}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	app := &App{Conversations: store, MemoryRecords: &fakeMemoryRecordStore{}, Bus: NewBus(), Logs: &fakeLogStore{}}
	app.maybeAnnounceTaskMemory("c1", conv)
	got, _ := store.Get("c1")
	if len(got.PendingAnnouncements) != 0 {
		t.Fatalf("pipeline conversations must be skipped, pending = %+v", got.PendingAnnouncements)
	}
}

func TestTaskMemoryQuery(t *testing.T) {
	conv := &domain.Conversation{Title: "Fix the room history window", Workspace: "/home/k/ns"}
	q := taskMemoryQuery(conv)
	if !strings.Contains(q, "Fix the room history window") || !strings.Contains(q, "ns") {
		t.Errorf("query = %q", q)
	}
	empty := taskMemoryQuery(&domain.Conversation{})
	if empty != "" {
		t.Errorf("empty conversation query = %q, want empty", empty)
	}
}

func TestTruncateUTF8(t *testing.T) {
	if got := truncateUTF8("hello", 100); got != "hello" {
		t.Errorf("short string must pass through, got %q", got)
	}
	s := "héllo wörld"
	got := truncateUTF8(s, 6)
	if !strings.HasPrefix(got, "héllo") || !strings.HasSuffix(got, "…") {
		t.Errorf("truncated %q from %q", got, s)
	}
}
