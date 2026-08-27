package journal

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestStore_appendReadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	st := newStore(dir)
	conv := "conv_test1"
	ev := journalEvent{
		Type:    eventTypeChange,
		TS:      time.Now().UTC(),
		EventID: "tc1",
		RunID:   "run1",
		Tool:    "file_write",
		Change: &domainFileChange{
			Path:       "/tmp/a.txt",
			Kind:       "modified",
			Origin:     "agent",
			BeforeHash: "aaa",
			AfterHash:  "bbb",
		},
	}
	if err := st.append(conv, ev); err != nil {
		t.Fatal(err)
	}
	events, err := st.readAll(conv)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events want 1", len(events))
	}
	if events[0].EventID != "tc1" {
		t.Fatalf("eventId: got %q", events[0].EventID)
	}
}

func TestStore_truncatedFinalLineSkipped(t *testing.T) {
	dir := t.TempDir()
	st := newStore(dir)
	conv := "conv_trunc"
	if err := st.append(conv, journalEvent{Type: eventTypeUnobserved, TS: time.Now().UTC(), EventID: "u1", Tool: "mcp_call"}); err != nil {
		t.Fatal(err)
	}
	path, err := st.journalPath(conv)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"type":"change","ts":`)
	_ = f.Close()
	events, err := st.readAll(conv)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events want 1 (truncated line skipped)", len(events))
	}
}

func TestStore_archiveMultiMemberMerge(t *testing.T) {
	dir := t.TempDir()
	st := newStore(dir)
	conv := "conv_multi"

	// Turn 1: one event, then archive.
	if err := st.append(conv, journalEvent{Type: eventTypeChange, TS: time.Now().UTC(), EventID: "tc1", Tool: "file_write",
		Change: &domainFileChange{Path: "/w/a.txt", Kind: "added", Origin: "agent", AfterHash: "h1"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.archive(conv); err != nil {
		t.Fatal(err)
	}

	// Turn 2: another event, then archive again — must append a second
	// gzip member, not overwrite the first.
	if err := st.append(conv, journalEvent{Type: eventTypeChange, TS: time.Now().UTC(), EventID: "tc2", Tool: "file_write",
		Change: &domainFileChange{Path: "/w/b.txt", Kind: "added", Origin: "agent", AfterHash: "h2"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.archive(conv); err != nil {
		t.Fatal(err)
	}

	// Turn 3: live tail event, not yet archived.
	if err := st.append(conv, journalEvent{Type: eventTypeUnobserved, TS: time.Now().UTC(), EventID: "u3", Tool: "mcp_call"}); err != nil {
		t.Fatal(err)
	}

	events, err := st.readAll(conv)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("got %d events want 3 (gz member 1 + gz member 2 + jsonl tail)", len(events))
	}
	if events[0].EventID != "tc1" || events[1].EventID != "tc2" || events[2].EventID != "u3" {
		t.Fatalf("order wrong: %q %q %q", events[0].EventID, events[1].EventID, events[2].EventID)
	}

	// JSONL must only hold the post-archive tail (turn 3 event); archived
	// turns live in the gz.
	tail, err := readJSONLFile(filepath.Join(dir, "conversations", conv+".journal", "journal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) != 1 || tail[0].EventID != "u3" {
		t.Fatalf("jsonl tail = %+v, want only u3", tail)
	}
}

func TestStore_readAllDedupesCrashDuplicates(t *testing.T) {
	dir := t.TempDir()
	st := newStore(dir)
	conv := "conv_dupe"

	ev := journalEvent{Type: eventTypeChange, TS: time.Now().UTC(), EventID: "tc1", Tool: "file_write",
		Change: &domainFileChange{Path: "/w/a.txt", Kind: "modified", Origin: "agent", AfterHash: "h1"}}
	// Same event lands in the archive and again in the tail (crash-replay).
	if err := st.append(conv, ev); err != nil {
		t.Fatal(err)
	}
	if err := st.archive(conv); err != nil {
		t.Fatal(err)
	}
	if err := st.append(conv, ev); err != nil {
		t.Fatal(err)
	}
	// Unobserved events with the same id are NOT deduplicated.
	u := journalEvent{Type: eventTypeUnobserved, TS: time.Now().UTC(), EventID: "u1", Tool: "mcp_call"}
	if err := st.append(conv, u); err != nil {
		t.Fatal(err)
	}
	if err := st.append(conv, u); err != nil {
		t.Fatal(err)
	}

	events, err := st.readAll(conv)
	if err != nil {
		t.Fatal(err)
	}
	changes, unobs := 0, 0
	for _, e := range events {
		if e.Type == eventTypeChange {
			changes++
		} else {
			unobs++
		}
	}
	if changes != 1 {
		t.Fatalf("change events = %d, want 1 (duplicate dropped)", changes)
	}
	if unobs != 2 {
		t.Fatalf("unobserved events = %d, want 2 (never deduped)", unobs)
	}
}

func TestStore_removeDeletesSidecar(t *testing.T) {
	dir := t.TempDir()
	st := newStore(dir)
	conv := "conv_remove"
	if err := st.append(conv, journalEvent{Type: eventTypeUnobserved, TS: time.Now().UTC(), EventID: "u1", Tool: "mcp_call"}); err != nil {
		t.Fatal(err)
	}
	sidecar := filepath.Join(dir, "conversations", conv+".journal")
	if _, err := os.Stat(sidecar); err != nil {
		t.Fatalf("sidecar should exist: %v", err)
	}
	if err := st.remove(conv); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
		t.Fatal("sidecar should be removed")
	}
	// Removing again is a no-op, not an error.
	if err := st.remove(conv); err != nil {
		t.Fatal(err)
	}
	events, err := st.readAll(conv)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("events after remove = %d, want 0", len(events))
	}
}

func TestStore_concurrentAppends(t *testing.T) {
	dir := t.TempDir()
	st := newStore(dir)
	conv := "conv_race"
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_ = st.append(conv, journalEvent{
				Type:    eventTypeUnobserved,
				TS:      time.Now().UTC(),
				EventID: fmt.Sprintf("e%d", n),
				Tool:    "mcp_call",
			})
		}(i)
	}
	wg.Wait()
	events, err := st.readAll(conv)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 20 {
		t.Fatalf("got %d events want 20", len(events))
	}
}
