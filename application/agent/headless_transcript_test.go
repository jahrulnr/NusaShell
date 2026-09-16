package agent

import (
	"context"
	"io"
	"testing"

	"nusashell/domain"
	"nusashell/infrastructure/ai/core"
)

type headlessTranscriptProvider struct{}

func (headlessTranscriptProvider) Name() string { return "headless-transcript-test" }
func (headlessTranscriptProvider) Chat(context.Context, *core.Request) (*core.Response, error) {
	return &core.Response{FinishReason: core.FinishReasonStop}, nil
}
func (headlessTranscriptProvider) Stream(context.Context, *core.Request) (core.Stream, error) {
	return &headlessTranscriptStream{events: []core.Event{
		core.ReasoningDelta{Text: "thinking"},
		core.ContentDelta{Text: "answer"},
		core.DoneEvent{FinishReason: core.FinishReasonStop, Provider: "headless-transcript-test", Model: "model"},
	}}, nil
}

type headlessTranscriptStream struct {
	events []core.Event
	index  int
}

func (s *headlessTranscriptStream) Next() (core.Event, error) {
	if s.index >= len(s.events) {
		return nil, io.EOF
	}
	event := s.events[s.index]
	s.index++
	return event, nil
}

func (s *headlessTranscriptStream) Close() error { return nil }

func TestStreamTurnRoundEmitsHeadlessTranscriptDeltas(t *testing.T) {
	var chunks []domain.AcpTranscriptChunk
	run := &TurnRun{
		ID:             "run_stream",
		ConversationID: "conv_stream",
		Ctx:            context.Background(),
		Headless:       true,
		HeadlessTranscript: func(chunk domain.AcpTranscriptChunk) {
			chunks = append(chunks, chunk)
		},
	}
	svc := New(Deps{})
	conversation := &domain.Conversation{
		ID: "conv_stream",
		Messages: []domain.Message{{
			ID: "user_stream", Role: domain.RoleUser, Content: "work", Status: domain.StatusDone,
		}},
	}
	adapter := ProviderContext{Provider: headlessTranscriptProvider{}, Kind: domain.ProviderChat}

	if _, err := svc.StreamTurnRoundOnce(
		run, adapter, conversation, "assistant_stream", "model", "", nil,
		domain.DefaultSettings(), false, nil, 1024, nil, ModelCapabilities{}, 1,
	); err != nil {
		t.Fatalf("stream round: %v", err)
	}

	if len(chunks) != 2 {
		t.Fatalf("headless transcript chunks = %+v, want reasoning and text", chunks)
	}
	if chunks[0].Kind != "thought" || chunks[0].Text != "thinking" {
		t.Fatalf("first chunk = %+v, want thought thinking", chunks[0])
	}
	if chunks[1].Kind != "text" || chunks[1].Text != "answer" {
		t.Fatalf("second chunk = %+v, want text answer", chunks[1])
	}
	if chunks[0].At.IsZero() || chunks[1].At.IsZero() {
		t.Fatalf("stream chunks must carry timestamps: %+v", chunks)
	}
}
