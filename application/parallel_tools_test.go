package application

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"nusashell/domain"
)

// barrierToolbox blocks each Execute until `want` calls are in flight at once,
// then releases them all. If tools ran serially, the barrier would never be
// reached and executeTurnTools would hang (the test guards this with a timeout).
type barrierToolbox struct {
	want      int
	mu        sync.Mutex
	active    int
	maxActive int
	gate      chan struct{}
}

func (b *barrierToolbox) ListTools() []ToolInfo { return nil }

func (b *barrierToolbox) Execute(ctx context.Context, name string, argsJSON []byte) (string, error) {
	b.mu.Lock()
	b.active++
	if b.active > b.maxActive {
		b.maxActive = b.active
	}
	reached := b.active >= b.want
	b.mu.Unlock()
	if reached {
		select {
		case <-b.gate:
		default:
			close(b.gate)
		}
	}
	<-b.gate // wait until enough calls are concurrent
	b.mu.Lock()
	b.active--
	b.mu.Unlock()
	return "ok:" + name, nil
}

func newBarrierApp(t *testing.T, toolCalls []domain.ToolCall, box ToolExecutor) (*App, *domain.Conversation, *TurnRun) {
	t.Helper()
	conv := &domain.Conversation{
		ID:       "c1",
		Messages: []domain.Message{{ID: "m1", ToolCalls: toolCalls}},
	}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}},
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		Toolbox:       box,
	}
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: context.Background(), Cancel: func() {}}
	return app, conv, run
}

// The tool calls of one round must run concurrently (bounded), not one-by-one.
func TestExecuteTurnToolsRunsConcurrently(t *testing.T) {
	want := 3
	box := &barrierToolbox{want: want, gate: make(chan struct{})}
	toolCalls := []domain.ToolCall{{ID: "t1", Name: "a"}, {ID: "t2", Name: "b"}, {ID: "t3", Name: "c"}}
	app, conv, run := newBarrierApp(t, toolCalls, box)

	done := make(chan error, 1)
	go func() {
		err := app.executeTurnTools(run, "m1", toolCalls, ModelCapabilities{Vision: true}, domain.Settings{}, 1)
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("executeTurnTools: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("executeTurnTools did not complete — tools appear to run serially, not in parallel")
	}

	if box.maxActive < want {
		t.Fatalf("max concurrent tools = %d, want >= %d (parallel execution)", box.maxActive, want)
	}
	for i, tc := range conv.Messages[0].ToolCalls {
		if tc.Status != domain.ToolOK {
			t.Fatalf("tool %d status = %v, want ok", i, tc.Status)
		}
	}
}

// TestExecuteTurnToolsRespectsMaxParallelTools: when settings.MaxParallelTools
// is set below the number of tool calls, the semaphore bounds concurrency to
// that value (maxActive never exceeds it). This proves the cap is configurable
// and not a hardcoded constant.
func TestExecuteTurnToolsRespectsMaxParallelTools(t *testing.T) {
	cap := 2
	box := &barrierToolbox{want: cap, gate: make(chan struct{})}
	toolCalls := []domain.ToolCall{
		{ID: "t1", Name: "a"}, {ID: "t2", Name: "b"},
		{ID: "t3", Name: "c"}, {ID: "t4", Name: "d"},
	}
	app, _, run := newBarrierApp(t, toolCalls, box)
	settings := domain.Settings{MaxParallelTools: cap}

	done := make(chan error, 1)
	go func() {
		err := app.executeTurnTools(run, "m1", toolCalls, ModelCapabilities{Vision: true}, settings, 1)
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("executeTurnTools: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("executeTurnTools did not complete")
	}

	if box.maxActive > cap {
		t.Fatalf("max concurrent tools = %d, want <= %d (cap not respected)", box.maxActive, cap)
	}
	if box.maxActive < cap {
		t.Fatalf("max concurrent tools = %d, want exactly %d (cap should allow this many)", box.maxActive, cap)
	}
}

// orderedToolbox returns the tool name as output so we can assert results are
// persisted in tool-call order regardless of completion order.
type orderedToolbox struct{}

func (orderedToolbox) ListTools() []ToolInfo { return nil }
func (orderedToolbox) Execute(ctx context.Context, name string, argsJSON []byte) (string, error) {
	// Make later tools finish first to shuffle completion order.
	switch name {
	case "first":
		time.Sleep(30 * time.Millisecond)
	case "second":
		time.Sleep(15 * time.Millisecond)
	}
	return "out:" + name, nil
}

// Even when tools finish out of order, results are persisted in tool-call order.
func TestExecuteTurnToolsPersistsResultsInOrder(t *testing.T) {
	toolCalls := []domain.ToolCall{
		{ID: "t1", Name: "first"},
		{ID: "t2", Name: "second"},
		{ID: "t3", Name: "third"},
	}
	app, conv, run := newBarrierApp(t, toolCalls, orderedToolbox{})

	if err := app.executeTurnTools(run, "m1", toolCalls, ModelCapabilities{Vision: true}, domain.Settings{}, 1); err != nil {
		t.Fatalf("executeTurnTools: %v", err)
	}
	got := conv.Messages[0].ToolCalls
	for i, name := range []string{"first", "second", "third"} {
		want := fmt.Sprintf("out:%s", name)
		if got[i].ID != toolCalls[i].ID || got[i].Output != want {
			t.Fatalf("tool %d = {id:%s out:%q}, want {id:%s out:%q}", i, got[i].ID, got[i].Output, toolCalls[i].ID, want)
		}
	}
}
