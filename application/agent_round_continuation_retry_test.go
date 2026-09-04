package application

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"nusashell/domain"
	"nusashell/infrastructure/ai/core"
)

// prematureEOFStream emits the given content deltas, then returns a network
// error wrapping io.ErrUnexpectedEOF — the exact error shape produced by
// openai/stream.go and compat/stream.go when the stream ends before [DONE]
// without finish_reason. This simulates a 2xx response that starts
// streaming content but closes the connection without completing the turn.
type prematureEOFStream struct {
	events []core.Event
	idx    int
}

func (s *prematureEOFStream) Next() (core.Event, error) {
	if s.idx < len(s.events) {
		ev := s.events[s.idx]
		s.idx++
		return ev, nil
	}
	return nil, core.NewNetworkError("openai", "openai: stream ended before [DONE] without finish_reason", io.ErrUnexpectedEOF)
}

func (s *prematureEOFStream) Close() error { return nil }

// prematureEOFThenOKProvider streams partial content then fails with a
// premature EOF on the first call, then succeeds on the second call. It
// records every request so the test can assert the retry happened and that
// the continuation nudge was injected.
type prematureEOFThenOKProvider struct {
	mu       sync.Mutex
	calls    int
	requests []*core.Request
}

func (p *prematureEOFThenOKProvider) Name() string { return "premature-eof-then-ok" }

func (p *prematureEOFThenOKProvider) Stream(_ context.Context, req *core.Request) (core.Stream, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.requests = append(p.requests, req)
	if p.calls == 1 {
		// Stream partial content, then cut without finish_reason or [DONE].
		return &prematureEOFStream{events: []core.Event{
			core.ContentDelta{Text: "Hello, I will help you with"},
		}}, nil
	}
	// Second call: the model should continue from where it stopped.
	// Verify the continuation tool was injected by checking the last messages.
	resp := &core.Response{
		Blocks:       []core.Block{core.TextBlock{Text: " that task."}},
		FinishReason: core.FinishReasonStop,
	}
	return &stubStream{events: coreResponseEvents(resp)}, nil
}

func (p *prematureEOFThenOKProvider) Chat(context.Context, *core.Request) (*core.Response, error) {
	return nil, errors.New("chat not used")
}

// TestStreamTurnRoundContinuesAfterPrematureEOF proves that when a provider
// stream delivers partial content then ends without [DONE] or finish_reason
// (a 2xx response that cuts mid-stream), the retry loop does NOT fail the
// turn. Instead it auto-retries with a continuation nudge: the partial
// content is injected as an ephemeral assistant message followed by the
// announcement tool, and the model continues from where it stopped.
//
// Without this behavior, a transient mid-stream EOF on a 2xx response
// requires the user to manually click retry — even though the partial
// content is valid and the model can continue seamlessly.
func TestStreamTurnRoundContinuesAfterPrematureEOF(t *testing.T) {
	provider := &prematureEOFThenOKProvider{}
	conv := &domain.Conversation{
		ID: "c-premature",
		Messages: []domain.Message{
			{ID: "u1", Role: domain.RoleUser, Content: "help me"},
			{ID: "a1", Role: domain.RoleAssistant},
		},
	}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c-premature": conv}},
		Toolbox:       &recordingToolbox{},
		Bus:           NewBus(),
		retrySleeper:  func(context.Context, time.Duration) error { return nil },
	}
	run := &TurnRun{ID: "r-premature", ConversationID: "c-premature", ProviderID: "openai", Ctx: context.Background()}

	res, err := app.streamTurnRound(run, stubProviderContext(provider), conv, "a1", "gpt-4.1", "", nil, domain.Settings{}, false, 100, nil, ModelCapabilities{}, 1)
	if err != nil {
		t.Fatalf("streamTurnRound returned error after premature EOF: %v (expected continuation retry to succeed)", err)
	}
	// The final content should be the accumulated text: the partial content
	// from the first attempt prepended to the continuation from the retry.
	if res.Content != "Hello, I will help you with that task." {
		t.Fatalf("content = %q, want %q", res.Content, "Hello, I will help you with that task.")
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls = %d, want 2 (initial EOF + continuation retry)", provider.calls)
	}
	// The retry request must contain the continuation announcement tool:
	// the partial content as an assistant message, followed by the
	// announcement tool call and its result.
	retryReq := provider.requests[1]
	foundPartial := false
	foundAnnouncement := false
	for _, msg := range retryReq.Messages {
		if msg.Role == core.RoleAssistant && len(msg.Blocks) == 1 {
			if tb, ok := msg.Blocks[0].(core.TextBlock); ok && tb.Text == "Hello, I will help you with" {
				foundPartial = true
			}
		}
		for _, b := range msg.Blocks {
			if tb, ok := b.(core.ToolUseBlock); ok && tb.Name == domain.AnnouncementToolName {
				foundAnnouncement = true
			}
		}
	}
	if !foundPartial {
		t.Fatal("retry request does not contain the partial content as an assistant message")
	}
	if !foundAnnouncement {
		t.Fatal("retry request does not contain the continuation announcement tool")
	}
}

// prematureEOFWithToolsProvider streams a tool-call start then fails with a
// premature EOF. Tool-call failures must NOT use continuation — the tool
// call needs a clean restart, not a "continue from where you stopped".
func TestStreamTurnRoundDoesNotContinueWhenToolCallsInProgress(t *testing.T) {
	provider := &prematureEOFWithToolsProvider{}
	conv := &domain.Conversation{
		ID: "c-premature-tools",
		Messages: []domain.Message{
			{ID: "u1", Role: domain.RoleUser, Content: "run a tool"},
			{ID: "a1", Role: domain.RoleAssistant},
		},
	}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c-premature-tools": conv}},
		Toolbox:       &recordingToolbox{},
		Bus:           NewBus(),
		retrySleeper:  func(context.Context, time.Duration) error { return nil },
	}
	run := &TurnRun{ID: "r-premature-tools", ConversationID: "c-premature-tools", ProviderID: "openai", Ctx: context.Background()}

	_, err := app.streamTurnRound(run, stubProviderContext(provider), conv, "a1", "gpt-4.1", "", nil, domain.Settings{}, false, 100, nil, ModelCapabilities{}, 1)
	if err == nil {
		t.Fatal("expected error when tool calls in progress and stream cuts, got nil (should not continue with tool calls)")
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1 (should not retry with continuation when tool calls are in progress)", provider.calls)
	}
}

type prematureEOFWithToolsProvider struct {
	calls int
}

func (p *prematureEOFWithToolsProvider) Name() string { return "premature-eof-tools" }

func (p *prematureEOFWithToolsProvider) Stream(context.Context, *core.Request) (core.Stream, error) {
	p.calls++
	idx := 0
	return &prematureEOFStream{events: []core.Event{
		core.ContentDelta{Text: "Let me check"},
		core.ToolUseStart{ID: "call_1", Name: "file_read", Index: &idx},
	}}, nil
}

func (p *prematureEOFWithToolsProvider) Chat(context.Context, *core.Request) (*core.Response, error) {
	return nil, errors.New("chat not used")
}
