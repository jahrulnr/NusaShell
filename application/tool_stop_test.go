package application

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
)

// cancellableStreamToolbox models exec: it does not return until the context
// passed to the individual tool call is cancelled.
type cancellableStreamToolbox struct {
	started chan struct{}
	once    sync.Once
}

func (b *cancellableStreamToolbox) ListTools() []ToolInfo { return nil }

func (b *cancellableStreamToolbox) Execute(context.Context, string, []byte) (string, error) {
	return "", fmt.Errorf("unexpected non-streamed execution")
}

func (b *cancellableStreamToolbox) ExecuteStreamed(ctx context.Context, _ string, _ []byte, _ func(string)) (string, error) {
	b.once.Do(func() { close(b.started) })
	<-ctx.Done()
	return "", fmt.Errorf("exec cancelled: %w\npartial output:\nline-1", ctx.Err())
}

func TestHandleToolStopInterruptsOnlyTheSelectedTool(t *testing.T) {
	box := &cancellableStreamToolbox{started: make(chan struct{})}
	run := &TurnRun{ID: "run-1", ConversationID: "conv-1", Ctx: context.Background()}
	app := &App{
		Bus:     NewBus(),
		Toolbox: box,
		runs:    map[string]*TurnRun{run.ID: run},
	}

	done := make(chan toolExecResult, 1)
	go func() {
		done <- app.runOneTool(run, "msg-1", domain.ToolCall{ID: "tool-1", Name: "exec", Args: `{}`}, ModelCapabilities{}, domain.Settings{}, 1)
	}()

	select {
	case <-box.started:
	case <-time.After(time.Second):
		t.Fatal("exec did not start")
	}

	if _, rpcErr := app.handleToolStop(contracts.ToolStopRequest{RunID: run.ID, ToolCallID: "tool-1"}); rpcErr != nil {
		t.Fatalf("handleToolStop: %v", rpcErr)
	}

	select {
	case result := <-done:
		if result.status != domain.ToolInterrupted {
			t.Fatalf("tool status = %q, want %q", result.status, domain.ToolInterrupted)
		}
		if !strings.Contains(result.output, "interrupted by user") {
			t.Fatalf("tool output = %q, want an explicit user interruption marker", result.output)
		}
	case <-time.After(time.Second):
		t.Fatal("tool did not return after per-tool cancellation")
	}
	if err := run.Ctx.Err(); err != nil {
		t.Fatalf("per-tool stop cancelled the enclosing turn: %v", err)
	}
}

func TestExecuteTurnToolsContinuesAfterPerToolStop(t *testing.T) {
	box := &cancellableStreamToolbox{started: make(chan struct{})}
	conv := &domain.Conversation{
		ID:       "conv-1",
		Messages: []domain.Message{{ID: "msg-1", ToolCalls: []domain.ToolCall{{ID: "tool-1", Name: "exec", Args: `{}`}}}},
	}
	run := &TurnRun{ID: "run-1", ConversationID: conv.ID, Ctx: context.Background()}
	app := &App{
		Bus:           NewBus(),
		Toolbox:       box,
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{conv.ID: conv}},
		runs:          map[string]*TurnRun{run.ID: run},
	}

	done := make(chan error, 1)
	go func() {
		done <- app.executeTurnTools(run, "msg-1", conv.Messages[0].ToolCalls, ModelCapabilities{}, domain.Settings{}, 1)
	}()
	select {
	case <-box.started:
	case <-time.After(time.Second):
		t.Fatal("exec did not start")
	}
	if _, rpcErr := app.handleToolStop(contracts.ToolStopRequest{RunID: run.ID, ToolCallID: "tool-1"}); rpcErr != nil {
		t.Fatalf("handleToolStop: %v", rpcErr)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("executeTurnTools returned an error after per-tool stop: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("tool round did not continue after per-tool stop")
	}
	if got := conv.Messages[0].ToolCalls[0].Status; got != domain.ToolInterrupted {
		t.Fatalf("persisted tool status = %q, want %q", got, domain.ToolInterrupted)
	}
	if got := conv.Messages[0].ToolCalls[0].Output; !strings.Contains(got, "interrupted by user") {
		t.Fatalf("persisted tool output = %q, want an explicit user interruption marker", got)
	}
	if err := run.Ctx.Err(); err != nil {
		t.Fatalf("per-tool stop cancelled the enclosing turn: %v", err)
	}
}

func TestHandleToolStopRejectsUnknownToolCall(t *testing.T) {
	run := &TurnRun{ID: "run-1", Ctx: context.Background()}
	app := &App{runs: map[string]*TurnRun{run.ID: run}}
	if _, rpcErr := app.handleToolStop(contracts.ToolStopRequest{RunID: run.ID, ToolCallID: "missing"}); rpcErr == nil || rpcErr.Code != contracts.CodeNotFound {
		t.Fatalf("unknown tool stop error = %+v, want NOT_FOUND", rpcErr)
	}
}
