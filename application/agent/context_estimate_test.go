package agent

import (
	"context"
	"testing"

	"nusashell/contracts"
	"nusashell/domain"
)

type contextEstimateEmitter struct {
	events []contracts.ContextEstimateEvent
}

func (e *contextEstimateEmitter) Emit(typ string, payload any) {
	if typ != contracts.EventContextEstimate {
		return
	}
	if evt, ok := payload.(contracts.ContextEstimateEvent); ok {
		e.events = append(e.events, evt)
	}
}

func TestStreamTurnRoundOnceEmitsContextEstimateBreakdown(t *testing.T) {
	emitter := &contextEstimateEmitter{}
	run := &TurnRun{
		ID:             "run_estimate",
		ConversationID: "conv_estimate",
		Ctx:            context.Background(),
	}
	svc := New(Deps{Bus: emitter})
	conversation := &domain.Conversation{
		ID: "conv_estimate",
		Messages: []domain.Message{{
			ID: "user_estimate", Role: domain.RoleUser, Content: "work", Status: domain.StatusDone,
		}},
	}
	adapter := ProviderContext{Provider: headlessTranscriptProvider{}, Kind: domain.ProviderChat}
	tools := []ToolDef{{
		Name:        "read_file",
		Description: "Read a file from disk",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}},
	}}

	result, err := svc.StreamTurnRoundOnce(
		run, adapter, conversation, "assistant_estimate", "model", "", tools,
		domain.DefaultSettings(), false, nil, 1024, nil, ModelCapabilities{}, 1,
	)
	if err != nil {
		t.Fatalf("stream round: %v", err)
	}
	if len(emitter.events) != 1 {
		t.Fatalf("context estimate events = %d, want exactly one", len(emitter.events))
	}

	evt := emitter.events[0]
	if evt.EstimatedTokens <= 0 {
		t.Fatalf("estimated tokens = %d, want positive", evt.EstimatedTokens)
	}
	if evt.EstimatedTokens != result.RequestTokens {
		t.Fatalf("event estimate = %d, round result = %d; both must come from the same estimate", evt.EstimatedTokens, result.RequestTokens)
	}
	if evt.SystemTokens <= 0 {
		t.Fatalf("system tokens = %d, want positive for the built system prompt", evt.SystemTokens)
	}
	if evt.MessagesTokens <= 0 {
		t.Fatalf("messages tokens = %d, want positive for the user message", evt.MessagesTokens)
	}
	if evt.ToolsTokens <= 0 {
		t.Fatalf("tools tokens = %d, want positive for the advertised tool defs", evt.ToolsTokens)
	}
	if sum := evt.SystemTokens + evt.MessagesTokens + evt.ToolsTokens; sum != evt.EstimatedTokens {
		t.Fatalf("category sum = %d, estimated tokens = %d; breakdown must partition the total exactly", sum, evt.EstimatedTokens)
	}
}
