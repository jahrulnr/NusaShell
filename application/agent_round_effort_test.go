package application

import (
	"context"
	"io"
	"testing"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/infrastructure/ai/core"
)

// effortCapturingProvider records the Thinking field of the last
// core.Request it received so the test can assert the effort strip guard.
type effortCapturingProvider struct {
	lastThinking *core.Thinking
}

func (p *effortCapturingProvider) Name() string { return "effort-capture" }

func (p *effortCapturingProvider) Chat(ctx context.Context, req *core.Request) (*core.Response, error) {
	p.lastThinking = req.Thinking
	return &core.Response{FinishReason: core.FinishReasonStop, Blocks: []core.Block{core.TextBlock{Text: "ok"}}}, nil
}

func (p *effortCapturingProvider) Stream(ctx context.Context, req *core.Request) (core.Stream, error) {
	p.lastThinking = req.Thinking
	resp, err := p.Chat(ctx, req)
	if err != nil {
		return nil, err
	}
	return &singleShotStream{resp: resp, provider: p.Name(), model: req.Model}, nil
}

type singleShotStream struct {
	resp     *core.Response
	provider string
	model    string
	done     bool
}

func (s *singleShotStream) Next() (core.Event, error) {
	if s.done {
		return nil, io.EOF
	}
	s.done = true
	return core.DoneEvent{FinishReason: s.resp.FinishReason, Provider: s.provider, Model: s.model}, nil
}

func (s *singleShotStream) Close() error { return nil }

type toolConstructionActivityProvider struct{}

func (p *toolConstructionActivityProvider) Name() string { return "tool-activity" }

func (p *toolConstructionActivityProvider) Chat(context.Context, *core.Request) (*core.Response, error) {
	return nil, nil
}

func (p *toolConstructionActivityProvider) Stream(context.Context, *core.Request) (core.Stream, error) {
	index := 0
	return &stubStream{events: []core.Event{
		core.ToolUseStart{ID: "call_1", Name: "file_read", Index: &index},
		core.ToolUseDelta{ID: "call_1", Index: &index, ArgumentsDelta: []byte(`{"path":`)},
		core.ToolUseDelta{ID: "call_1", Index: &index, ArgumentsDelta: []byte(`"/tmp/note"}`)},
		core.ToolUseDone{ID: "call_1", Index: &index},
		core.DoneEvent{FinishReason: core.FinishReasonToolCall, Provider: "tool-activity", Model: "test-model"},
	}}, nil
}

func TestStreamTurnRoundPublishesToolConstructionActivity(t *testing.T) {
	reg := NewRoundStreamRegistry()
	conv := &domain.Conversation{
		ID: "c-activity",
		Messages: []domain.Message{
			{ID: "u1", Role: domain.RoleUser, Content: "read the note"},
			{ID: "a1", Role: domain.RoleAssistant},
		},
	}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c-activity": conv}},
		Toolbox:       &recordingToolbox{},
		Bus:           NewBus(),
		RoundStreams:  reg,
	}
	run := &TurnRun{ID: "r-activity", ConversationID: conv.ID, Ctx: context.Background()}
	if _, err := app.streamTurnRoundOnce(run, stubProviderContext(&toolConstructionActivityProvider{}), conv, "a1", "test-model", "", nil, domain.Settings{}, false, 100, nil, ModelCapabilities{}, 1); err != nil {
		t.Fatalf("streamTurnRoundOnce: %v", err)
	}

	sub, err := reg.Subscribe(context.Background(), run.ID, "a1", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	select {
	case frame := <-sub.Frames():
		if frame.Kind != contracts.RoundDeltaActivity || frame.Activity != contracts.RoundActivityToolCall {
			t.Fatalf("activity frame = %+v", frame)
		}
		if frame.ToolCallID != "call_1" || frame.Name != "file_read" {
			t.Fatalf("activity metadata = %+v", frame)
		}
		if len(frame.Args) != 0 {
			t.Fatalf("activity frame leaked partial args: %s", frame.Args)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for tool construction activity")
	}
}

// TestStreamTurnRoundStripsEffortForNonReasoningModel proves Bug 2's guard:
// when caps.Reasoning=false and effort is a real level (e.g. "high"), the
// effort is stripped to "auto" before reaching the provider so non-reasoning
// models do not receive a thinking field.
func TestStreamTurnRoundStripsEffortForNonReasoningModel(t *testing.T) {
	provider := &effortCapturingProvider{}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": {
			ID: "c1",
			Messages: []domain.Message{
				{ID: "u1", Role: domain.RoleUser, Content: "hi"},
				{ID: "a1", Role: domain.RoleAssistant},
			},
		}}},
		Toolbox: &recordingToolbox{},
		Bus:     NewBus(),
	}
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: context.Background()}
	caps := ModelCapabilities{Vision: true, Reasoning: false}
	_, err := app.streamTurnRoundOnce(run, stubProviderContext(provider), &domain.Conversation{
		ID: "c1",
		Messages: []domain.Message{
			{ID: "u1", Role: domain.RoleUser, Content: "hi"},
			{ID: "a1", Role: domain.RoleAssistant},
		},
	}, "a1", "openai/gpt-4.1", "high", nil, domain.Settings{}, false, 100, nil, caps, 1)
	if err != nil {
		t.Fatalf("streamTurnRoundOnce: %v", err)
	}
	if provider.lastThinking != nil {
		t.Fatalf("non-reasoning model received Thinking=%+v, want nil (effort stripped)", provider.lastThinking)
	}
}

// TestStreamTurnRoundKeepsEffortForReasoningModel ensures the guard does
// not strip effort when the model supports reasoning.
func TestStreamTurnRoundKeepsEffortForReasoningModel(t *testing.T) {
	provider := &effortCapturingProvider{}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": {
			ID: "c1",
			Messages: []domain.Message{
				{ID: "u1", Role: domain.RoleUser, Content: "hi"},
				{ID: "a1", Role: domain.RoleAssistant},
			},
		}}},
		Toolbox: &recordingToolbox{},
		Bus:     NewBus(),
	}
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: context.Background()}
	caps := ModelCapabilities{Vision: true, Reasoning: true}
	_, err := app.streamTurnRoundOnce(run, stubProviderContext(provider), &domain.Conversation{
		ID: "c1",
		Messages: []domain.Message{
			{ID: "u1", Role: domain.RoleUser, Content: "hi"},
			{ID: "a1", Role: domain.RoleAssistant},
		},
	}, "a1", "deepseek/deepseek-r1", "high", nil, domain.Settings{}, false, 100, nil, caps, 1)
	if err != nil {
		t.Fatalf("streamTurnRoundOnce: %v", err)
	}
	if provider.lastThinking == nil || provider.lastThinking.Mode != core.ThinkingEnabled || provider.lastThinking.Effort != "high" {
		t.Fatalf("reasoning model received Thinking=%+v, want enabled/high", provider.lastThinking)
	}
}
