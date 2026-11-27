package job

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/config"
	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	repoconversation "github.com/jahrulnr/nusashell/infrastructure/repository/conversation"
)

// Tests stub the backoff to keep the package fast — the loop's
// attempt counting, not its wall-clock pacing, is what matters here.
func init() {
	sendRetryBackoff = []time.Duration{time.Millisecond, time.Millisecond}
}

// flakySvc fails its first scripted calls with the given status before
// answering — the attempt count proves the retry loop engaged.
type flakySvc struct {
	calls  int32
	fails  int32
	status int
}

func (s *flakySvc) Send(_ context.Context, conv *aggregate.Conversation, _ *aggregate.Provider, _ entity.Model, _ event.TurnEventHandler) error {
	if atomic.AddInt32(&s.calls, 1) <= s.fails {
		return apperror.New(s.status, "upstream unreachable", errors.New("dial failed"))
	}
	m, _ := entity.NewMessage(enum.RoleAssistant, "done")
	m.SetFinishReason(enum.FinishReasonStop)
	conv.Append(m)
	return nil
}

// A transient 5xx clears on its own: the turn retries inside the same
// call and completes — the user never sees the blip.
func TestRunTurnRetriesTransientUpstreamFailure(t *testing.T) {
	d := Deps{
		SummaryTool: mustSummaryTool(t),
		Convs:       repoconversation.NewConversationRepository(t.TempDir()),
		Config:      config.ConversationConfig{MaxTurnStep: 4},
	}
	conv := seedRoom(t, d.Convs, "room", "hi")
	svc := &flakySvc{fails: 2, status: 502}
	if err := d.runTurn(t.Context(), "room", conv, EnvironmentBundle{}, nil, entity.Model{}, svc, nil, nil, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}
	if want := int32(1 + len(sendRetryBackoff)); svc.calls != want {
		t.Fatalf("calls = %d, want %d (1 attempt + %d retries)", svc.calls, want, len(sendRetryBackoff))
	}
	if last := conv.Last(); last == nil || last.Content() != "done" {
		t.Fatalf("reply = %+v", conv.Last())
	}
}

// Each retry pause is announced on the turn's stream before it starts — a
// provider asking for quiet reads as a pause, not as a stalled reply.
func TestRunTurnAnnouncesRetryWait(t *testing.T) {
	d := Deps{
		SummaryTool: mustSummaryTool(t),
		Convs:       repoconversation.NewConversationRepository(t.TempDir()),
		Config:      config.ConversationConfig{MaxTurnStep: 4},
	}
	conv := seedRoom(t, d.Convs, "room", "hi")
	svc := &flakySvc{fails: 2, status: 503}
	var waits []enum.TurnStatus
	onEvent := func(e event.TurnEvent) {
		if e.Kind() == enum.TurnEventStatus {
			waits = append(waits, e.Status())
		}
	}
	if err := d.runTurn(t.Context(), "room", conv, EnvironmentBundle{}, nil, entity.Model{}, svc, onEvent, nil, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}
	if len(waits) != len(sendRetryBackoff) {
		t.Fatalf("retry_wait announcements = %d, want one per retry (%d)", len(waits), len(sendRetryBackoff))
	}
	for _, s := range waits {
		if s != enum.TurnStatusRetryWait {
			t.Fatalf("status = %q, want %q", s, enum.TurnStatusRetryWait)
		}
	}
}

// Client statuses never clear on their own — one call and out.
func TestRunTurnNoRetryOnClientError(t *testing.T) {
	d := Deps{
		SummaryTool: mustSummaryTool(t),
		Convs:       repoconversation.NewConversationRepository(t.TempDir()),
		Config:      config.ConversationConfig{MaxTurnStep: 4},
	}
	conv := seedRoom(t, d.Convs, "room", "hi")
	svc := &flakySvc{fails: 9, status: 400}
	if err := d.runTurn(t.Context(), "room", conv, EnvironmentBundle{}, nil, entity.Model{}, svc, nil, nil, valueobject.TurnOrigin{}); err == nil {
		t.Fatal("a definitive provider error must fail the turn")
	}
	if svc.calls != 1 {
		t.Fatalf("calls = %d, want 1 — client errors are not retried", svc.calls)
	}
}

// A provider that stays down exhausts the bounded retries and the turn
// fails — the backlog never stalls on it.
func TestRunTurnRetryExhaustion(t *testing.T) {
	d := Deps{
		SummaryTool: mustSummaryTool(t),
		Convs:       repoconversation.NewConversationRepository(t.TempDir()),
		Config:      config.ConversationConfig{MaxTurnStep: 4},
	}
	conv := seedRoom(t, d.Convs, "room", "hi")
	svc := &flakySvc{fails: 9, status: 503}
	err := d.runTurn(t.Context(), "room", conv, EnvironmentBundle{}, nil, entity.Model{}, svc, nil, nil, valueobject.TurnOrigin{})
	if err == nil {
		t.Fatal("exhausted retries must fail the turn")
	}
	if want := int32(1 + len(sendRetryBackoff)); svc.calls != want {
		t.Fatalf("calls = %d, want %d", svc.calls, want)
	}
	// Exhaustion leaves the refusal once. The retries did not each append a
	// line, and the sentence is the one the thread shows.
	if got := assistantLines(conv.Messages()); len(got) != 1 || got[0].Content() != "upstream unreachable" {
		t.Fatalf("in-memory assistants = %d, want the one refusal", len(got))
	}
	loaded, lerr := d.Convs.Load(t.Context(), "room")
	if lerr != nil {
		t.Fatal(lerr)
	}
	if got := assistantLines(loaded.Messages()); len(got) != 1 || got[0].Content() != "upstream unreachable" {
		t.Fatalf("stored assistants = %d, want the one refusal", len(got))
	}
}

// assistantLines lists the assistant messages in a history — what a retry
// cycle must leave at most one of.
func assistantLines(msgs []entity.Message) []entity.Message {
	var out []entity.Message
	for _, m := range msgs {
		if m.Role() == enum.RoleAssistant {
			out = append(out, m)
		}
	}
	return out
}

// emitThenFailSvc streams a delta, then fails mid-read — output already
// reached the client, so a retry would duplicate it on the wire.
type emitThenFailSvc struct {
	calls  int32
	status int
}

func (s *emitThenFailSvc) Send(_ context.Context, _ *aggregate.Conversation, _ *aggregate.Provider, _ entity.Model, emit event.TurnEventHandler) error {
	atomic.AddInt32(&s.calls, 1)
	emit(event.NewDeltaEvent("partial"))
	return apperror.New(s.status, "upstream stream broken", errors.New("mid-read failure"))
}

// streamThenRecoverSvc streams deltas and fails transiently on its first
// call, then streams its own deltas and commits an answer.
type streamThenRecoverSvc struct {
	calls int32
}

func (s *streamThenRecoverSvc) Send(_ context.Context, conv *aggregate.Conversation, _ *aggregate.Provider, _ entity.Model, emit event.TurnEventHandler) error {
	if atomic.AddInt32(&s.calls, 1) == 1 {
		emit(event.NewDeltaEvent("failed segment"))
		return apperror.New(502, "upstream stream broken", errors.New("mid-read failure"))
	}
	emit(event.NewDeltaEvent("answer"))
	m, _ := entity.NewMessage(enum.RoleAssistant, "the answer")
	m.SetFinishReason(enum.FinishReasonStop)
	conv.Append(m)
	return nil
}

// Deltas the client already watched do not block a retry: the failed
// attempt persisted nothing, and the client drops its segment when it sees
// retry_wait. History carries only the attempt that succeeded.
func TestRunTurnRetriesAfterStreamedDeltas(t *testing.T) {
	d := Deps{
		SummaryTool: mustSummaryTool(t),
		Convs:       repoconversation.NewConversationRepository(t.TempDir()),
		Config:      config.ConversationConfig{MaxTurnStep: 4},
	}
	conv := seedRoom(t, d.Convs, "room", "hi")
	svc := &streamThenRecoverSvc{}
	var waits, deltas int
	onEvent := func(e event.TurnEvent) {
		switch e.Kind() {
		case enum.TurnEventStatus:
			if e.Status() == enum.TurnStatusRetryWait {
				waits++
			}
		case enum.TurnEventDelta:
			deltas++
		}
	}
	if err := d.runTurn(t.Context(), "room", conv, EnvironmentBundle{}, nil, entity.Model{}, svc, onEvent, nil, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}
	if svc.calls != 2 {
		t.Fatalf("calls = %d, want 2 — a transient failure after deltas still retries", svc.calls)
	}
	if waits != 1 {
		t.Fatalf("retry_wait announcements = %d, want 1", waits)
	}
	if deltas != 2 {
		t.Fatalf("deltas emitted = %d, want the failed attempt's and the retry's", deltas)
	}

	loaded, err := d.Convs.Load(t.Context(), "room")
	if err != nil {
		t.Fatal(err)
	}
	assistants := assistantLines(loaded.Messages())
	if len(assistants) != 1 {
		t.Fatalf("assistant messages = %d, want 1 — the failed attempt left nothing", len(assistants))
	}
	if m := assistants[0]; m.Content() != "the answer" || m.FinishReason() != enum.FinishReasonStop {
		t.Fatalf("assistant message = %q/%q, want only the successful attempt", m.Content(), m.FinishReason())
	}
	if last := conv.Last(); last == nil || last.Content() != "the answer" {
		t.Fatalf("in-memory tail = %+v, want the successful reply", conv.Last())
	}
}

// commitThenFailSvc commits an assistant message, then fails transiently —
// the call left a message behind, so replaying it would double the reply.
type commitThenFailSvc struct {
	calls  int32
	status int
}

func (s *commitThenFailSvc) Send(_ context.Context, conv *aggregate.Conversation, _ *aggregate.Provider, _ entity.Model, _ event.TurnEventHandler) error {
	atomic.AddInt32(&s.calls, 1)
	m, _ := entity.NewMessage(enum.RoleAssistant, "committed")
	conv.Append(m)
	return apperror.New(s.status, "upstream failed after commit", errors.New("late failure"))
}

// A call that already appended a message is not retried, however transient
// the failure: that message is the turn's outcome, and the error surfaces
// instead of a second attempt.
func TestRunTurnNoRetryAfterAppendedMessage(t *testing.T) {
	d := Deps{
		SummaryTool: mustSummaryTool(t),
		Convs:       repoconversation.NewConversationRepository(t.TempDir()),
		Config:      config.ConversationConfig{MaxTurnStep: 4},
	}
	conv := seedRoom(t, d.Convs, "room", "hi")
	svc := &commitThenFailSvc{status: 502}
	if err := d.runTurn(t.Context(), "room", conv, EnvironmentBundle{}, nil, entity.Model{}, svc, nil, nil, valueobject.TurnOrigin{}); err == nil {
		t.Fatal("a failure after an appended message must fail the turn")
	}
	if svc.calls != 1 {
		t.Fatalf("calls = %d, want 1 — an appended message blocks retry", svc.calls)
	}
	loaded, err := d.Convs.Load(t.Context(), "room")
	if err != nil {
		t.Fatal(err)
	}
	if got := assistantLines(loaded.Messages()); len(got) != 0 {
		t.Fatalf("stored assistants = %d, want none — the failed call persisted nothing", len(got))
	}
}

// The text a failed stream already showed the owner survives the failure
// as an interrupted reply, in memory and on disk — after the transient
// retries are spent, since only a persisted message ends them. Without it
// a Retry regenerates the answer and the partial the reader watched
// vanishes.
func TestRunTurnKeepsStreamedTextOnFailure(t *testing.T) {
	d := Deps{
		SummaryTool: mustSummaryTool(t),
		Convs:       repoconversation.NewConversationRepository(t.TempDir()),
		Config:      config.ConversationConfig{MaxTurnStep: 4},
	}
	conv := seedRoom(t, d.Convs, "room", "hi")
	svc := &emitThenFailSvc{status: 502}
	if err := d.runTurn(t.Context(), "room", conv, EnvironmentBundle{}, nil, entity.Model{}, svc, nil, nil, valueobject.TurnOrigin{}); err == nil {
		t.Fatal("a mid-stream failure must fail the turn")
	}
	if want := int32(1 + len(sendRetryBackoff)); svc.calls != want {
		t.Fatalf("calls = %d, want %d — the transient failure retries before the partial is kept", svc.calls, want)
	}
	check := func(label string, c *aggregate.Conversation) {
		t.Helper()
		last := c.Last()
		if last == nil || last.Role() != enum.RoleAssistant || last.Content() != "partial" {
			t.Fatalf("%s: tail = %+v, want the streamed partial reply", label, last)
		}
		if last.FinishReason() != enum.FinishReasonInterrupted {
			t.Fatalf("%s: finish = %q, want interrupted", label, last.FinishReason())
		}
	}
	check("in memory", conv)
	reloaded, err := d.Convs.Load(t.Context(), "room")
	if err != nil {
		t.Fatal(err)
	}
	check("reloaded", reloaded)
}

// Only answer text makes a kept reply: tool-call parts point at calls that
// never reached history, and reasoning alone has nothing a Retry can replay.
func TestInterruptedReplyKeepsAnswerTextOnly(t *testing.T) {
	msg, ok, err := interruptedReply([]entity.MessagePart{
		{Kind: entity.MessagePartReasoning, Text: "considering"},
		{Kind: entity.MessagePartText, Text: "Hel"},
		{Kind: entity.MessagePartToolCall, ToolCallID: "call_1"},
		{Kind: entity.MessagePartText, Text: "lo"},
	})
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v, want a kept reply", ok, err)
	}
	if msg.Content() != "Hello" || msg.FinishReason() != enum.FinishReasonInterrupted {
		t.Fatalf("reply = %q/%q, want Hello/interrupted", msg.Content(), msg.FinishReason())
	}
	if got := msg.Parts(); len(got) != 3 {
		t.Fatalf("parts = %+v, want reasoning, text, text with the tool call dropped", got)
	}
	if _, ok, _ := interruptedReply([]entity.MessagePart{{Kind: entity.MessagePartReasoning, Text: "thinking"}}); ok {
		t.Fatal("reasoning without answer text must not be kept")
	}
}

// A 413 after emitted output can't compact-and-retry either — the
// delta already crossed the wire, so the overflow error propagates.
func TestRunTurn413AfterEmittedDeltaPropagates(t *testing.T) {
	d := Deps{
		SummaryTool: mustSummaryTool(t),
		Convs:       repoconversation.NewConversationRepository(t.TempDir()),
		Config:      config.ConversationConfig{MaxTurnStep: 4},
	}
	conv, _ := aggregate.NewConversation("room")
	if err := d.Convs.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	// A tail longer than keepTail arms the compact-retry branch —
	// only the emitted delta stands between it and the fold.
	for _, seed := range []struct {
		role enum.Role
		text string
	}{
		{enum.RoleUser, "hi"},
		{enum.RoleAssistant, "first answer"},
		{enum.RoleUser, "follow-up"},
		{enum.RoleAssistant, "second answer"},
		{enum.RoleUser, "again"},
	} {
		m, _ := entity.NewMessage(seed.role, seed.text)
		conv.Append(m)
		if _, err := d.Convs.AppendMessage(t.Context(), "room", m); err != nil {
			t.Fatal(err)
		}
	}
	svc := &emitThenFailSvc{status: 413}
	err := d.runTurn(t.Context(), "room", conv, EnvironmentBundle{}, nil, entity.Model{}, svc, nil, nil, valueobject.TurnOrigin{})
	if apperror.As(err).StatusCode() != 413 {
		t.Fatalf("post-delta 413 must propagate, got %v", err)
	}
	if svc.calls != 1 {
		t.Fatalf("compact-retry fired despite the emitted delta: calls=%d", svc.calls)
	}
}

// A 429 carrying Retry-After is an instruction, not a hint we may ignore:
// the next attempt must wait at least that long. The fixed sendRetryBackoff
// (1ms in tests) must not fire first and hammer a provider that asked for
// quiet.
type retryAfterSvc struct {
	calls int32
}

type retryAfterError struct {
	err   error
	delay time.Duration
}

func (e retryAfterError) Error() string             { return e.err.Error() }
func (e retryAfterError) Unwrap() error             { return e.err }
func (e retryAfterError) RetryAfter() time.Duration { return e.delay }

func (s *retryAfterSvc) Send(_ context.Context, _ *aggregate.Conversation, _ *aggregate.Provider, _ entity.Model, _ event.TurnEventHandler) error {
	atomic.AddInt32(&s.calls, 1)
	return retryAfterError{err: apperror.New(429, "rate limited", errors.New("too many requests")), delay: 5 * time.Second}
}

func TestRunTurnHonorsRetryAfter(t *testing.T) {
	d := Deps{
		SummaryTool: mustSummaryTool(t),
		Convs:       repoconversation.NewConversationRepository(t.TempDir()),
		Config:      config.ConversationConfig{MaxTurnStep: 4},
	}
	conv := seedRoom(t, d.Convs, "room", "hi")
	svc := &retryAfterSvc{}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	err := d.runTurn(ctx, "room", conv, EnvironmentBundle{}, nil, entity.Model{}, svc, nil, nil, valueobject.TurnOrigin{})
	if apperror.As(err).StatusCode() != 408 {
		t.Fatalf("retry-after wait must end as a canceled turn, got %v", err)
	}
	if svc.calls != 1 {
		t.Fatalf("calls = %d, want 1 — Retry-After forbids an early retry", svc.calls)
	}
}
