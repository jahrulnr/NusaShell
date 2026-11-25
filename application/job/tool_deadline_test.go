package job

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/config"
	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	repoconversation "github.com/jahrulnr/nusashell/infrastructure/repository/conversation"
)

// hangingTool blocks until its ctx dies and answers the ctx error with no
// output — the shape a stuck process takes once the seam deadline fires.
type hangingTool struct{ schema valueobject.ToolSchema }

func newHangingTool(t *testing.T, name string, perm enum.ToolPermission) *hangingTool {
	t.Helper()
	schema, err := valueobject.NewToolSchema(name, "blocks forever",
		json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`),
		true, perm)
	if err != nil {
		t.Fatal(err)
	}
	return &hangingTool{schema: schema}
}

func (h *hangingTool) Schema() valueobject.ToolSchema { return h.schema }

func (h *hangingTool) Execute(ctx context.Context, _ entity.ToolCall, _ dservice.ToolEmitter) (dservice.ToolResult, error) {
	<-ctx.Done()
	return dservice.ToolResult{}, ctx.Err()
}

// hangThenAnswerSvc asks for one exec call, then answers final text once
// the call's result comes back — the second answer existing at all is the
// proof the turn continued past the timed-out tool.
type hangThenAnswerSvc struct{ calls int }

func (s *hangThenAnswerSvc) Send(_ context.Context, conv *aggregate.Conversation, _ *aggregate.Provider, _ entity.Model, _ event.TurnEventHandler) error {
	s.calls++
	m, err := entity.NewMessage(enum.RoleAssistant, "done after the timeout")
	if err != nil {
		return err
	}
	if s.calls == 1 {
		call, err := entity.NewToolCall("call_1", "exec", `{"command":"sleep 999"}`, enum.TooltypeOrigin)
		if err != nil {
			return err
		}
		if err := m.AddToolCall(call); err != nil {
			return err
		}
		m.SetFinishReason(enum.FinishReasonToolCalls)
	} else {
		m.SetFinishReason(enum.FinishReasonStop)
	}
	conv.Append(m)
	return nil
}

func shrinkDeadline(t *testing.T, d time.Duration) {
	t.Helper()
	old := toolCallDeadline
	toolCallDeadline = d
	t.Cleanup(func() { toolCallDeadline = old })
}

// The turn seam: a tool that outlives the ceiling is recorded as a
// timed-out result — the shared wording, marked failed — and the turn
// continues to its next provider step. Mutation target: dropping the
// WithTimeout at this seam leaves the parent ctx (2s below) as the only
// stopper, so the turn dies canceled instead of completing — red.
func TestToolDeadlineIsATimedOutResultNotACancel(t *testing.T) {
	shrinkDeadline(t, 150*time.Millisecond)
	d := Deps{
		SummaryTool: mustSummaryTool(t),
		Convs:       repoconversation.NewConversationRepository(t.TempDir()),
		Config:      config.ConversationConfig{MaxTurnStep: 4},
	}
	tool := newHangingTool(t, "exec", enum.ToolPermissionAgent)
	bundle := EnvironmentBundle{Tools: map[string]dservice.Tool{"exec": tool}}
	d.Environments = map[enum.Environment]EnvironmentDriver{enum.EnvironmentLocal: bundle}
	conv := seedRoom(t, d.Convs, "room", "hi")

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	svc := &hangThenAnswerSvc{}
	err := d.runTurn(ctx, "room", conv, bundle, nil, entity.Model{}, svc, nil, nil, valueobject.TurnOrigin{})
	if err != nil {
		t.Fatalf("a timed-out tool must not end the turn: %v", err)
	}
	if svc.calls < 2 {
		t.Fatalf("provider calls = %d — the turn never continued past the tool", svc.calls)
	}

	loaded, lerr := d.Convs.Load(t.Context(), "room")
	if lerr != nil {
		t.Fatal(lerr)
	}
	var found *entity.ToolCall
	for _, m := range loaded.Messages() {
		for i := range m.ToolCalls() {
			c := m.ToolCalls()[i]
			if c.ID() == "call_1" {
				found = &c
			}
		}
	}
	if found == nil {
		t.Fatal("the exec call is not in the persisted history")
	}
	if found.Output() != dservice.ToolCallTimeoutText {
		t.Fatalf("tool output = %q, want the shared %q", found.Output(), dservice.ToolCallTimeoutText)
	}
	if !found.Failed() {
		t.Fatal("a timed-out call is a failed call — the record must say so")
	}
	if last := loaded.Last(); last == nil || last.Content() != "done after the timeout" {
		t.Fatalf("last = %+v, want the answer after the timeout", last)
	}
}

// quickSystemTool answers immediately — the control proving the build
// reaches the tools after the stuck one.
func quickSystemTool(t *testing.T) dservice.Tool {
	t.Helper()
	schema, err := valueobject.NewToolSchema("quick", "answers at once",
		json.RawMessage(`{"type":"object"}`), true, enum.ToolPermissionSystem)
	if err != nil {
		t.Fatal(err)
	}
	return quickTool{schema}
}

type quickTool struct{ schema valueobject.ToolSchema }

func (q quickTool) Schema() valueobject.ToolSchema { return q.schema }

func (q quickTool) Execute(context.Context, entity.ToolCall, dservice.ToolEmitter) (dservice.ToolResult, error) {
	return dservice.ToolResult{Text: "quick answer"}, nil
}

// The hydration seam: a system tool that outlives the ceiling is recorded
// as timed out — the shared wording — and the context build continues to
// the remaining tools. Mutation target: dropping the WithTimeout at this
// seam leaves the parent ctx as the only stopper, so the build aborts
// with the ctx error instead of returning records — red.
func TestHydrationDeadlineIsATimedOutRecord(t *testing.T) {
	shrinkDeadline(t, 150*time.Millisecond)
	bundle := EnvironmentBundle{Tools: map[string]dservice.Tool{
		"stuck": newHangingTool(t, "stuck", enum.ToolPermissionSystem),
		"quick": quickSystemTool(t),
	}}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	contexts, err := bundle.ToolContexts(ctx, nil)
	if err != nil {
		t.Fatalf("a timed-out hydration tool must not abort the build: %v", err)
	}
	if len(contexts) != 2 {
		t.Fatalf("contexts = %+v, want both records — the build must continue past the timeout", contexts)
	}
	byName := map[string]ToolContext{}
	for _, c := range contexts {
		byName[c.Name] = c
	}
	if byName["stuck"].Output != dservice.ToolCallTimeoutText {
		t.Fatalf("hydration output = %q, want the shared %q", byName["stuck"].Output, dservice.ToolCallTimeoutText)
	}
	if byName["quick"].Output != "quick answer" {
		t.Fatalf("the tool after the timeout = %q, want its own answer", byName["quick"].Output)
	}
}

// slowSummary delegates to the real summary tool after a delay — the fold
// work the ceiling must never cut.
type slowSummary struct {
	inner dservice.Tool
	delay time.Duration
}

func (s slowSummary) Schema() valueobject.ToolSchema { return s.inner.Schema() }

func (s slowSummary) Execute(ctx context.Context, call entity.ToolCall, emit dservice.ToolEmitter) (dservice.ToolResult, error) {
	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
		return dservice.ToolResult{}, ctx.Err()
	}
	return s.inner.Execute(ctx, call, emit)
}

// The written exception: compaction is NOT capped. With the seam deadline
// shrunk to 50ms and a fold whose summary needs ~200ms, the fold still
// completes and the turn finishes — if anyone wraps compact.go's
// summary.Execute in the deadline, this test turns red.
func TestCompactionIsNotCapped(t *testing.T) {
	shrinkDeadline(t, 50*time.Millisecond)
	slow := slowSummary{inner: mustSummaryTool(t), delay: 200 * time.Millisecond}
	d := Deps{
		SummaryTool: slow,
		Convs:       repoconversation.NewConversationRepository(t.TempDir()),
		Config:      config.ConversationConfig{MaxTurnStep: 64, CompactBudgetPct: 100, CompactKeepTail: 2},
	}
	conv := seedRoom(t, d.Convs, "room", "go")
	model := entity.Model{}
	model.SetContextWindow(entity.DefaultUnknownMaxOutputTokens + 500)
	schema, _ := valueobject.NewToolSchema("push", "test tool", nil, true, enum.ToolPermissionAgent)
	driver := EnvironmentBundle{Tools: map[string]dservice.Tool{"push": fatTool{schema: schema}}}
	svc := &longTurnSvc{}
	if err := d.runTurn(t.Context(), "room", conv, driver, nil, model, svc, nil, nil, valueobject.TurnOrigin{}); err != nil {
		t.Fatalf("a fold longer than the seam deadline must still complete: %v", err)
	}
	if svc.handoffs.Load() == 0 {
		t.Fatal("no fold happened — the test proves nothing")
	}
	if conv.Last().Content() != "done" {
		t.Fatalf("the turn must finish past the slow fold: %+v", conv.Last())
	}
}
