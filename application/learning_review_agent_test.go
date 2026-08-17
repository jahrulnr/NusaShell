package application

import (
	"context"
	"errors"
	"testing"

	"nusashell/domain"
	"nusashell/resources"
)

// reviewStubToolbox is a ToolExecutor stub for the review loop. The fail
// set causes Execute on those tool names to return an error, mimicking e.g.
// a model calling a tool that does not exist.
type reviewStubToolbox struct {
	fail map[string]bool
}

func (s *reviewStubToolbox) ListTools() []ToolInfo {
	return []ToolInfo{
		{Name: "memory_save", Description: "Save a fact"},
		{Name: "skill_save", Description: "Save a skill"},
	}
}

func (s *reviewStubToolbox) Execute(_ context.Context, name string, _ []byte) (string, error) {
	if s.fail[name] {
		return "", errors.New("boom: tool unavailable")
	}
	return "Saved " + name + " entry.", nil
}

// reviewStubAdapter is an AIProvider that returns tool calls for the first
// request and a terminal response afterwards.
type reviewStubAdapter struct {
	toolCalls []domain.ToolCall
	calls     int
}

func (a *reviewStubAdapter) Kind() domain.ProviderKind { return domain.ProviderChat }
func (a *reviewStubAdapter) Stream(context.Context, ChatRequest, func(string), func(string)) (ChatResponse, error) {
	return ChatResponse{}, nil
}

func (a *reviewStubAdapter) Complete(_ context.Context, req ChatRequest) (ChatResponse, error) {
	a.calls++
	if a.calls == 1 {
		return ChatResponse{ToolCalls: a.toolCalls}, nil
	}
	return ChatResponse{Content: "Nothing to save."}, nil
}

func newReviewApp(toolbox *reviewStubToolbox) *App {
	return &App{
		Toolbox: toolbox,
		Logs:    &fakeLogStore{},
	}
}

func TestReviewLoopRecordsMutationOnlyOnSuccess(t *testing.T) {
	if resources.ReviewPrompt() == "" {
		t.Fatal("review prompt must be non-empty for the loop to run")
	}
	conv := &domain.Conversation{
		ID: "conv_1",
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "please remember I prefer Indonesian"},
			{Role: domain.RoleAssistant, Content: "noted"},
		},
	}

	t.Run("successful save is recorded", func(t *testing.T) {
		agent := NewBackgroundReviewAgent(newReviewApp(&reviewStubToolbox{}), DefaultReviewSettings())
		adapter := &reviewStubAdapter{toolCalls: []domain.ToolCall{{
			Name: "memory_save",
			Args: `{"content":"user prefers Indonesian","tags":["preference","language"]}`,
		}}}
		mutations := agent.runReviewLoop(context.Background(), adapter, "model", conv)
		if len(mutations) != 1 || mutations[0].Kind != "memory" {
			t.Fatalf("mutations = %+v, want exactly one memory mutation", mutations)
		}
	})

	t.Run("failed tool is not recorded as a mutation", func(t *testing.T) {
		agent := NewBackgroundReviewAgent(newReviewApp(&reviewStubToolbox{fail: map[string]bool{"memory_save": true}}), DefaultReviewSettings())
		adapter := &reviewStubAdapter{toolCalls: []domain.ToolCall{{
			Name: "memory_save",
			Args: `{"content":"x"}`,
		}}}
		mutations := agent.runReviewLoop(context.Background(), adapter, "model", conv)
		if len(mutations) != 0 {
			t.Fatalf("mutations = %+v, want none when the tool fails", mutations)
		}
	})

	t.Run("non-whitelisted tool is not recorded", func(t *testing.T) {
		agent := NewBackgroundReviewAgent(newReviewApp(&reviewStubToolbox{}), DefaultReviewSettings())
		adapter := &reviewStubAdapter{toolCalls: []domain.ToolCall{{
			Name: "memory", // the (wrong) name referenced by the old prompt
			Args: `{"content":"x"}`,
		}}}
		mutations := agent.runReviewLoop(context.Background(), adapter, "model", conv)
		if len(mutations) != 0 {
			t.Fatalf("mutations = %+v, want none for non-whitelisted tool", mutations)
		}
	})
}
