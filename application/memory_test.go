package application

import (
	"context"
	"encoding/json"
	"nusashell/contracts"
	"nusashell/domain"
	"strings"
	"testing"
	"time"
)

// --- from task_memory_test.go ---

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

func TestTaskMemoryAlphaWords(t *testing.T) {
	got := taskMemoryAlphaWords(`hey. gimana 🎉 nusashell saat ini? .tmpcQpqGO`)
	want := map[string]bool{
		"hey": true, "gimana": true, "nusashell": true, "saat": true, "ini": true, "tmpcqpqgo": true,
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for w := range want {
		if !got[w] {
			t.Errorf("missing %q in %v", w, got)
		}
	}
	if len(taskMemoryAlphaWords("")) != 0 {
		t.Fatal("empty input must yield no words")
	}
	if len(taskMemoryAlphaWords("... 🎉 123 !")) != 0 {
		t.Fatal("punctuation, emoji, and digits must not yield words")
	}
	if len(taskMemoryAlphaWords(".")) != 0 {
		t.Fatal("filepath.Base(\"\") token must not yield a word")
	}
}

func TestMaybeAnnounceTaskMemoryIgnoresEmptyWorkspacePunctuation(t *testing.T) {
	now := time.Now()
	oldClock := clockNow
	clockNow = func() time.Time { return now }
	defer func() { clockNow = oldClock }()

	conv := &domain.Conversation{
		ID:    "c1",
		Title: "hey. gimana menurut mu folder ini?",
	}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	recs := &fakeMemoryRecordStore{items: []*domain.MemoryRecord{{
		ID:            "rec-junk",
		Type:          domain.MemoryTypePreference,
		Status:        domain.MemoryStatusLearned,
		Body:          "sip, useless bagi multi lang. yaudah, buat jadi summary ya.",
		Scope:         domain.MemoryScope{Project: "OtherProject"},
		LastConfirmed: now,
	}}}
	app := &App{Conversations: store, MemoryRecords: recs, Bus: NewBus(), Logs: &fakeLogStore{}}

	app.maybeAnnounceTaskMemory("c1", conv)
	got, _ := store.Get("c1")
	if len(got.PendingAnnouncements) != 0 {
		t.Fatalf("period/punctuation must not match, pending = %+v", got.PendingAnnouncements)
	}
}

func TestMaybeAnnounceTaskMemoryMatchesSharedAlphabeticWord(t *testing.T) {
	now := time.Now()
	oldClock := clockNow
	clockNow = func() time.Time { return now }
	defer func() { clockNow = oldClock }()

	conv := &domain.Conversation{ID: "c1", Title: "hey. 🎉", Workspace: "/tmp/.tmpcQpqGO"}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	recs := &fakeMemoryRecordStore{items: []*domain.MemoryRecord{{
		ID:            "rec-ws",
		Type:          domain.MemoryTypeFact,
		Status:        domain.MemoryStatusLearned,
		Body:          "tmpcQpqGO is an empty trap workspace.",
		LastConfirmed: now,
	}}}
	app := &App{Conversations: store, MemoryRecords: recs, Bus: NewBus(), Logs: &fakeLogStore{}}

	app.maybeAnnounceTaskMemory("c1", conv)
	got, _ := store.Get("c1")
	if len(got.PendingAnnouncements) != 1 {
		t.Fatalf("shared alphabetic word must match, pending = %+v", got.PendingAnnouncements)
	}
}

// --- from memory_user_update_test.go ---

type userUpdateStore struct {
	entry   domain.DocumentEntry
	updates int
}

func (s *userUpdateStore) Load() *domain.MemoryDocument {
	return &domain.MemoryDocument{Entries: []domain.DocumentEntry{s.entry}}
}

func (s *userUpdateStore) Update(entries []domain.DocumentEntry) error {
	s.updates++
	if len(entries) == 0 {
		s.entry = domain.DocumentEntry{}
	} else {
		s.entry = entries[0]
	}
	return nil
}

func (s *userUpdateStore) Replace(string, string) error { return nil }
func (s *userUpdateStore) Path() string                 { return "" }

func TestMemoryUserUpdateReplacesOnlyUserDocument(t *testing.T) {
	user := &userUpdateStore{entry: domain.DocumentEntry{Content: "old", Source: "agent"}}
	app := &App{User: user, Bus: NewBus()}

	result, rpcErr := app.Dispatch(context.Background(), contracts.MethodMemoryUserUpdate, json.RawMessage(`{"content":"new profile\nwith preferences"}`))
	if rpcErr != nil {
		t.Fatalf("Dispatch user update: %v", rpcErr)
	}
	got, ok := result.(contracts.MemoryUserUpdateResult)
	if !ok {
		t.Fatalf("result type = %T, want MemoryUserUpdateResult", result)
	}
	if got.Entry.Content != "new profile\nwith preferences" {
		t.Fatalf("returned content = %q", got.Entry.Content)
	}
	if user.entry.Content != got.Entry.Content || user.entry.Source != "user" {
		t.Fatalf("stored user document = %+v, want user-owned updated document", user.entry)
	}
	if user.updates != 1 {
		t.Fatalf("user updates = %d, want 1", user.updates)
	}
}

func TestMemoryUserUpdateAllowsClearingDocument(t *testing.T) {
	user := &userUpdateStore{entry: domain.DocumentEntry{Content: "old"}}
	app := &App{User: user}

	if _, rpcErr := app.handleMemoryUserUpdate(contracts.MemoryUserUpdateRequest{}); rpcErr != nil {
		t.Fatalf("clearing user document: %v", rpcErr)
	}
	if user.entry.Content != "" {
		t.Fatalf("cleared user content = %q", user.entry.Content)
	}
}

func TestMemoryUserUpdateRejectsOverCap(t *testing.T) {
	user := &userUpdateStore{entry: domain.DocumentEntry{Content: "keep"}}
	app := &App{User: user}
	content := make([]byte, domain.UserCharCap+1)
	for i := range content {
		content[i] = 'x'
	}

	_, rpcErr := app.handleMemoryUserUpdate(contracts.MemoryUserUpdateRequest{Content: string(content)})
	if rpcErr == nil || rpcErr.Code != contracts.CodeValidation {
		t.Fatalf("over-cap error = %+v, want VALIDATION_ERROR", rpcErr)
	}
	if user.updates != 0 || user.entry.Content != "keep" {
		t.Fatalf("over-cap update changed user document: %+v", user.entry)
	}
}

func TestMemoryAgentUpdateReplacesOnlyAgentDocument(t *testing.T) {
	agent := &userUpdateStore{entry: domain.DocumentEntry{Content: "old", Source: "user"}}
	app := &App{Agent: agent, Bus: NewBus()}

	result, rpcErr := app.Dispatch(context.Background(), contracts.MethodMemoryAgentUpdate, json.RawMessage(`{"content":"agent conventions\nwith references"}`))
	if rpcErr != nil {
		t.Fatalf("Dispatch agent update: %v", rpcErr)
	}
	got, ok := result.(contracts.MemoryAgentUpdateResult)
	if !ok {
		t.Fatalf("result type = %T, want MemoryAgentUpdateResult", result)
	}
	if got.Entry.Content != "agent conventions\nwith references" {
		t.Fatalf("returned content = %q", got.Entry.Content)
	}
	if agent.entry.Content != got.Entry.Content || agent.entry.Source != "user" {
		t.Fatalf("stored agent = %+v, want user-edited agent document", agent.entry)
	}
	if agent.updates != 1 {
		t.Fatalf("agent updates = %d, want 1", agent.updates)
	}
}

func TestMemoryAgentUpdateRejectsOverCap(t *testing.T) {
	agent := &userUpdateStore{entry: domain.DocumentEntry{Content: "keep"}}
	app := &App{Agent: agent}
	content := make([]byte, domain.AgentCharCap+1)
	for i := range content {
		content[i] = 'x'
	}

	_, rpcErr := app.handleMemoryAgentUpdate(contracts.MemoryAgentUpdateRequest{Content: string(content)})
	if rpcErr == nil || rpcErr.Code != contracts.CodeValidation {
		t.Fatalf("over-cap error = %+v, want VALIDATION_ERROR", rpcErr)
	}
	if agent.updates != 0 || agent.entry.Content != "keep" {
		t.Fatalf("over-cap update changed agent: %+v", agent.entry)
	}
}
