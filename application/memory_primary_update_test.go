package application

import (
	"context"
	"encoding/json"
	"testing"

	"nusashell/contracts"
	"nusashell/domain"
)

type primaryUpdateStore struct {
	entry   domain.PrimaryEntry
	updates int
}

func (s *primaryUpdateStore) Load() *domain.PrimaryMemory {
	return &domain.PrimaryMemory{Entries: []domain.PrimaryEntry{s.entry}}
}

func (s *primaryUpdateStore) Update(entries []domain.PrimaryEntry) error {
	s.updates++
	if len(entries) == 0 {
		s.entry = domain.PrimaryEntry{}
	} else {
		s.entry = entries[0]
	}
	return nil
}

func (s *primaryUpdateStore) Replace(string, string) error { return nil }
func (s *primaryUpdateStore) Path() string                 { return "" }

func TestMemoryPrimaryUpdateReplacesOnlyPrimaryDocument(t *testing.T) {
	primary := &primaryUpdateStore{entry: domain.PrimaryEntry{Content: "old", Source: "agent"}}
	app := &App{Primary: primary, Bus: NewBus()}

	result, rpcErr := app.Dispatch(context.Background(), contracts.MethodMemoryPrimaryUpdate, json.RawMessage(`{"content":"new profile\nwith preferences"}`))
	if rpcErr != nil {
		t.Fatalf("Dispatch primary update: %v", rpcErr)
	}
	got, ok := result.(contracts.MemoryPrimaryUpdateResult)
	if !ok {
		t.Fatalf("result type = %T, want MemoryPrimaryUpdateResult", result)
	}
	if got.Entry.Content != "new profile\nwith preferences" {
		t.Fatalf("returned content = %q", got.Entry.Content)
	}
	if primary.entry.Content != got.Entry.Content || primary.entry.Source != "user" {
		t.Fatalf("stored primary = %+v, want user-owned updated document", primary.entry)
	}
	if primary.updates != 1 {
		t.Fatalf("primary updates = %d, want 1", primary.updates)
	}
}

func TestMemoryPrimaryUpdateAllowsClearingDocument(t *testing.T) {
	primary := &primaryUpdateStore{entry: domain.PrimaryEntry{Content: "old"}}
	app := &App{Primary: primary}

	if _, rpcErr := app.handleMemoryPrimaryUpdate(contracts.MemoryPrimaryUpdateRequest{}); rpcErr != nil {
		t.Fatalf("clearing primary: %v", rpcErr)
	}
	if primary.entry.Content != "" {
		t.Fatalf("cleared primary content = %q", primary.entry.Content)
	}
}

func TestMemoryPrimaryUpdateRejectsOverCap(t *testing.T) {
	primary := &primaryUpdateStore{entry: domain.PrimaryEntry{Content: "keep"}}
	app := &App{Primary: primary}
	content := make([]byte, domain.PrimaryCharCap+1)
	for i := range content {
		content[i] = 'x'
	}

	_, rpcErr := app.handleMemoryPrimaryUpdate(contracts.MemoryPrimaryUpdateRequest{Content: string(content)})
	if rpcErr == nil || rpcErr.Code != contracts.CodeValidation {
		t.Fatalf("over-cap error = %+v, want VALIDATION_ERROR", rpcErr)
	}
	if primary.updates != 0 || primary.entry.Content != "keep" {
		t.Fatalf("over-cap update changed primary: %+v", primary.entry)
	}
}

func TestMemoryAgentUpdateReplacesOnlyAgentDocument(t *testing.T) {
	agent := &primaryUpdateStore{entry: domain.PrimaryEntry{Content: "old", Source: "user"}}
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
	agent := &primaryUpdateStore{entry: domain.PrimaryEntry{Content: "keep"}}
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
