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
func (a *persistStubAdapter) Stream(ctx context.Context, req ChatRequest, _ func(string), _ func(string)) (ChatResponse, error) {
	return a.Complete(ctx, req)
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
			ToolCalls: []domain.ToolCall{{ID: "tc_1", Name: "memory", Args: `{"op":"save","content":"user prefers Indonesian"}`}},
		},
		{Reasoning: "Save succeeded, done.", Content: "Saved one memory fragment."},
	}}

	agent := NewBackgroundReviewAgent(newReviewApp(&reviewStubToolbox{}), DefaultReviewSettings())
	_, messages, err := agent.runReviewLoop(context.Background(), adapter, "model", conv)
	if err != nil {
		t.Fatalf("runReviewLoop: %v", err)
	}

	// Expected shape after the synthetic-tool refactor:
	//   0: user (prompt from prompts/user/review.md)
	//   1: assistant (synthetic tool calls: review_transcript + memory_primary)
	//   2: tool (review_transcript result)
	//   3: tool (memory_primary result)
	//   4: assistant (first LLM response: reasoning + memory save tool call)
	//   5: tool (memory save result)
	//   6: assistant (final summary: reasoning + content)
	if len(messages) != 7 {
		t.Fatalf("messages = %d, want 7; got %+v", len(messages), messages)
	}
	if messages[4].Reasoning != "The user stated a durable preference." {
		t.Errorf("round reasoning not persisted: %q", messages[4].Reasoning)
	}
	if messages[5].ToolResult == nil || messages[5].ToolResult.ToolCallID != "tc_1" {
		t.Fatalf("tool result missing: %+v", messages[5])
	}
	if messages[6].Role != "assistant" || messages[6].Content != "Saved one memory fragment." {
		t.Errorf("final summary not persisted as assistant message: %+v", messages[6])
	}
	if messages[6].Reasoning != "Save succeeded, done." {
		t.Errorf("final reasoning not persisted: %q", messages[6].Reasoning)
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
	// Expected shape after the synthetic-tool refactor:
	//   0: user (prompt)
	//   1: assistant (synthetic tool calls)
	//   2: tool (review_transcript result)
	//   3: tool (memory_primary result)
	//   4: assistant ("Nothing to save." conclusion)
	if len(messages) != 5 {
		t.Fatalf("messages = %d, want 5 (prompt + synthetic + 2 tools + conclusion); got %+v", len(messages), messages)
	}
	if messages[4].Content != "Nothing to save." {
		t.Errorf("conclusion = %q, want %q", messages[4].Content, "Nothing to save.")
	}
}
