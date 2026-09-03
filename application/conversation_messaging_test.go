package application

import (
	"testing"
	"time"

	"nusashell/domain"
)

func TestConversationMessagingListAndSearch(t *testing.T) {
	now := time.Now()
	c1 := &domain.Conversation{ID: "conv_1", Title: "Backend Project", Summary: "Refactoring auth middleware", UpdatedAt: now.Add(-10 * time.Minute)}
	c2 := &domain.Conversation{ID: "conv_2", Title: "Frontend UI", Summary: "Building user profile page", UpdatedAt: now.Add(-5 * time.Minute)}
	c3 := &domain.Conversation{ID: "conv_3", Title: "Database Migration", Summary: "Postgres schema updates", UpdatedAt: now}
	hidden := &domain.Conversation{ID: "conv_pipe", Title: "[pipeline] step", Origin: domain.ConversationOriginPipeline, UpdatedAt: now}

	store := &fakeConvStore{
		convs: map[string]*domain.Conversation{
			"conv_1":    c1,
			"conv_2":    c2,
			"conv_3":    c3,
			"conv_pipe": hidden,
		},
	}
	app := &App{Conversations: store, Bus: NewBus(), Logs: &fakeLogStore{}}

	// 1. List from conv_3 (self excluded, hidden excluded, sorted newest first)
	total, items, err := app.List("conv_3", 10, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}
	if items[0].ID != "conv_2" || items[1].ID != "conv_1" {
		t.Fatalf("items order = [%s, %s], want [conv_2, conv_1] (newest first)", items[0].ID, items[1].ID)
	}

	// 2. List with pagination (limit 1, offset 0)
	total, items, err = app.List("conv_3", 1, 0)
	if err != nil {
		t.Fatalf("List pagination: %v", err)
	}
	if total != 2 || len(items) != 1 || items[0].ID != "conv_2" {
		t.Fatalf("page 1 items = %+v, want conv_2", items)
	}

	// 3. List with pagination (limit 1, offset 1)
	total, items, err = app.List("conv_3", 1, 1)
	if err != nil {
		t.Fatalf("List pagination offset: %v", err)
	}
	if total != 2 || len(items) != 1 || items[0].ID != "conv_1" {
		t.Fatalf("page 2 items = %+v, want conv_1", items)
	}

	// 4. Search by title
	total, items, err = app.Search("conv_3", "backend", 10, 0)
	if err != nil {
		t.Fatalf("Search title: %v", err)
	}
	if total != 1 || items[0].ID != "conv_1" {
		t.Fatalf("search 'backend' = %+v, want conv_1", items)
	}

	// 5. Search by summary
	total, items, err = app.Search("conv_3", "profile", 10, 0)
	if err != nil {
		t.Fatalf("Search summary: %v", err)
	}
	if total != 1 || items[0].ID != "conv_2" {
		t.Fatalf("search 'profile' = %+v, want conv_2", items)
	}
}

func TestConversationMessagingSend(t *testing.T) {
	c1 := &domain.Conversation{ID: "conv_1", Title: "Sender"}
	c2 := &domain.Conversation{ID: "conv_2", Title: "Receiver"}
	hidden := &domain.Conversation{ID: "conv_pipe", Title: "[pipeline] step", Origin: domain.ConversationOriginPipeline}

	store := &fakeConvStore{
		convs: map[string]*domain.Conversation{
			"conv_1":    c1,
			"conv_2":    c2,
			"conv_pipe": hidden,
		},
	}
	app := &App{Conversations: store, Bus: NewBus(), Logs: &fakeLogStore{}}

	// 1. Send valid message from conv_1 to conv_2
	err := app.Send("conv_1", "conv_2", "Hey bro, whutsapp?")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	gotC2, _ := store.Get("conv_2")
	if len(gotC2.PendingAnnouncements) != 1 {
		t.Fatalf("conv_2 pending announcements = %d, want 1", len(gotC2.PendingAnnouncements))
	}
	ann := gotC2.PendingAnnouncements[0]
	if ann.Type != "peer_message" {
		t.Fatalf("announcement type = %q, want peer_message", ann.Type)
	}
	wantMsg := "You received message from conversation `conv_1`, use `conversation(op=\"send\", id=\"conv_1\", content=\"...\")` to reply:\n> Hey bro, whutsapp?"
	if ann.Message != wantMsg {
		t.Fatalf("announcement message =\n%q\nwant:\n%q", ann.Message, wantMsg)
	}

	// 2. Reject send to self
	if err := app.Send("conv_1", "conv_1", "hi"); err == nil {
		t.Fatal("expected error when sending to self")
	}

	// 3. Reject send to non-existent
	if err := app.Send("conv_1", "conv_999", "hi"); err == nil {
		t.Fatal("expected error when target does not exist")
	}

	// 4. Reject send to hidden room
	if err := app.Send("conv_1", "conv_pipe", "hi"); err == nil {
		t.Fatal("expected error when target is hidden")
	}
}
