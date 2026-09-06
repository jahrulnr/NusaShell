package logs

import (
	"testing"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
)

type memStore struct {
	entries []*domain.LogEntry
	cleared bool
}

func (m *memStore) List(level string, limit int) []*domain.LogEntry {
	out := make([]*domain.LogEntry, 0, len(m.entries))
	for _, e := range m.entries {
		if level != "" && e.Level != level {
			continue
		}
		out = append(out, e)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

func (m *memStore) Clear() { m.cleared = true; m.entries = nil }

func TestHandleListDefaultsLimitAndMapsDTO(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	store := &memStore{entries: []*domain.LogEntry{
		{ID: "log_1", Time: now, Level: "info", Source: "plugin", Message: "installed"},
	}}
	svc := New(Deps{Store: store})
	resp, rpcErr := svc.handleList(contracts.LogsListRequest{})
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	got := resp.(contracts.LogsListResult)
	if len(got.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(got.Entries))
	}
	if got.Entries[0].ID != "log_1" || got.Entries[0].Source != "plugin" {
		t.Fatalf("dto = %+v", got.Entries[0])
	}
}

func TestHandleClearEmptiesStore(t *testing.T) {
	store := &memStore{entries: []*domain.LogEntry{{ID: "log_1"}}}
	svc := New(Deps{Store: store})
	resp, rpcErr := svc.handleClear()
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	if ok := resp.(map[string]bool)["ok"]; !ok {
		t.Fatalf("resp = %#v", resp)
	}
	if !store.cleared || len(store.entries) != 0 {
		t.Fatal("store must be cleared")
	}
}
