package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"nusashell/domain"
)

// recordingCompactionJournal is a minimal ChangeJournal used to assert that
// a successful compaction writes exactly one durable audit event. The other
// journal methods are irrelevant for this path.
type recordingCompactionJournal struct {
	compactions []domain.CompactionEvent
}

func (j *recordingCompactionJournal) WrapMutation(_ context.Context, _ MutationRequest, exec func() error) error {
	return exec()
}

func (j *recordingCompactionJournal) SessionState(context.Context, string, string) (*WorkspaceState, error) {
	return nil, errors.New("not used in compaction journal test")
}

func (j *recordingCompactionJournal) RecordCompaction(_ string, ev domain.CompactionEvent) error {
	j.compactions = append(j.compactions, ev)
	return nil
}

func TestCompactionRecordsJournalEvent(t *testing.T) {
	// A successful compaction must leave exactly one durable audit event in
	// the journal (trigger, model, budget, summary), written only after the
	// compacted conversation itself was persisted.
	msgs := make([]domain.Message, 0, 10)
	body := strings.Repeat("abcdefghij", 80) // 800 chars ≈ 200 tokens
	for i := 0; i < 10; i++ {
		msgs = append(msgs, domain.Message{
			ID: fmt.Sprintf("m%d", i), Role: domain.RoleUser, Content: body, Status: domain.StatusDone,
		})
	}
	conv := &domain.Conversation{ID: "c1", Messages: msgs}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	adapter := &recordingCompleteAdapter{toolCallSummaries: []string{validTestSummary}}
	journal := &recordingCompactionJournal{}
	app := &App{Conversations: store, Logs: &fakeLogStore{}, Bus: NewBus(), Journal: journal}

	summary, err := app.compactConversation(context.Background(), stubProviderContext(adapter), conv, "compactor-model", 4000, domain.DefaultSettings(), domain.CompactionTriggerProactive)
	if err != nil {
		t.Fatalf("compactConversation: %v", err)
	}
	if summary != validTestSummary {
		t.Fatalf("summary = %q, want %q", summary, validTestSummary)
	}
	if got := len(journal.compactions); got != 1 {
		t.Fatalf("journal compaction events = %d, want 1", got)
	}
	ev := journal.compactions[0]
	if ev.Trigger != domain.CompactionTriggerProactive {
		t.Fatalf("trigger = %q, want %q", ev.Trigger, domain.CompactionTriggerProactive)
	}
	if ev.Model != "compactor-model" {
		t.Fatalf("model = %q, want compactor-model", ev.Model)
	}
	if ev.KeepBudget <= 0 {
		t.Fatalf("keepBudget = %d, want > 0", ev.KeepBudget)
	}
	if ev.Summary != validTestSummary {
		t.Fatalf("summary = %q, want %q", ev.Summary, validTestSummary)
	}
}

func TestCompactionWithNilJournalStillSucceeds(t *testing.T) {
	// Journal is an optional port; a nil journal must not panic nor change
	// compaction behavior. Enough messages are used so compaction actually
	// runs (a tiny conversation fits the keep budget and produces no pass).
	body := strings.Repeat("abcdefghij", 80)
	msgs := make([]domain.Message, 0, 8)
	for i := 0; i < 8; i++ {
		msgs = append(msgs, domain.Message{
			ID: fmt.Sprintf("u%d", i), Role: domain.RoleUser, Content: body, Status: domain.StatusDone,
		})
	}
	conv := &domain.Conversation{ID: "c2", Messages: msgs}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c2": conv}},
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
	}
	adapter := &recordingCompleteAdapter{toolCallSummaries: []string{validTestSummary}}
	summary, err := app.compactConversation(context.Background(), stubProviderContext(adapter), conv, "model", 4000, domain.DefaultSettings(), domain.CompactionTriggerInitial)
	if err != nil {
		t.Fatalf("compactConversation with nil journal: %v", err)
	}
	if summary != validTestSummary {
		t.Fatalf("summary = %q, want %q", summary, validTestSummary)
	}
}
