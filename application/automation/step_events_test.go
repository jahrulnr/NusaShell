package automation

import (
	"context"
	"encoding/json"
	"errors"
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
	nextID   int
	editFail bool
}

func (r *recordingMCPCaller) CallTool(ctx context.Context, serverID, toolName string, args map[string]any) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, struct {
		server, tool string
		args         map[string]any
	}{server: serverID, tool: toolName, args: args})
	if r.editFail {
		if _, ok := args["message_id"]; ok {
			return "", errors.New("edit failed")
		}
	}
	r.nextID++
	id := r.nextID
	b, _ := json.Marshal(map[string]any{"message_id": id})
	return string(b), nil
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

func TestNotifyProgressSinkOpensThenEditsSameMessageID(t *testing.T) {
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
		AgentRunID: "agent1", RunID: "run1", StepID: "s1", Round: 1,
		CallID: "call_x", ToolName: "file_read",
		Notify: notify, TriggerEvent: &ev, ConversationID: "conv1",
	}

	_ = sink.OnStepEvent(context.Background(), withEvent(base, StepKindToolCall, StepPhasePre, StepStatusRunning, "args"))
	_ = sink.OnStepEvent(context.Background(), withEvent(base, StepKindToolCall, StepPhasePost, StepStatusOK, "file contents"))

	caller.mu.Lock()
	defer caller.mu.Unlock()
	if len(caller.calls) != 2 {
		t.Fatalf("want open+edit (2 calls), got %d: %+v", len(caller.calls), caller.calls)
	}
	open := caller.calls[0]
	edit := caller.calls[1]
	if open.tool != "internal_send_progress" || open.args["event_type"] != "tool_start" {
		t.Fatalf("open call = %+v", open)
	}
	if _, has := open.args["message_id"]; has {
		t.Fatalf("open must not send message_id, got %+v", open.args)
	}
	msgID, _ := edit.args["message_id"].(string)
	if msgID != "1" {
		t.Fatalf("edit message_id = %q, want 1 (round-trip from open)", msgID)
	}
	if edit.args["event_type"] != "tool_end" || edit.args["status"] != StepStatusOK {
		t.Fatalf("edit call = %+v", edit)
	}
}

func TestNotifyProgressSinkEditFallbackOpensNewMessage(t *testing.T) {
	caller := &recordingMCPCaller{editFail: true, nextID: 10}
	bus := &recordingBus{}
	sink := NewNotifyProgressSink(caller, bus)
	notify := &domain.NotifyConfig{
		Plugin: "nusashell.telegram",
		Detail: domain.NotifyDetailTools,
		ChatID: "520213916",
	}
	base := StepLifecycleEvent{
		AgentRunID: "agent1", RunID: "run1", StepID: "s1", Round: 1,
		CallID: "call_y", ToolName: "exec",
		Notify: notify, ConversationID: "conv1",
	}
	_ = sink.OnStepEvent(context.Background(), withEvent(base, StepKindToolCall, StepPhasePre, StepStatusRunning, ""))
	// Seed stored id so post attempts an edit.
	sink.storeMessageID("agent1", "call_y", "seeded")
	_ = sink.OnStepEvent(context.Background(), withEvent(base, StepKindToolCall, StepPhasePost, StepStatusError, "boom"))

	caller.mu.Lock()
	defer caller.mu.Unlock()
	// open + failed edit (internal) + failed edit (admin fallback) + open fallback (internal)
	// deliverEdit: callProgress with message_id fails both tools → emitFailed... wait
	// Looking at deliverEdit:
	//   callProgress with messageID → tries internal, fails → tries admin, fails → emitFailed, returns err
	//   then deliverOpen as fallback
	//
	// open (pre): 1 call (internal ok)
	// post edit: internal fail, admin fail → emitFailed, then deliverOpen: internal ok
	// So at least 1 (pre) + 2 (edit fails) + 1 (fallback open) = 4 if both tools fail on edit
	if len(caller.calls) < 3 {
		t.Fatalf("expected edit failure then fallback open, got %d calls: %+v", len(caller.calls), caller.calls)
	}
	last := caller.calls[len(caller.calls)-1]
	if _, has := last.args["message_id"]; has {
		t.Fatalf("fallback open must not carry message_id, got %+v", last.args)
	}
	if last.args["event_type"] != "tool_end" {
		t.Fatalf("fallback event_type = %v", last.args["event_type"])
	}
}

func TestNotifyProgressSinkClearsStateAtRunEnd(t *testing.T) {
	caller := &recordingMCPCaller{}
	sink := NewNotifyProgressSink(caller, &recordingBus{})
	notify := &domain.NotifyConfig{Plugin: "tg", Detail: domain.NotifyDetailTools, ChatID: "1"}
	base := StepLifecycleEvent{
		AgentRunID: "agent1", RunID: "run1", StepID: "s1",
		CallID: "c1", Notify: notify, ConversationID: "conv1",
	}
	_ = sink.OnStepEvent(context.Background(), withEvent(base, StepKindToolCall, StepPhasePre, StepStatusRunning, ""))
	if sink.lookupMessageID("agent1", "c1") == "" {
		t.Fatal("expected stored message_id after pre")
	}
	_ = sink.OnStepEvent(context.Background(), StepLifecycleEvent{
		Kind: StepKindStep, Phase: StepPhasePost, Status: StepStatusOK,
		AgentRunID: "agent1", RunID: "run1", StepID: "s1",
		Notify: notify, ConversationID: "conv1",
	})
	if got := sink.lookupMessageID("agent1", "c1"); got != "" {
		t.Fatalf("state should clear on step post, got message_id %q", got)
	}
}

func TestNotifyProgressSinkSkipsMissingChatID(t *testing.T) {
	caller := &recordingMCPCaller{}
	bus := &recordingBus{}
	sink := NewNotifyProgressSink(caller, bus)
	ev := StepLifecycleEvent{
		Kind: StepKindStep, Phase: StepPhasePre, RunID: "r", StepID: "s",
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
		Kind: StepKindToolCall, Phase: StepPhasePost, ConversationID: "conv1", Round: 2,
		CallID: "c9", ToolName: "exec", Status: StepStatusOK, Detail: "done",
	})
	select {
	case ev := <-got:
		if ev.RunID != "run1" || ev.JobID != "job1" || ev.Notify == nil || ev.Notify.Plugin != "tg" {
			t.Fatalf("enriched event = %+v", ev)
		}
		if ev.CallID != "c9" || ev.Kind != StepKindToolCall {
			t.Fatalf("kind/call not forwarded: %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for sink event")
	}
}

func TestParseProgressMessageID(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`{"message_id":"42"}`, "42"},
		{`{"message_id":99}`, "99"},
		{"ok", ""},
		{"", ""},
		{"plain-id", "plain-id"},
	}
	for _, tc := range cases {
		if got := parseProgressMessageID(tc.in); got != tc.want {
			t.Fatalf("parseProgressMessageID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

type stepSinkFunc func(ctx context.Context, ev StepLifecycleEvent) error

func (f stepSinkFunc) OnStepEvent(ctx context.Context, ev StepLifecycleEvent) error {
	return f(ctx, ev)
}

func withEvent(base StepLifecycleEvent, kind, phase, status, detail string) StepLifecycleEvent {
	base.Kind = kind
	base.Phase = phase
	base.Status = status
	base.Detail = detail
	return base
}

func TestNotifyDetailFilterStillHonored(t *testing.T) {
	caller := &recordingMCPCaller{}
	sink := NewNotifyProgressSink(caller, &recordingBus{})
	base := StepLifecycleEvent{
		AgentRunID: "a", RunID: "r", StepID: "s", CallID: "c",
		Notify: &domain.NotifyConfig{Plugin: "tg", Detail: domain.NotifyDetailNone, ChatID: "1"},
	}
	_ = sink.OnStepEvent(context.Background(), withEvent(base, StepKindToolCall, StepPhasePre, StepStatusRunning, "x"))
	caller.mu.Lock()
	n := len(caller.calls)
	caller.mu.Unlock()
	if n != 0 {
		t.Fatalf("detail=none should skip tool events, got %d", n)
	}
}
