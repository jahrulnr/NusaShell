package automation

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"nusashell/domain"
)

type recordingMCPCaller struct {
	mu    sync.Mutex
	calls []struct {
		server, tool string
		args         map[string]any
	}
}

func (r *recordingMCPCaller) CallTool(ctx context.Context, serverID, toolName string, args map[string]any) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, struct {
		server, tool string
		args         map[string]any
	}{server: serverID, tool: toolName, args: args})
	return "ok", nil
}

type recordingBus struct {
	mu     sync.Mutex
	events []string
}

func (b *recordingBus) Emit(typ string, v any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, typ)
}

func TestNotifyProgressSinkThrottlesOneCallPerRound(t *testing.T) {
	caller := &recordingMCPCaller{}
	bus := &recordingBus{}
	sink := NewNotifyProgressSink(caller, bus)
	notify := &domain.NotifyConfig{
		Plugin: "nusashell.telegram",
		Detail: domain.NotifyDetailTools,
		ChatID: "${event.chat_id}",
	}
	ev := domain.Event{Attributes: map[string]any{"chat_id": "520213916"}}
	base := StepLifecycleEvent{
		RunID: "run1", StepID: "s1", Round: 1,
		Notify: notify, TriggerEvent: &ev, ConversationID: "conv1",
	}

	_ = sink.OnStepEvent(context.Background(), withType(base, StepEventToolStart, "file_read", "", "args"))
	_ = sink.OnStepEvent(context.Background(), withType(base, StepEventReasoning, "", "", "thinking hard"))
	_ = sink.OnStepEvent(context.Background(), withType(base, StepEventText, "", "", "partial"))
	_ = sink.OnStepEvent(context.Background(), withType(base, StepEventToolEnd, "file_read", "ok", "file contents"))

	caller.mu.Lock()
	defer caller.mu.Unlock()
	if len(caller.calls) != 1 {
		t.Fatalf("want 1 MCP call per round, got %d: %+v", len(caller.calls), caller.calls)
	}
	c := caller.calls[0]
	if c.server != "plugin:nusashell.telegram" || c.tool != "internal_send_progress" {
		t.Fatalf("call = %+v", c)
	}
	detail, _ := c.args["detail"].(string)
	if !strings.Contains(detail, "thinking") || !strings.Contains(detail, "file contents") {
		t.Fatalf("detail should batch reasoning+output, got %q", detail)
	}
	if got, _ := c.args["chat_id"].(string); got != "520213916" {
		t.Fatalf("chat_id = %q", got)
	}
}

func TestNotifyProgressSinkSkipsMissingChatID(t *testing.T) {
	caller := &recordingMCPCaller{}
	bus := &recordingBus{}
	sink := NewNotifyProgressSink(caller, bus)
	ev := StepLifecycleEvent{
		Type: StepEventStart, RunID: "r", StepID: "s",
		Notify: &domain.NotifyConfig{Plugin: "nusashell.telegram", Detail: domain.NotifyDetailNone},
	}
	_ = sink.OnStepEvent(context.Background(), ev)
	caller.mu.Lock()
	n := len(caller.calls)
	caller.mu.Unlock()
	if n != 0 {
		t.Fatalf("expected no call, got %d", n)
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	if len(bus.events) != 1 || bus.events[0] != "automation.notify.skipped" {
		t.Fatalf("events = %v", bus.events)
	}
}

func TestForwardAgentLifecycleAsyncToSink(t *testing.T) {
	es := NewExecutionScheduler()
	got := make(chan StepLifecycleEvent, 1)
	es.AddStepEventSink(stepSinkFunc(func(ctx context.Context, ev StepLifecycleEvent) error {
		got <- ev
		return nil
	}))
	run := &domain.WorkflowRun{
		TaskState:  domain.TaskState[domain.RunStatus]{ID: "run1"},
		WorkflowID: "wf1",
		Definition: domain.WorkflowDefinition{
			Notify: &domain.NotifyConfig{Plugin: "tg", Detail: domain.NotifyDetailTools, ChatID: "1"},
		},
		Event: &domain.Event{Attributes: map[string]any{"chat_id": "1"}},
	}
	es.bindActiveAgentStep("conv1", run, "job1", "step1")
	es.ForwardAgentLifecycle(StepLifecycleEvent{
		Type: StepEventToolEnd, ConversationID: "conv1", Round: 2,
		ToolName: "exec", Status: "ok", Detail: "done",
	})
	select {
	case ev := <-got:
		if ev.RunID != "run1" || ev.JobID != "job1" || ev.Notify == nil || ev.Notify.Plugin != "tg" {
			t.Fatalf("enriched event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for sink event")
	}
}

type stepSinkFunc func(ctx context.Context, ev StepLifecycleEvent) error

func (f stepSinkFunc) OnStepEvent(ctx context.Context, ev StepLifecycleEvent) error {
	return f(ctx, ev)
}

func withType(base StepLifecycleEvent, typ, tool, status, detail string) StepLifecycleEvent {
	base.Type = typ
	base.ToolName = tool
	base.Status = status
	base.Detail = detail
	return base
}
