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

// TestMessageEstimateTokensImageNotCountedByBase64Size: image attachments
// must not be counted by their base64 data URL length. A 1MB image encodes
// to ~1.3MB base64, which at chars/4 would be ~330k tokens — far more than
// any provider actually charges (typically 765-1000 tokens for a 1024x1024
// image). The estimate must use a resolution-based heuristic instead.
func TestMessageEstimateTokensImageNotCountedByBase64Size(t *testing.T) {
	// Simulate a 1MB image as a base64 data URL (~1.3MB of base64 chars).
	bigBase64 := "data:image/png;base64," + strings.Repeat("A", 1_300_000)
	msg := Message{
		Content: "What is this?",
		Attachments: []Attachment{
			{Type: "image", Name: "big.png", MediaType: "image/png", DataURL: bigBase64},
		},
	}
	got := msg.EstimateTokens()
	// "What is this?" = 14 chars = ~4 tokens. Image should add a small
	// fixed cost (hundreds, not hundreds of thousands).
	if got > 5000 {
		t.Fatalf("EstimateTokens() = %d for one image — should be resolution-based (~hundreds), not base64-char-based (~330k)", got)
	}
	if got < 100 {
		t.Fatalf("EstimateTokens() = %d — image should contribute a non-trivial token estimate", got)
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
