package application

import (
	"context"
	"encoding/json"
	"testing"

	"nusashell/contracts"
	"nusashell/domain"
)

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
