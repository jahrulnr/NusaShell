package domain

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestMessageEstimateTokensIgnoresUsageAndDoesNotDoubleCountSteps(t *testing.T) {
	content := strings.Repeat("a", 40) // 10 tokens
	msg := Message{
		Content:   content,
		Reasoning: "think", // ignored when steps are present
		Usage:     &Usage{InputTokens: 9000, OutputTokens: 50},
		ToolCalls: []ToolCall{{Name: "docs_search", Args: `{"q":"x"}`, Output: "hit"}},
		Steps: []MessageStep{
			{Type: StepReasoning, Content: "think"},
			{Type: StepText, Content: content},
			{Type: StepToolCalls, ToolCalls: []ToolCall{{Name: "docs_search", Args: `{"q":"x"}`, Output: "hit"}}},
		},
	}

	got := msg.EstimateTokens()
	want := Message{
		Steps: []MessageStep{
			{Type: StepReasoning, Content: "think"},
			{Type: StepText, Content: content},
			{Type: StepToolCalls, ToolCalls: []ToolCall{{Name: "docs_search", Args: `{"q":"x"}`, Output: "hit"}}},
		},
	}
	if got != want.EstimateTokens() {
		t.Fatalf("EstimateTokens() = %d, want %d (usage and mirrored flat fields must not inflate the estimate)", got, want.EstimateTokens())
	}
	if got >= 9000 {
		t.Fatalf("EstimateTokens() = %d includes usage input tokens", got)
	}
}

func TestMessageEstimateTokensFallsBackToFlatFieldsWithoutSteps(t *testing.T) {
	msg := Message{Content: "hello world", Reasoning: "hmm", ToolCalls: []ToolCall{{Name: "todo", Args: "{}"}}}
	if msg.EstimateTokens() <= 0 {
		t.Fatal("expected a positive estimate from flat fields")
	}
	equivalent := Message{Steps: []MessageStep{
		{Type: StepReasoning, Content: "hmm"},
		{Type: StepText, Content: "hello world"},
		{Type: StepToolCalls, ToolCalls: []ToolCall{{Name: "todo", Args: "{}"}}},
	}}
	if msg.EstimateTokens() != equivalent.EstimateTokens() {
		t.Fatal("flat fields and equivalent steps should estimate the same")
	}
}

func TestDefaultTitleDoesNotSplitUTF8Rune(t *testing.T) {
	c := &Conversation{Messages: []Message{{
		Role:    RoleUser,
		Content: strings.Repeat("語", 60),
	}}}
	title := c.DefaultTitle()
	if !utf8.ValidString(title) {
		t.Fatalf("title is invalid UTF-8: %q", title)
	}
	if !strings.HasSuffix(title, "…") {
		t.Fatalf("title = %q, want truncated with ellipsis", title)
	}
}
