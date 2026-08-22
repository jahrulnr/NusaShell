package application

import (
	"context"
	"testing"

	"nusashell/domain"
	"nusashell/resources"
)

// persistStubAdapter replays a scripted sequence of responses so the test
// can assert exactly which messages the review loop persists.
type persistStubAdapter struct {
	responses []ChatResponse
	calls     int
}

func (a *persistStubAdapter) Kind() domain.ProviderKind { return domain.ProviderChat }
func (a *persistStubAdapter) Stream(context.Context, ChatRequest, func(string), func(string)) (ChatResponse, error) {
	return ChatResponse{}, nil
}
func (a *persistStubAdapter) Complete(context.Context, ChatRequest) (ChatResponse, error) {
	if a.calls >= len(a.responses) {
		return ChatResponse{}, context.DeadlineExceeded
	}
	r := a.responses[a.calls]
	a.calls++
	return r, nil
}

// TestReviewLoopPersistsReasoningAndFinalSummary pins the persistence
// contract the Learning log UI depends on: every assistant round keeps its
// reasoning text, and the terminal response (the review's conclusion,
// e.g. "Nothing to save." or a summary of what was saved) becomes a final
// persisted assistant message instead of being dropped.
func TestReviewLoopPersistsReasoningAndFinalSummary(t *testing.T) {
	if resources.ReviewPrompt() == "" {
		t.Fatal("review prompt must be non-empty for the loop to run")
	}
	conv := &domain.Conversation{
		ID: "conv_1",
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "please remember I prefer Indonesian"},
		},
	}
	adapter := &persistStubAdapter{responses: []ChatResponse{
		{
			Reasoning: "The user stated a durable preference.",
			ToolCalls: []domain.ToolCall{{ID: "tc_1", Name: "memory_save", Args: `{"content":"user prefers Indonesian"}`}},
		},
		{Reasoning: "Save succeeded, done.", Content: "Saved one memory fragment."},
	}}

	agent := NewBackgroundReviewAgent(newReviewApp(&reviewStubToolbox{}), DefaultReviewSettings())
	_, messages, err := agent.runReviewLoop(context.Background(), adapter, "model", conv)
	if err != nil {
		t.Fatalf("runReviewLoop: %v", err)
	}

	// Expected shape: user transcript, assistant(+reasoning,+toolcall),
	// tool result, assistant final summary(+reasoning).
	if len(messages) != 4 {
		t.Fatalf("messages = %d, want 4; got %+v", len(messages), messages)
	}
	if messages[1].Reasoning != "The user stated a durable preference." {
		t.Errorf("round reasoning not persisted: %q", messages[1].Reasoning)
	}
	if messages[2].ToolResult == nil || messages[2].ToolResult.ToolCallID != "tc_1" {
		t.Fatalf("tool result missing: %+v", messages[2])
	}
	if messages[3].Role != "assistant" || messages[3].Content != "Saved one memory fragment." {
		t.Errorf("final summary not persisted as assistant message: %+v", messages[3])
	}
	if messages[3].Reasoning != "Save succeeded, done." {
		t.Errorf("final reasoning not persisted: %q", messages[3].Reasoning)
	}
}

// TestReviewLoopPersistsNothingToSaveConclusion covers the no-op path: the
// canonical "Nothing to save." response must still land in the transcript.
func TestReviewLoopPersistsNothingToSaveConclusion(t *testing.T) {
	if resources.ReviewPrompt() == "" {
		t.Fatal("review prompt must be non-empty for the loop to run")
	}
	conv := &domain.Conversation{
		ID: "conv_1",
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "hello"},
		},
	}
	adapter := &persistStubAdapter{responses: []ChatResponse{
		{Content: "Nothing to save."},
	}}

	agent := NewBackgroundReviewAgent(newReviewApp(&reviewStubToolbox{}), DefaultReviewSettings())
	_, messages, err := agent.runReviewLoop(context.Background(), adapter, "model", conv)
	if err != nil {
		t.Fatalf("runReviewLoop: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("messages = %d, want 2 (transcript + conclusion); got %+v", len(messages), messages)
	}
	if messages[1].Content != "Nothing to save." {
		t.Errorf("conclusion = %q, want %q", messages[1].Content, "Nothing to save.")
	}
}
