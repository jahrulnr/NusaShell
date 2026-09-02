package application

import (
	"strings"
	"sync"
	"testing"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
)

func TestDrainAnnouncementsInjectsPendingAtRoundBoundary(t *testing.T) {
	conv := &domain.Conversation{ID: "c1"}
	conv.QueueAnnouncement(domain.PendingAnnouncement{
		ID: "announce-1", Type: "config_changed", Args: `{"type":"config_changed"}`, Message: "config changed", CreatedAt: time.Now(),
	})
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	app := &App{Conversations: store, Bus: NewBus(), Logs: &fakeLogStore{}}
	run := &TurnRun{ID: "r1", ConversationID: "c1"}

	applied, err := app.drainAnnouncements(run)
	if err != nil {
		t.Fatalf("drainAnnouncements: %v", err)
	}
	if !applied {
		t.Fatal("expected an announcement to be injected")
	}
	last := conv.Messages[len(conv.Messages)-1]
	if last.Role != domain.RoleAssistant || len(last.ToolCalls) != 1 {
		t.Fatalf("injected message = %+v, want assistant announcement tool call", last)
	}
	tc := last.ToolCalls[0]
	if tc.Name != domain.AnnouncementToolName || tc.Output != "config changed" {
		t.Fatalf("tool call = %+v, want announcement with pre-filled result", tc)
	}
	if len(conv.PendingAnnouncements) != 0 {
		t.Fatalf("pending queue not cleared: %+v", conv.PendingAnnouncements)
	}
}

func TestDrainAnnouncementsMergesAllPendingTypesIntoOne(t *testing.T) {
	conv := &domain.Conversation{ID: "c1"}
	conv.QueueAnnouncement(domain.PendingAnnouncement{
		ID: "announce-1", Type: "config_changed", Args: `{"type":"config_changed"}`, Message: "config changed", CreatedAt: time.Now(),
	})
	conv.QueueAnnouncement(domain.PendingAnnouncement{
		ID: "announce-2", Type: "memory_changed", Args: `{"type":"memory_changed"}`, Message: "memory changed", CreatedAt: time.Now(),
	})
	conv.QueueAnnouncement(domain.PendingAnnouncement{
		ID: "announce-3", Type: "skills_changed", Args: `{"type":"skills_changed"}`, Message: "skills changed", CreatedAt: time.Now(),
	})
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	app := &App{Conversations: store, Bus: NewBus(), Logs: &fakeLogStore{}}
	run := &TurnRun{ID: "r1", ConversationID: "c1"}

	applied, err := app.drainAnnouncements(run)
	if err != nil {
		t.Fatalf("drainAnnouncements: %v", err)
	}
	if !applied {
		t.Fatal("expected announcements to be injected")
	}
	// All pending notices merge into ONE synthetic announcement tool call.
	if len(conv.Messages) != 1 {
		t.Fatalf("messages = %d, want 1 merged announcement message", len(conv.Messages))
	}
	tc := conv.Messages[0].ToolCalls
	if len(tc) != 1 || tc[0].Name != domain.AnnouncementToolName {
		t.Fatalf("injected message = %+v, want a single announcement tool call", conv.Messages[0])
	}
	wantOutput := "config changed\n---\nmemory changed\n---\nskills changed"
	if tc[0].Output != wantOutput {
		t.Fatalf("output = %q, want merged notices:\n%s", tc[0].Output, wantOutput)
	}
	for _, want := range []string{`"items"`, `"config_changed"`, `"memory_changed"`, `"skills_changed"`} {
		if !strings.Contains(tc[0].Args, want) {
			t.Fatalf("args must carry %s so every notice stays self-describing, got %s", want, tc[0].Args)
		}
	}
	if len(conv.PendingAnnouncements) != 0 {
		t.Fatalf("pending queue not cleared: %+v", conv.PendingAnnouncements)
	}
}

func TestPublishAnnouncementCapDropsOldest(t *testing.T) {
	conv := &domain.Conversation{ID: "c1"}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	app := &App{Conversations: store, Bus: NewBus(), Logs: &fakeLogStore{}}

	for i := 0; i < maxPendingAnnouncements+1; i++ {
		app.publishAnnouncement("c1", newAnnouncement(
			"config_changed",
			`{"type":"config_changed"}`,
			"config changed "+strings.Repeat("x", i+1),
		))
	}

	got, err := store.Get("c1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.PendingAnnouncements) != maxPendingAnnouncements {
		t.Fatalf("pending queue = %d entries, want capped at %d", len(got.PendingAnnouncements), maxPendingAnnouncements)
	}
	// The very first published notice was dropped, the newest survives.
	if got.PendingAnnouncements[0].Message == "config changed x" {
		t.Fatalf("oldest entry must be dropped, got %q first", got.PendingAnnouncements[0].Message)
	}
	if last := got.PendingAnnouncements[len(got.PendingAnnouncements)-1].Message; last != "config changed "+strings.Repeat("x", maxPendingAnnouncements+1) {
		t.Fatalf("last entry = %q, want the newest notice kept", last)
	}
}

func TestDrainAnnouncementsEmptyQueueIsNoop(t *testing.T) {
	conv := &domain.Conversation{ID: "c1"}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	app := &App{Conversations: store, Bus: NewBus(), Logs: &fakeLogStore{}}
	run := &TurnRun{ID: "r1", ConversationID: "c1"}

	applied, err := app.drainAnnouncements(run)
	if err != nil {
		t.Fatalf("drainAnnouncements: %v", err)
	}
	if applied {
		t.Fatal("empty queue must not inject anything")
	}
	if len(conv.Messages) != 0 {
		t.Fatalf("no message must be injected, got %d", len(conv.Messages))
	}
}

func TestPublishAnnouncementPersistsPending(t *testing.T) {
	conv := &domain.Conversation{ID: "c1"}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	app := &App{Conversations: store, Bus: NewBus(), Logs: &fakeLogStore{}}

	app.publishAnnouncement("c1", newAnnouncement("config_changed", `{"type":"config_changed"}`, "config changed"))

	if len(conv.PendingAnnouncements) != 1 {
		t.Fatalf("pending queue = %+v, want 1 entry", conv.PendingAnnouncements)
	}
	pa := conv.PendingAnnouncements[0]
	if pa.Type != "config_changed" || pa.Message != "config changed" {
		t.Fatalf("pending entry = %+v, want config_changed with message", pa)
	}
}

func TestPublishAnnouncementDedupsIdenticalAppendsDistinct(t *testing.T) {
	conv := &domain.Conversation{ID: "c1"}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	app := &App{Conversations: store, Bus: NewBus(), Logs: &fakeLogStore{}}

	// Distinct content: both arrive (append, never last-write-win).
	app.publishAnnouncement("c1", newAnnouncement("config_changed", `{"type":"config_changed","changed":["subagent"]}`, "config changed 1"))
	app.publishAnnouncement("c1", newAnnouncement("config_changed", `{"type":"config_changed","changed":["provider"]}`, "config changed 2"))
	// Exact duplicate of the first: skipped so bursts never double-book.
	app.publishAnnouncement("c1", newAnnouncement("config_changed", `{"type":"config_changed","changed":["subagent"]}`, "config changed 1"))

	// Save persists a clone, so re-fetch from the store.
	got, err := store.Get("c1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.PendingAnnouncements) != 2 {
		t.Fatalf("pending queue = %+v, want 2 entries (deduped identical, appended distinct)", got.PendingAnnouncements)
	}
	if got.PendingAnnouncements[0].Message != "config changed 1" {
		t.Fatalf("entries[0] = %q, want first published order preserved", got.PendingAnnouncements[0].Message)
	}
	if got.PendingAnnouncements[1].Message != "config changed 2" {
		t.Fatalf("entries[1] = %q, want second distinct entry appended", got.PendingAnnouncements[1].Message)
	}
}

func TestPublishAnnouncementToAllSkipsHiddenAndSelf(t *testing.T) {
	visible := &domain.Conversation{ID: "c1"}
	hidden := &domain.Conversation{ID: "c2", Origin: domain.ConversationOriginPipeline}
	self := &domain.Conversation{ID: "c3"}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": visible, "c2": hidden, "c3": self}}
	app := &App{Conversations: store, Bus: NewBus(), Logs: &fakeLogStore{}}

	app.publishAnnouncementToAll(newAnnouncement("memory_changed", `{"type":"memory_changed"}`, "memory changed"), "c3")

	if len(visible.PendingAnnouncements) != 1 {
		t.Fatalf("visible conversation pending = %+v, want 1", visible.PendingAnnouncements)
	}
	if len(hidden.PendingAnnouncements) != 0 {
		t.Fatalf("hidden conversation must be skipped, got %+v", hidden.PendingAnnouncements)
	}
	if len(self.PendingAnnouncements) != 0 {
		t.Fatalf("self conversation must be skipped, got %+v", self.PendingAnnouncements)
	}
}

func TestPublishDrainConcurrentNoLostOrDoubleInjection(t *testing.T) {
	// The per-conversation announcement lock serializes load-modify-save
	// between publishers and the worker drain: every published announcement
	// is injected exactly once, and the queue is never left behind or
	// double-injected. All publishes here are byte-identical, so the
	// exact-duplicate dedup collapses them into a single pending entry.
	conv := &domain.Conversation{ID: "c1"}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	app := &App{Conversations: store, Bus: NewBus(), Logs: &fakeLogStore{}}
	run := &TurnRun{ID: "r1", ConversationID: "c1"}

	const publishers = 8
	const perPublisher = 10
	var wg sync.WaitGroup
	for p := 0; p < publishers; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for i := 0; i < perPublisher; i++ {
				app.publishAnnouncement("c1", newAnnouncement(
					"config_changed",
					`{"type":"config_changed"}`,
					"config changed",
				))
			}
		}(p)
	}
	wg.Wait()

	// All publishes coalesce to a single pending entry.
	got, err := store.Get("c1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.PendingAnnouncements) != 1 {
		t.Fatalf("pending queue = %+v, want 1 coalesced entry", got.PendingAnnouncements)
	}

	// Drain injects it exactly once.
	applied, err := app.drainAnnouncements(run)
	if err != nil {
		t.Fatalf("drainAnnouncements: %v", err)
	}
	if !applied {
		t.Fatal("expected the coalesced announcement to be injected")
	}
	// Save persists a clone, so re-fetch from the store.
	got, _ = store.Get("c1")
	if len(got.Messages) != 1 {
		t.Fatalf("messages = %d, want exactly 1 injected announcement", len(got.Messages))
	}
	if len(got.PendingAnnouncements) != 0 {
		t.Fatalf("pending queue not cleared after drain: %+v", got.PendingAnnouncements)
	}
}

func TestAddTurnMessagesDrainsPendingAnnouncements(t *testing.T) {
	conv := &domain.Conversation{ID: "c1", Messages: []domain.Message{{ID: "u0", Role: domain.RoleUser, Content: "earlier"}}}
	conv.QueueAnnouncement(domain.PendingAnnouncement{
		ID: "announce-1", Type: "config_changed", Args: `{"type":"config_changed"}`, Message: "config changed", CreatedAt: time.Now(),
	})
	app := &App{Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}, Bus: NewBus(), Logs: &fakeLogStore{}}

	userMsg := domain.Message{ID: "u1", Role: domain.RoleUser, Content: "hi", Status: domain.StatusDone}
	asstMsg := domain.Message{ID: "a1", Role: domain.RoleAssistant}
	app.addTurnMessages(conv, userMsg, asstMsg)

	// Order: user → announcement → assistant placeholder.
	if len(conv.Messages) != 4 {
		t.Fatalf("messages = %d, want 4 (earlier, user, announcement, assistant)", len(conv.Messages))
	}
	if conv.Messages[1].ID != "u1" {
		t.Fatalf("messages[1] = %s, want the new user message", conv.Messages[1].ID)
	}
	ann := conv.Messages[2]
	if ann.Role != domain.RoleAssistant || len(ann.ToolCalls) != 1 || ann.ToolCalls[0].Name != domain.AnnouncementToolName {
		t.Fatalf("messages[2] = %+v, want announcement tool call", ann)
	}
	if conv.Messages[3].ID != "a1" {
		t.Fatalf("messages[3] = %s, want the assistant placeholder", conv.Messages[3].ID)
	}
	if len(conv.PendingAnnouncements) != 0 {
		t.Fatalf("pending queue not drained: %+v", conv.PendingAnnouncements)
	}
}

func TestQueueAnnouncementDedupsIdenticalAppendsDistinct(t *testing.T) {
	c := &domain.Conversation{}
	// a1 and a2 carry identical content (only IDs differ): deduped.
	c.QueueAnnouncement(domain.PendingAnnouncement{ID: "a1", Type: "config_changed"})
	c.QueueAnnouncement(domain.PendingAnnouncement{ID: "a2", Type: "config_changed"})
	// Different type: appended.
	c.QueueAnnouncement(domain.PendingAnnouncement{ID: "a3", Type: "memory_changed"})
	if len(c.PendingAnnouncements) != 2 {
		t.Fatalf("pending = %+v, want 2 entries (deduped identical, appended distinct)", c.PendingAnnouncements)
	}
	if c.PendingAnnouncements[0].ID != "a1" {
		t.Fatalf("config entry = %q, want a1 (first arrival kept)", c.PendingAnnouncements[0].ID)
	}
	if c.PendingAnnouncements[1].ID != "a3" {
		t.Fatalf("memory entry = %q, want a3 appended", c.PendingAnnouncements[1].ID)
	}
}

// memSettingsStore is a minimal in-memory SettingsStore for tests.
type memSettingsStore struct {
	s domain.Settings
}

func (m *memSettingsStore) Get() domain.Settings { return m.s }
func (m *memSettingsStore) Set(s domain.Settings) error {
	m.s = s
	return nil
}

func TestHandleSettingsSetPublishesOnlyOnUserPromptChange(t *testing.T) {
	conv := &domain.Conversation{ID: "c1"}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	app := &App{
		Conversations: store,
		Bus:           NewBus(),
		Logs:          &fakeLogStore{},
		Settings:      &memSettingsStore{s: domain.DefaultSettings()},
	}

	// Unrelated settings change: no announcement.
	if _, rpcErr := app.handleSettingsSet(contracts.SettingsSetRequest{
		MaxToolRounds: intPtr(5),
	}); rpcErr != nil {
		t.Fatalf("handleSettingsSet: %v", rpcErr.Message)
	}
	if len(conv.PendingAnnouncements) != 0 {
		t.Fatalf("unrelated settings change must not announce, got %+v", conv.PendingAnnouncements)
	}

	// Real UserPrompt change: announcement with the user_prompt surface.
	prompt := "Always answer in Indonesian."
	if _, rpcErr := app.handleSettingsSet(contracts.SettingsSetRequest{
		UserPrompt: &prompt,
	}); rpcErr != nil {
		t.Fatalf("handleSettingsSet: %v", rpcErr.Message)
	}
	if len(conv.PendingAnnouncements) != 1 {
		t.Fatalf("pending = %+v, want 1 config_changed entry", conv.PendingAnnouncements)
	}
	pa := conv.PendingAnnouncements[0]
	if pa.Type != "config_changed" || !strings.Contains(pa.Args, "user_prompt") {
		t.Fatalf("pending = %+v, want config_changed with user_prompt surface", pa)
	}

	// Same value saved again: no new announcement (coalesced by type anyway,
	// but the publish must not fire at all).
	if _, rpcErr := app.handleSettingsSet(contracts.SettingsSetRequest{
		UserPrompt: &prompt,
	}); rpcErr != nil {
		t.Fatalf("handleSettingsSet: %v", rpcErr.Message)
	}
	if len(conv.PendingAnnouncements) != 1 {
		t.Fatalf("unchanged UserPrompt must not publish, got %+v", conv.PendingAnnouncements)
	}
}

func intPtr(v int) *int { return &v }
