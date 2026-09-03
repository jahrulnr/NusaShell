package application

import (
	"context"
	"testing"

	"nusashell/domain"
	"nusashell/infrastructure/ai/core"
	"nusashell/resources"
)

// persistStubAdapter replays a scripted sequence of responses so the test
// can assert exactly which messages the review loop persists.
type persistStubAdapter struct {
	responses []ChatResponse
	calls     int
}

func (a *persistStubAdapter) Name() string { return "persist-stub" }

func (a *persistStubAdapter) Chat(_ context.Context, _ *core.Request) (*core.Response, error) {
	if a.calls >= len(a.responses) {
		return nil, context.DeadlineExceeded
	}
	r := a.responses[a.calls]
	a.calls++
	return chatResponseToCore(r), nil
}

func (a *persistStubAdapter) Stream(ctx context.Context, req *core.Request) (core.Stream, error) {
	resp, err := a.Chat(ctx, req)
	if err != nil {
		return nil, err
	}
	return &stubStream{events: coreResponseEvents(resp)}, nil
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
	_, messages, err := agent.runReviewLoop(context.Background(), stubProviderContext(adapter), "model", conv)
	if err != nil {
		t.Fatalf("runReviewLoop: %v", err)
	}

	// Expected shape (newReviewApp sets no user memory store, so only
	// review_transcript is pre-injected — no file_read call):
	//   0: user (prompt from prompts/user/review.md)
	//   1: assistant (synthetic tool call: review_transcript)
	//   2: tool (review_transcript result)
	//   3: assistant (first LLM response: reasoning + memory save tool call)
	//   4: tool (memory save result)
	//   5: assistant (final summary: reasoning + content)
	if len(messages) != 6 {
		t.Fatalf("messages = %d, want 6; got %+v", len(messages), messages)
	}
	if messages[3].Reasoning != "The user stated a durable preference." {
		t.Errorf("round reasoning not persisted: %q", messages[3].Reasoning)
	}
	if messages[4].ToolResult == nil || messages[4].ToolResult.ToolCallID != "tc_1" {
		t.Fatalf("tool result missing: %+v", messages[4])
	}
	if messages[5].Role != "assistant" || messages[5].Content != "Saved one memory fragment." {
		t.Errorf("final summary not persisted as assistant message: %+v", messages[5])
	}
	if messages[5].Reasoning != "Save succeeded, done." {
		t.Errorf("final reasoning not persisted: %q", messages[5].Reasoning)
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
	_, messages, err := agent.runReviewLoop(context.Background(), stubProviderContext(adapter), "model", conv)
	if err != nil {
		t.Fatalf("runReviewLoop: %v", err)
	}
	// Expected shape (newReviewApp sets no user memory store, so only
	// review_transcript is pre-injected — no file_read call):
	//   0: user (prompt)
	//   1: assistant (synthetic tool call: review_transcript)
	//   2: tool (review_transcript result)
	//   3: assistant ("Nothing to save." conclusion)
	if len(messages) != 4 {
		t.Fatalf("messages = %d, want 4 (prompt + synthetic + 1 tool + conclusion); got %+v", len(messages), messages)
	}
	if messages[3].Content != "Nothing to save." {
		t.Errorf("conclusion = %q, want %q", messages[3].Content, "Nothing to save.")
	}
}
