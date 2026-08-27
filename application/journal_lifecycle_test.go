package application

import (
	"context"
	"sync"
	"testing"

	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/infrastructure/ai/core"
)

// lifecycleJournal extends fakeChangeJournal with the optional Archive and
// Remove lifecycle methods so the App's type assertions bind.
type lifecycleJournal struct {
	fakeChangeJournal
	mu       sync.Mutex
	archived []string
	removed  []string
}

func (j *lifecycleJournal) Archive(conversationID string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.archived = append(j.archived, conversationID)
	return nil
}

func (j *lifecycleJournal) Remove(conversationID string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.removed = append(j.removed, conversationID)
	return nil
}

func (j *lifecycleJournal) archivedFor(conversationID string) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, id := range j.archived {
		if id == conversationID {
			return true
		}
	}
	return false
}

func (j *lifecycleJournal) removedFor(conversationID string) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, id := range j.removed {
		if id == conversationID {
			return true
		}
	}
	return false
}

// TestRunTurnArchivesJournalAtTurnEnd pins the turn-end hook: when a turn
// finishes, the conversation's live journal is compressed so journal.jsonl
// stays bounded across long sessions.
func TestRunTurnArchivesJournalAtTurnEnd(t *testing.T) {
	conv := &domain.Conversation{
		ID: "c_arch",
		Messages: []domain.Message{
			{ID: "u1", Role: domain.RoleUser, Content: "halo", Status: domain.StatusDone},
			{ID: "a1", Role: domain.RoleAssistant},
		},
	}
	adapter := &freshTurnStreamAdapter{}
	settings := domain.DefaultSettings()
	settings.CompactionEnabled = false
	journal := &lifecycleJournal{}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c_arch": conv}},
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		Toolbox:       &recordingToolbox{},
		Settings:      &fakeSettingsStore{settings: settings},
		Journal:       journal,
		Factory: func(context.Context, *domain.Provider, string) (core.Provider, error) {
			return adapter, nil
		},
		runs: map[string]*TurnRun{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c_arch", Ctx: ctx, Cancel: cancel}
	app.runTurn(run, &domain.Provider{ID: "p", Kind: domain.ProviderChat}, "key", "model", "", "a1", false, ModelCapabilities{})

	if !journal.archivedFor("c_arch") {
		t.Fatal("turn end must archive the conversation journal")
	}
}

// TestRunTurnSkipsArchiveWhenJournalLacksIt verifies the narrow type
// assertion: a ChangeJournal without Archive still works (no panic).
func TestRunTurnSkipsArchiveWhenJournalLacksIt(t *testing.T) {
	conv := &domain.Conversation{
		ID: "c_noarch",
		Messages: []domain.Message{
			{ID: "u1", Role: domain.RoleUser, Content: "halo", Status: domain.StatusDone},
			{ID: "a1", Role: domain.RoleAssistant},
		},
	}
	adapter := &freshTurnStreamAdapter{}
	settings := domain.DefaultSettings()
	settings.CompactionEnabled = false
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c_noarch": conv}},
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		Toolbox:       &recordingToolbox{},
		Settings:      &fakeSettingsStore{settings: settings},
		Journal:       &fakeChangeJournal{}, // no Archive method
		Factory: func(context.Context, *domain.Provider, string) (core.Provider, error) {
			return adapter, nil
		},
		runs: map[string]*TurnRun{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c_noarch", Ctx: ctx, Cancel: cancel}
	app.runTurn(run, &domain.Provider{ID: "p", Kind: domain.ProviderChat}, "key", "model", "", "a1", false, ModelCapabilities{})
	// No panic, turn completes.
}

// TestConversationDeleteRemovesJournal pins the delete hook: deleting a
// conversation also deletes its journal sidecar.
func TestConversationDeleteRemovesJournal(t *testing.T) {
	conv := &domain.Conversation{ID: "c_del", Title: "t"}
	journal := &lifecycleJournal{}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c_del": conv}},
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		Journal:       journal,
		runs:          map[string]*TurnRun{},
	}

	res, rpcErr := app.handleConversationsDelete(contracts.ConversationIDRequest{ID: "c_del"})
	if rpcErr != nil {
		t.Fatalf("delete failed: %+v", rpcErr)
	}
	if ok, _ := res.(map[string]bool); !ok["ok"] {
		t.Fatalf("unexpected result: %v", res)
	}
	if !journal.removedFor("c_del") {
		t.Fatal("conversation delete must remove the journal sidecar")
	}
}

// TestConversationDeleteSkipsRemoveWhenJournalLacksIt verifies the narrow
// type assertion for Remove: a ChangeJournal without Remove still works.
func TestConversationDeleteSkipsRemoveWhenJournalLacksIt(t *testing.T) {
	conv := &domain.Conversation{ID: "c_del2", Title: "t"}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c_del2": conv}},
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		Journal:       &fakeChangeJournal{}, // no Remove method
		runs:          map[string]*TurnRun{},
	}

	res, rpcErr := app.handleConversationsDelete(contracts.ConversationIDRequest{ID: "c_del2"})
	if rpcErr != nil {
		t.Fatalf("delete failed: %+v", rpcErr)
	}
	if ok, _ := res.(map[string]bool); !ok["ok"] {
		t.Fatalf("unexpected result: %v", res)
	}
}

// TestConversationDeleteNilJournal verifies delete works with no journal
// wired at all.
func TestConversationDeleteNilJournal(t *testing.T) {
	conv := &domain.Conversation{ID: "c_del3", Title: "t"}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c_del3": conv}},
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		runs:          map[string]*TurnRun{},
	}

	res, rpcErr := app.handleConversationsDelete(contracts.ConversationIDRequest{ID: "c_del3"})
	if rpcErr != nil {
		t.Fatalf("delete failed: %+v", rpcErr)
	}
	if ok, _ := res.(map[string]bool); !ok["ok"] {
		t.Fatalf("unexpected result: %v", res)
	}
}
