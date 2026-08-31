package application

import (
	"errors"
	"testing"

	"nusashell/domain"
)

func TestNewConversationStartsEmpty(t *testing.T) {
	repo := NewConversation(&fakeConvStore{}, "Planning")
	if repo.ID() == "" {
		t.Fatal("NewConversation must assign an id")
	}
	if got := repo.GetAll(); len(got) != 0 {
		t.Fatalf("GetAll() = %d messages, want 0", len(got))
	}
}

func TestNewConversationEmptyTitleIsUntitled(t *testing.T) {
	store := &fakeConvStore{}
	repo := NewConversation(store, "  ")
	if err := repo.Save(); err != nil {
		t.Fatal(err)
	}
	c, err := store.Get(repo.ID())
	if err != nil {
		t.Fatal(err)
	}
	if c.Title != "Untitled" {
		t.Fatalf("title = %q, want Untitled", c.Title)
	}
}

func TestAddAppendsByRole(t *testing.T) {
	repo := NewConversation(&fakeConvStore{}, "Chat")
	if err := repo.Add(domain.RoleUser, "hello"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Add(domain.RoleAssistant, "hi"); err != nil {
		t.Fatal(err)
	}
	got := repo.GetAll()
	if len(got) != 2 {
		t.Fatalf("GetAll() = %d, want 2", len(got))
	}
	if got[0].Role != domain.RoleUser || got[0].Content != "hello" {
		t.Fatalf("first message = %+v", got[0])
	}
	if got[1].Role != domain.RoleAssistant || got[1].Content != "hi" {
		t.Fatalf("second message = %+v", got[1])
	}
	if got[0].ID == "" || got[1].ID == "" || got[0].ID == got[1].ID {
		t.Fatalf("messages need distinct ids, got %q %q", got[0].ID, got[1].ID)
	}
}

func TestAddAcceptsAttachments(t *testing.T) {
	repo := NewConversation(&fakeConvStore{}, "Chat")
	att := domain.Attachment{Type: "text", Name: "note.md", Content: "# hi"}
	if err := repo.Add(domain.RoleUser, "see file", att); err != nil {
		t.Fatal(err)
	}
	got := repo.GetAll()
	if len(got) != 1 || len(got[0].Attachments) != 1 || got[0].Attachments[0].Name != "note.md" {
		t.Fatalf("attachments = %+v", got)
	}
}

func TestGetFromReturnsHalfOpenRange(t *testing.T) {
	repo := NewConversation(&fakeConvStore{}, "Chat")
	for _, text := range []string{"a", "b", "c", "d"} {
		if err := repo.Add(domain.RoleUser, text); err != nil {
			t.Fatal(err)
		}
	}
	got := repo.GetFrom(1, 3)
	if len(got) != 2 || got[0].Content != "b" || got[1].Content != "c" {
		t.Fatalf("GetFrom(1,3) = %+v", got)
	}
	if len(repo.GetFrom(0, 0)) != 0 {
		t.Fatal("GetFrom(0,0) should be empty")
	}
	clamped := repo.GetFrom(-2, 99)
	if len(clamped) != 4 {
		t.Fatalf("GetFrom clamped = %d, want 4", len(clamped))
	}
}

func TestGetAllReturnsCopy(t *testing.T) {
	repo := NewConversation(&fakeConvStore{}, "Chat")
	if err := repo.Add(domain.RoleUser, "hello"); err != nil {
		t.Fatal(err)
	}
	got := repo.GetAll()
	got[0].Content = "mutated"
	if repo.GetAll()[0].Content != "hello" {
		t.Fatal("GetAll must return a copy")
	}
}

func TestSaveThenGetByIdLoadsMessages(t *testing.T) {
	store := &fakeConvStore{}
	repo := NewConversation(store, "Chat")
	if err := repo.Add(domain.RoleUser, "hello"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(); err != nil {
		t.Fatal(err)
	}

	loaded := NewConversation(store, "ignored")
	if err := loaded.GetById(repo.ID()); err != nil {
		t.Fatal(err)
	}
	got := loaded.GetAll()
	if len(got) != 1 || got[0].Content != "hello" {
		t.Fatalf("loaded GetAll() = %+v", got)
	}
	if loaded.ID() != repo.ID() {
		t.Fatalf("ID() = %q, want %q", loaded.ID(), repo.ID())
	}
}

func TestGetByIdMissingReturnsError(t *testing.T) {
	repo := NewConversation(&fakeConvStore{}, "Chat")
	if err := repo.GetById("conv_missing"); err == nil {
		t.Fatal("GetById missing must error")
	}
}

func TestSaveAllowsAppend(t *testing.T) {
	store := &fakeConvStore{}
	repo := NewConversation(store, "Chat")
	if err := repo.Add(domain.RoleUser, "hello"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(); err != nil {
		t.Fatal(err)
	}
	if err := repo.Add(domain.RoleAssistant, "hi"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(); err != nil {
		t.Fatal(err)
	}
	c, err := store.Get(repo.ID())
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Messages) != 2 {
		t.Fatalf("persisted messages = %d, want 2", len(c.Messages))
	}
}

func TestSaveAllowsUpdatingExistingMessageBody(t *testing.T) {
	store := &fakeConvStore{}
	repo := NewConversation(store, "Chat")
	if err := repo.Add(domain.RoleAssistant, domain.Message{ID: "a1", Status: ""}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(); err != nil {
		t.Fatal(err)
	}
	repo.inner.Messages[0].Content = "streaming"
	if err := repo.Save(); err != nil {
		t.Fatal(err)
	}
	c, err := store.Get(repo.ID())
	if err != nil {
		t.Fatal(err)
	}
	if c.Messages[0].Content != "streaming" {
		t.Fatalf("content = %q, want streaming", c.Messages[0].Content)
	}
}

func TestSaveRejectsInsertInMiddle(t *testing.T) {
	store := &fakeConvStore{}
	repo := NewConversation(store, "Chat")
	if err := repo.Add(domain.RoleUser, domain.Message{ID: "u1", Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Add(domain.RoleAssistant, domain.Message{ID: "a1", Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(); err != nil {
		t.Fatal(err)
	}
	repo.inner.Messages = append(repo.inner.Messages[:1], append([]domain.Message{{ID: "hyd", Role: domain.RoleAssistant}}, repo.inner.Messages[1:]...)...)
	err := repo.Save()
	if !errors.Is(err, ErrConversationImmutable) {
		t.Fatalf("Save() err = %v, want ErrConversationImmutable", err)
	}
}

func TestResetTranscriptStartsNewEpoch(t *testing.T) {
	store := &fakeConvStore{}
	repo := NewConversation(store, "Chat")
	if err := repo.Add(domain.RoleUser, "old"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(); err != nil {
		t.Fatal(err)
	}
	id := repo.ID()
	repo.ResetTranscript()
	if err := repo.Add(domain.RoleUser, domain.Message{ID: "handover", Content: "[COMPACTION CHECKPOINT]\nsum"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(); err != nil {
		t.Fatal(err)
	}
	if repo.ID() != id {
		t.Fatalf("ID() = %q, want %q (compaction keeps the room id)", repo.ID(), id)
	}
	got := repo.GetAll()
	if len(got) != 1 || got[0].ID != "handover" {
		t.Fatalf("epoch messages = %+v, want handover only", got)
	}
}

func TestSaveRejectsDeletingMessages(t *testing.T) {
	store := &fakeConvStore{}
	repo := NewConversation(store, "Chat")
	if err := repo.Add(domain.RoleUser, "hello"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Add(domain.RoleAssistant, "hi"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(); err != nil {
		t.Fatal(err)
	}
	repo.inner.Messages = repo.inner.Messages[:1]
	err := repo.Save()
	if !errors.Is(err, ErrConversationImmutable) {
		t.Fatalf("Save() err = %v, want ErrConversationImmutable", err)
	}
}
