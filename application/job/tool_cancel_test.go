package job

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/config"
	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	repoconversation "github.com/jahrulnr/nusashell/infrastructure/repository/conversation"
	"github.com/jahrulnr/nusashell/infrastructure/worker"
)

// stoppedTool behaves like a process tool under a stop: it streams what it
// produced, waits for the cancel, and answers with that head plus the line
// naming who stopped it — no error, because the captured output is the
// call's outcome.
type stoppedTool struct {
	schema  valueobject.ToolSchema
	started chan struct{}
	// reportError makes Execute answer with the captured text *and* the
	// cancel error — the shape a tool that reports its own failure uses, and
	// the one only a turn that keeps `res.Text` under a cancel can save.
	reportError bool
}

func newStoppedTool(t *testing.T, reportError bool) *stoppedTool {
	t.Helper()
	schema, err := valueobject.NewToolSchema("exec", "run a command",
		json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`),
		true, enum.ToolPermissionAgent)
	if err != nil {
		t.Fatal(err)
	}
	return &stoppedTool{schema: schema, started: make(chan struct{}), reportError: reportError}
}

func (s *stoppedTool) Schema() valueobject.ToolSchema { return s.schema }

func (s *stoppedTool) Execute(ctx context.Context, _ entity.ToolCall, emit dservice.ToolEmitter) (dservice.ToolResult, error) {
	if emit != nil {
		emit("head line\n")
	}
	close(s.started)
	<-ctx.Done()
	res := dservice.ToolResult{Text: "head line\n(interrupted by user after 0.2s — partial output above)"}
	if s.reportError {
		return res, ctx.Err()
	}
	return res, nil
}

// toolCallSvc asks for one exec call and stops there.
type toolCallSvc struct{ calls int }

func (s *toolCallSvc) Send(_ context.Context, conv *aggregate.Conversation, _ *aggregate.Provider, _ entity.Model, _ event.TurnEventHandler) error {
	s.calls++
	if s.calls > 1 {
		return apperror.New(500, "provider called again", nil)
	}
	m, err := entity.NewMessage(enum.RoleAssistant, "running it")
	if err != nil {
		return err
	}
	call, err := entity.NewToolCall("call_1", "exec", `{"command":"sleep 30"}`, enum.TooltypeOrigin)
	if err != nil {
		return err
	}
	if err := m.AddToolCall(call); err != nil {
		return err
	}
	m.SetFinishReason(enum.FinishReasonToolCalls)
	conv.Append(m)
	return nil
}

// A tool stopped mid-run keeps what it captured: its result is written as the
// call's outcome — head and the line naming the stop — before the turn ends
// canceled. Replacing that with the blind "interrupted by user" marker hides
// everything the command did, which is what the owner could not read.
func TestCanceledToolCallKeepsItsCapturedOutput(t *testing.T) {
	for _, tc := range []struct {
		name        string
		reportError bool
	}{
		// Exec answers with the captured text and no error.
		{"result", false},
		// A tool that also reports the cancel still keeps its head.
		{"result and error", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := Deps{
				SummaryTool: mustSummaryTool(t),
				Convs:       repoconversation.NewConversationRepository(t.TempDir()),
				Config:      config.ConversationConfig{MaxTurnStep: 4},
			}
			tool := newStoppedTool(t, tc.reportError)
			bundle := EnvironmentBundle{Tools: map[string]dservice.Tool{"exec": tool}}
			d.Environments = map[enum.Environment]EnvironmentDriver{enum.EnvironmentLocal: bundle}
			conv := seedRoom(t, d.Convs, "room", "hi")

			ctx, stop := context.WithCancelCause(t.Context())
			defer stop(nil)
			go func() {
				<-tool.started
				// What the owner's Stop does to a job's ctx.
				stop(dservice.ErrInterruptedByUser)
			}()

			err := d.runTurn(ctx, "room", conv, bundle, nil, entity.Model{}, &toolCallSvc{}, nil, nil, valueobject.TurnOrigin{})
			if code := apperror.As(err).StatusCode(); code != 408 {
				t.Fatalf("canceled turn → %v (status %d), want 408", err, code)
			}

			loaded, lerr := d.Convs.Load(t.Context(), "room")
			if lerr != nil {
				t.Fatal(lerr)
			}
			last := loaded.Last()
			if last == nil || last.Role() != enum.RoleAssistant {
				t.Fatalf("last message = %+v, want the assistant call", last)
			}
			calls := last.ToolCalls()
			if len(calls) != 1 {
				t.Fatalf("tool calls = %d, want 1", len(calls))
			}
			out := calls[0].Output()
			if !strings.HasPrefix(out, "head line\n") {
				t.Fatalf("persisted tool output = %q, want the captured head", out)
			}
			if !strings.HasSuffix(out, "(interrupted by user after 0.2s — partial output above)") {
				t.Fatalf("persisted tool output = %q, want the interrupt line", out)
			}
			if strings.TrimSpace(out) == "interrupted by user" {
				t.Fatal("the call fell back to the blind marker instead of its output")
			}
			if !calls[0].HasOutput() || calls[0].OutputLatency() == "" {
				t.Fatalf("call record = %+v, want a stored result", calls[0])
			}
			// The provider's own finish reason stays: this message asked for a
			// tool, and the interrupt is recorded on the call, not by
			// rewriting it.
			if got := last.FinishReason(); got != enum.FinishReasonToolCalls {
				t.Fatalf("finish reason = %q, want the provider's tool_calls", got)
			}
		})
	}
}

// The owner's Stop carries its name onto the job's ctx, which is how a tool
// reports "interrupted by user" instead of a bare signal.
func TestInterruptNamesTheOwnerAsCause(t *testing.T) {
	var b Base
	ctx := b.Cancellable(context.Background(), context.Background())
	b.Interrupt()
	if cause := context.Cause(ctx); !errors.Is(cause, dservice.ErrInterruptedByUser) {
		t.Fatalf("cause = %v, want the owner's Stop", cause)
	}
}

// A caller that left — a tab closed mid-turn — is nobody's decision: the
// cause stays the neutral canceled, so no actor is invented for it.
func TestCallerGoneKeepsTheNeutralCause(t *testing.T) {
	var b Base
	caller, cancel := context.WithCancel(context.Background())
	cancel()
	ctx := b.Cancellable(context.Background(), caller)
	if cause := context.Cause(ctx); !errors.Is(cause, context.Canceled) {
		t.Fatalf("cause = %v, want the neutral canceled", cause)
	}
	if errors.Is(context.Cause(ctx), dservice.ErrInterruptedByUser) {
		t.Fatal("a caller that left must not be blamed on the owner")
	}
}

// shuttingDownJob derives its ctx the way every room job does, so this is the
// composition a restart actually cuts: what the job sees must be the restart
// cause, not the soft Interrupt that runs beside it.
type shuttingDownJob struct {
	Base
	started chan struct{}
	cause   chan error
}

func (j *shuttingDownJob) Execute(execCtx context.Context) error {
	ctx := j.Cancellable(execCtx, context.Background())
	close(j.started)
	<-ctx.Done()
	j.cause <- context.Cause(ctx)
	return ctx.Err()
}

func (j *shuttingDownJob) Interrupt() { j.Base.Interrupt() }

func TestWorkerTerminateGivesTheRestartCause(t *testing.T) {
	w := worker.New(worker.Options{})
	job := &shuttingDownJob{started: make(chan struct{}), cause: make(chan error, 1)}
	if err := w.Submit(job); err != nil {
		t.Fatal(err)
	}
	<-job.started
	w.Terminate()
	select {
	case cause := <-job.cause:
		if !errors.Is(cause, dservice.ErrServerRestarted) {
			t.Fatalf("job cause after Worker.Terminate = %v, want the restart", cause)
		}
		if errors.Is(cause, dservice.ErrInterruptedByUser) {
			t.Fatal("a restart must never be labeled as the owner's Stop")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the job never observed the termination")
	}
	select {
	case <-w.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("the worker did not stop")
	}
}

// A call that never ran is marked with its cause — a restart is not the
// owner's Stop, and the short marker says which happened (ADR-0004 §7).
func TestStampInterruptedNamesItsCause(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cause error
		want  string
	}{
		{"owner", dservice.ErrInterruptedByUser, "interrupted by user"},
		{"restart", dservice.ErrServerRestarted, "interrupted by a nusashell restart"},
		// The caller left: nobody decided it, so the marker must not blame
		// the owner.
		{"caller gone", context.Canceled, "(canceled — the caller went away)"},
		// An unknown cause is never rounded to the owner: the marker says the
		// cause has no name instead of naming one.
		{"unknown cause", errors.New("a mechanism that has not landed yet"), "(interrupted — cause not named)"},
		// The agent's cause is carried, and it does not borrow the owner's
		// words either.
		{"agent", dservice.ErrInterruptedByAgent, "(interrupted — cause not named)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg, err := entity.NewMessage(enum.RoleAssistant, "partial")
			if err != nil {
				t.Fatal(err)
			}
			call, err := entity.NewToolCall("call_1", "exec", `{}`, enum.TooltypeOrigin)
			if err != nil {
				t.Fatal(err)
			}
			if err := msg.AddToolCall(call); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancelCause(context.Background())
			cancel(tc.cause)
			if err := stampInterrupted(ctx, &msg); err != nil {
				t.Fatal(err)
			}
			if got := msg.ToolCalls()[0].Output(); got != tc.want {
				t.Fatalf("marker = %q, want %q", got, tc.want)
			}
			if fr := msg.FinishReason(); fr != enum.FinishReasonInterrupted {
				t.Fatalf("finish reason = %q, want interrupted", fr)
			}
		})
	}
}

// streamingSettledTool streams its head, then settles with text the stream
// never carried — an exec that streamed output and then prints its status
// line. The live row must receive that tail too, or a stopped run reads as a
// clean success until the owner reloads.
type streamingSettledTool struct {
	stoppedTool
}

func (s *streamingSettledTool) Execute(ctx context.Context, _ entity.ToolCall, emit dservice.ToolEmitter) (dservice.ToolResult, error) {
	if emit != nil {
		emit("head line\n")
	}
	close(s.started)
	<-ctx.Done()
	return dservice.ToolResult{Text: "head line\n(interrupted by user after 0.2s — partial output above)"}, nil
}

func TestStreamedToolSettledTailReachesTheLiveRow(t *testing.T) {
	d := Deps{
		SummaryTool: mustSummaryTool(t),
		Convs:       repoconversation.NewConversationRepository(t.TempDir()),
		Config:      config.ConversationConfig{MaxTurnStep: 4},
	}
	tool := &streamingSettledTool{stoppedTool{started: make(chan struct{})}}
	schema, err := valueobject.NewToolSchema("exec", "run a command",
		json.RawMessage(`{"type":"object","properties":{}}`), true, enum.ToolPermissionAgent)
	if err != nil {
		t.Fatal(err)
	}
	tool.schema = schema
	bundle := EnvironmentBundle{Tools: map[string]dservice.Tool{"exec": tool}}
	d.Environments = map[enum.Environment]EnvironmentDriver{enum.EnvironmentLocal: bundle}
	conv := seedRoom(t, d.Convs, "room", "hi")

	ctx, stop := context.WithCancelCause(t.Context())
	defer stop(nil)
	go func() {
		<-tool.started
		stop(dservice.ErrInterruptedByUser)
	}()

	var streamed strings.Builder
	onEvent := func(ev event.TurnEvent) {
		if ev.Kind() == enum.TurnEventToolOutput {
			streamed.WriteString(ev.Delta())
		}
	}
	if err := d.runTurn(ctx, "room", conv, bundle, nil, entity.Model{}, &toolCallSvc{}, onEvent, nil, valueobject.TurnOrigin{}); apperror.As(err).StatusCode() != 408 {
		t.Fatalf("turn → %v, want 408", err)
	}
	// Exactly once: what the row rendered must equal what the record holds —
	// a suffix check alone passes even when the tail is sent twice.
	loaded, lerr := d.Convs.Load(t.Context(), "room")
	if lerr != nil {
		t.Fatal(lerr)
	}
	persisted := loaded.Last().ToolCalls()[0].Output()
	if got := streamed.String(); got != persisted {
		t.Fatalf("the client saw %q, the record holds %q — both must be the same text, once", got, persisted)
	}
	if strings.Count(streamed.String(), "(interrupted by user after 0.2s") != 1 {
		t.Fatalf("the tail crossed %d times: %q", strings.Count(streamed.String(), "(interrupted by user after 0.2s"), streamed.String())
	}
}

// streamThenBlockSvc streams a partial answer, then waits for the cancel —
// the shape a restart cuts mid-stream.
type streamThenBlockSvc struct {
	started chan struct{}
}

func (s *streamThenBlockSvc) Send(ctx context.Context, _ *aggregate.Conversation, _ *aggregate.Provider, _ entity.Model, onEvent event.TurnEventHandler) error {
	onEvent(event.NewDeltaEvent("the answer so far"))
	close(s.started)
	<-ctx.Done()
	return apperror.New(408, "turn canceled", ctx.Err())
}

// A restart mid-stream keeps the reply the owner already watched: the partial
// is persisted, sealed interrupted, with the cause that cut it — losing it
// would make the last screen a lie.
func TestCanceledStreamKeepsItsPartialReply(t *testing.T) {
	d := Deps{
		SummaryTool: mustSummaryTool(t),
		Convs:       repoconversation.NewConversationRepository(t.TempDir()),
		Config:      config.ConversationConfig{MaxTurnStep: 4},
	}
	conv := seedRoom(t, d.Convs, "room", "hi")
	svc := &streamThenBlockSvc{started: make(chan struct{})}
	ctx, stop := context.WithCancelCause(t.Context())
	defer stop(nil)
	go func() {
		<-svc.started
		stop(dservice.ErrServerRestarted)
	}()

	err := d.runTurn(ctx, "room", conv, EnvironmentBundle{}, nil, entity.Model{}, svc, nil, nil, valueobject.TurnOrigin{})
	if code := apperror.As(err).StatusCode(); code != 408 {
		t.Fatalf("turn → %v (status %d), want 408", code, code)
	}
	loaded, lerr := d.Convs.Load(t.Context(), "room")
	if lerr != nil {
		t.Fatal(lerr)
	}
	last := loaded.Last()
	if last == nil || last.Role() != enum.RoleAssistant {
		t.Fatalf("last = %+v, want the partial assistant reply", last)
	}
	if last.Content() != "the answer so far" {
		t.Fatalf("partial reply = %q, want what the owner watched", last.Content())
	}
	if got := last.FinishReason(); got != enum.FinishReasonInterrupted {
		t.Fatalf("finish reason = %q, want interrupted", got)
	}
	// One partial, not two: appending it twice would still leave a matching
	// Last().
	assistants := 0
	for _, m := range loaded.Messages() {
		if m.Role() == enum.RoleAssistant {
			assistants++
		}
	}
	if assistants != 1 {
		t.Fatalf("assistant messages = %d, want exactly the one partial reply", assistants)
	}
}
