package toolexec

import (
	"strings"
	"sync"
	"testing"
	"time"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

// exits collects the completions the store reports.
type exits struct {
	mu   sync.Mutex
	got  []dservice.Completion
	seen chan struct{}
}

func (e *exits) sink(convID string, c dservice.Completion) {
	e.mu.Lock()
	e.got = append(e.got, c)
	e.mu.Unlock()
	e.seen <- struct{}{}
}

func watchExits(t *testing.T) (*SessionStore, *exits) {
	t.Helper()
	store := NewSessionStore(nil)
	e := &exits{seen: make(chan struct{}, 8)}
	store.OnExit(e.sink)
	return store, e
}

func (e *exits) wait(t *testing.T, what string) dservice.Completion {
	t.Helper()
	select {
	case <-e.seen:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s was never reported", what)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.got[len(e.got)-1]
}

// An opted-in session that exits cleanly reports its exit as data: the session, the
// status, and the tail a model reads.
func TestOptedInSessionReportsACleanExit(t *testing.T) {
	store, e := watchExits(t)
	defer store.TerminateConversation("c_ok")
	if _, err := store.Open(t.Context(), "c_ok", dservice.TerminalOpenRequest{Command: "echo all-done", Wake: true}); err != nil {
		t.Fatal(err)
	}
	c := e.wait(t, "the clean exit")
	if c.Source != "session" || c.ID == "" || c.Status != "exited" || !strings.Contains(c.Detail, "exit 0") {
		t.Fatalf("completion = %+v", c)
	}
	if !strings.Contains(c.Detail, "all-done") {
		t.Fatalf("detail lost the tail: %q", c.Detail)
	}
}

// A non-zero exit reports as failed — the record says how it ended, never that it
// succeeded.
func TestOptedInSessionReportsAFailedExit(t *testing.T) {
	store, e := watchExits(t)
	defer store.TerminateConversation("c_bad")
	if _, err := store.Open(t.Context(), "c_bad", dservice.TerminalOpenRequest{Command: "exit 3", Wake: true}); err != nil {
		t.Fatal(err)
	}
	c := e.wait(t, "the failed exit")
	if c.Status != "failed" || !strings.Contains(c.Detail, "exit 3") {
		t.Fatalf("completion = %+v, want failed with exit 3", c)
	}
}

// Default off: a session that did not ask reports nothing at all.
func TestUnoptedSessionReportsNothing(t *testing.T) {
	store, e := watchExits(t)
	defer store.TerminateConversation("c_quiet")
	if _, err := store.Open(t.Context(), "c_quiet", dservice.TerminalOpenRequest{Command: "true"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-e.seen:
		t.Fatalf("a session that did not ask reported: %+v", e.got)
	case <-time.After(400 * time.Millisecond):
	}
}

// A room reaped while its session ran leaves no orphan record.
func TestReapedRoomReportsNothing(t *testing.T) {
	store, e := watchExits(t)
	if _, err := store.Open(t.Context(), "c_reap", dservice.TerminalOpenRequest{Command: "sleep 0.2", Wake: true}); err != nil {
		t.Fatal(err)
	}
	store.TerminateConversation("c_reap")
	select {
	case <-e.seen:
		t.Fatalf("a reaped room still reported: %+v", e.got)
	case <-time.After(700 * time.Millisecond):
	}
}
