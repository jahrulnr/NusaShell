// Package toolplan provides the update_plan tool: the model's running
// checklist for a multi-step task. A call carries the whole current
// plan; the tool validates it and emits it as the turn's plan event so
// the owner sees the live card, then answers with a one-line receipt
// for the transcript.
//
// Turn events belong to the turn's own sink, which a tool never sees —
// the Execute signature offers no TurnEventHandler and the room worker's
// context carries no per-turn values. The Dispatcher is the bridge, the
// same role SessionStore plays for exec sessions: delivery binds the
// running turn's sink by conversation id, and the tool resolves its
// room through ConversationID(ctx) exactly as the terminal tools do.
// A call with no bound turn (no live sink) still records its output —
// the event is a live render, not the plan's persistence.
package toolplan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// ToolName is the catalog name the prompt points the model at.
const ToolName = "update_plan"

// Dispatcher routes one room's plan events to the sink its running turn
// bound. Overlapping runTurn wrappers for one room can race a Bind: the
// newest binding wins the route, and each unbind detaches only its own
// binding — a turn finishing late can never detach a successor's.
type Dispatcher struct {
	mu    sync.Mutex
	sinks map[string]*binding
}

type binding struct {
	sink event.TurnEventHandler
}

// NewDispatcher returns an empty registry.
func NewDispatcher() *Dispatcher {
	return &Dispatcher{sinks: map[string]*binding{}}
}

// Bind registers the room's turn sink until the returned unbind runs.
// nil sinks are rejected — emitting to nothing is silently dropping.
func (d *Dispatcher) Bind(convID string, sink event.TurnEventHandler) (unbind func()) {
	if convID == "" || sink == nil {
		return func() {}
	}
	b := &binding{sink: sink}
	d.mu.Lock()
	d.sinks[convID] = b
	d.mu.Unlock()
	return func() {
		d.mu.Lock()
		if d.sinks[convID] == b {
			delete(d.sinks, convID)
		}
		d.mu.Unlock()
	}
}

// emit delivers one plan event to the room's bound sink, if any. It runs
// on the tool's Execute — the worker goroutine — so it is already
// serialized with that turn's own emissions.
func (d *Dispatcher) emit(convID string, entries string) {
	d.mu.Lock()
	b := d.sinks[convID]
	d.mu.Unlock()
	if b != nil {
		b.sink(event.NewPlanEvent(entries))
	}
}

// PlanTool is the model-callable plan contract.
type PlanTool struct {
	schema     valueobject.ToolSchema
	dispatcher *Dispatcher
}

func NewUpdatePlanTool(d *Dispatcher) (dservice.Tool, error) {
	if d == nil {
		return nil, errors.New("update_plan needs a dispatcher")
	}
	schema, err := valueobject.NewToolSchema(
		ToolName,
		"Record the plan for a task that takes several steps: the whole list, each step with its status. One call replaces the previous plan. Update it as steps start and finish, keeping at most one step in_progress.",
		json.RawMessage(`{"type":"object","properties":{
"explanation":{"type":"string","description":"What changed about the plan and why, in one line; omit when nothing needs explaining"},
"steps":{"type":"array","minItems":1,"description":"The whole current plan, in order","items":{"type":"object","properties":{
"step":{"type":"string","description":"One step of the work"},
"status":{"type":"string","enum":["pending","in_progress","completed"],"description":"pending, in_progress, or completed"}},"required":["step","status"],"additionalProperties":false}}},
"required":["steps"]}`),
		true,
		enum.ToolPermissionAgent,
	)
	if err != nil {
		return nil, err
	}
	return &PlanTool{schema: schema, dispatcher: d}, nil
}

func (tool *PlanTool) Schema() valueobject.ToolSchema {
	return tool.schema
}

func (tool *PlanTool) Execute(ctx context.Context, call entity.ToolCall, _ dservice.ToolEmitter) (dservice.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return dservice.ToolResult{}, err
	}
	var in struct {
		Explanation string `json:"explanation"`
		Steps       []struct {
			Step   string `json:"step"`
			Status string `json:"status"`
		} `json:"steps"`
	}
	if err := json.Unmarshal([]byte(call.Input()), &in); err != nil {
		return dservice.ToolResult{}, fmt.Errorf("invalid update_plan input: %w", err)
	}
	if len(in.Steps) == 0 {
		return dservice.ToolResult{}, errors.New("steps must hold at least one step")
	}
	// The plan event rides the ACP entries shape — content + status —
	// so the owner's card renders builtin and agent plans alike.
	type entry struct {
		Content string `json:"content"`
		Status  string `json:"status"`
	}
	entries := make([]entry, len(in.Steps))
	inProgress := 0
	for i, s := range in.Steps {
		step := strings.TrimSpace(s.Step)
		if step == "" {
			return dservice.ToolResult{}, fmt.Errorf("steps[%d].step is empty", i)
		}
		switch s.Status {
		case "pending", "in_progress", "completed":
		default:
			return dservice.ToolResult{}, fmt.Errorf("steps[%d].status %q is not pending, in_progress, or completed", i, s.Status)
		}
		if s.Status == "in_progress" {
			inProgress++
		}
		entries[i] = entry{Content: step, Status: s.Status}
	}
	raw, err := json.Marshal(entries)
	if err != nil {
		return dservice.ToolResult{}, err
	}
	tool.dispatcher.emit(dservice.ConversationID(ctx), string(raw))
	noun := "steps"
	if len(entries) == 1 {
		noun = "step"
	}
	return dservice.ToolResult{Text: fmt.Sprintf("plan updated: %d %s (%d in progress)", len(entries), noun, inProgress)}, nil
}
