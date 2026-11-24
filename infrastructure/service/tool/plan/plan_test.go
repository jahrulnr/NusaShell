package toolplan

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

// The schema must stay agent-callable — the prompt names the tool and a
// locked or system-only schema silently demotes plan discipline to prose.
func TestUpdatePlanSchemaIsAgentCallable(t *testing.T) {
	tool, err := NewUpdatePlanTool(NewDispatcher())
	if err != nil {
		t.Fatal(err)
	}
	s := tool.Schema()
	if s.Name() != ToolName {
		t.Fatalf("schema name = %q, want %s", s.Name(), ToolName)
	}
	if s.Permission() != enum.ToolPermissionAgent {
		t.Fatalf("permission = %v, want agent", s.Permission())
	}
	if !s.CallableByAgent() || !s.Enabled() {
		t.Fatal("update_plan must be enabled and agent-callable")
	}
	var params map[string]any
	if err := json.Unmarshal(s.Parameters(), &params); err != nil {
		t.Fatalf("parameters not valid JSON: %v", err)
	}
	if params["type"] != "object" {
		t.Fatalf("parameters type = %v, want object", params["type"])
	}
}

// A valid call emits the room's plan event in the ACP entries shape and
// answers with the step counts for the transcript.
func TestUpdatePlanEmitsEventAndReceipt(t *testing.T) {
	d := NewDispatcher()
	tool, err := NewUpdatePlanTool(d)
	if err != nil {
		t.Fatal(err)
	}
	var got []event.TurnEvent
	unbind := d.Bind("room-1", func(ev event.TurnEvent) { got = append(got, ev) })
	defer unbind()

	call, _ := entity.NewToolCall("p1", ToolName, `{"steps":[
{"step":"read the wiring","status":"completed"},
{"step":"add the tool","status":"in_progress"},
{"step":"run tests","status":"pending"}]}`, enum.TooltypeOrigin)
	res, err := tool.Execute(dservice.WithConversationID(context.Background(), "room-1"), call, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "plan updated: 3 steps (1 in progress)" {
		t.Fatalf("output = %q", res.Text)
	}
	if len(got) != 1 || got[0].Kind() != enum.TurnEventPlan {
		t.Fatalf("events = %v, want one plan event", got)
	}
	var entries []map[string]string
	if err := json.Unmarshal([]byte(got[0].Delta()), &entries); err != nil {
		t.Fatalf("plan entries not JSON: %v", err)
	}
	if len(entries) != 3 || entries[1]["content"] != "add the tool" || entries[1]["status"] != "in_progress" {
		t.Fatalf("entries = %v", entries)
	}
}

// Malformed calls fail before anything is emitted — a half-valid plan
// must never reach the owner's card.
func TestUpdatePlanRejectsBadInput(t *testing.T) {
	d := NewDispatcher()
	tool, err := NewUpdatePlanTool(d)
	if err != nil {
		t.Fatal(err)
	}
	got := 0
	unbind := d.Bind("room-1", func(event.TurnEvent) { got++ })
	defer unbind()
	ctx := dservice.WithConversationID(context.Background(), "room-1")
	for name, input := range map[string]string{
		"not json":       `{"steps":`,
		"empty steps":    `{"steps":[]}`,
		"missing steps":  `{"explanation":"none"}`,
		"empty step":     `{"steps":[{"step":"  ","status":"pending"}]}`,
		"unknown status": `{"steps":[{"step":"x","status":"done"}]}`,
	} {
		call, err := entity.NewToolCall("p1", ToolName, input, enum.TooltypeOrigin)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := tool.Execute(ctx, call, nil); err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}
	if got != 0 {
		t.Fatalf("invalid calls emitted %d events", got)
	}
}

// With no bound turn — an unregistered room — the call still records its
// output; the event is a live render, not the plan's persistence.
func TestUpdatePlanWithoutBoundSinkStillAnswers(t *testing.T) {
	tool, err := NewUpdatePlanTool(NewDispatcher())
	if err != nil {
		t.Fatal(err)
	}
	call, _ := entity.NewToolCall("p1", ToolName, `{"steps":[{"step":"only step","status":"completed"}]}`, enum.TooltypeOrigin)
	res, err := tool.Execute(dservice.WithConversationID(context.Background(), "room-none"), call, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "plan updated: 1 step (0 in progress)" {
		t.Fatalf("output = %q", res.Text)
	}
}

// Unbind detaches only its own binding: overlapping turns for one room
// leave the newer binding attached when the older turn finishes.
func TestDispatcherBindUnbind(t *testing.T) {
	d := NewDispatcher()
	first := 0
	unbind := d.Bind("room-1", func(event.TurnEvent) { first++ })
	d.emit("room-1", "[]")
	unbind()
	d.emit("room-1", "[]")
	if first != 1 {
		t.Fatalf("emits after unbind still delivered: %d", first)
	}
	second := 0
	d.Bind("room-1", func(event.TurnEvent) { second++ })
	unbind() // stale unbind from the older turn must not detach the newer
	d.emit("room-1", "[]")
	if first != 1 || second != 1 {
		t.Fatalf("first = %d, second = %d; rebind must replace", first, second)
	}
}

// A nil dispatcher cannot be handed to the tool — wiring mistakes fail
// at construction, not as silent plan drops mid-turn.
func TestUpdatePlanRejectsNilDispatcher(t *testing.T) {
	if _, err := NewUpdatePlanTool(nil); err == nil {
		t.Fatal("nil dispatcher must fail")
	}
}
