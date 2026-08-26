package application

import (
	"testing"

	"nusashell/domain"
	"nusashell/infrastructure/ai/core"
)

// TestReasoningReplayInjectsPlaceholderWhenReasoningEmpty proves Bug 1:
// when ReasoningReplay is true but the prior assistant reasoning is empty
// (stripped, unavailable, or first-turn edge case), the replay must inject
// domain.ReasoningPlaceholder so models that require reasoning_content on
// every assistant message (deepseek, qwen, glm) do not 400 with
// "reasoning_content must be passed back".
func TestReasoningReplayInjectsPlaceholderWhenReasoningEmpty(t *testing.T) {
	req := ChatRequest{
		Model: "deepseek/deepseek-r1",
		Messages: []ChatMessage{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "ok", Reasoning: ""},
		},
		ReasoningReplay: true,
	}
	cr := ToCoreRequest(req, domain.ProviderChat, true)
	var assistant *core.Message
	for i := range cr.Messages {
		if cr.Messages[i].Role == core.RoleAssistant {
			assistant = &cr.Messages[i]
			break
		}
	}
	if assistant == nil {
		t.Fatalf("no assistant message in converted request")
	}
	var hasReasoningBlock bool
	for _, block := range assistant.Blocks {
		if rb, ok := block.(core.ReasoningBlock); ok {
			hasReasoningBlock = true
			if rb.Text != domain.ReasoningPlaceholder {
				t.Fatalf("reasoning block text = %q, want placeholder %q", rb.Text, domain.ReasoningPlaceholder)
			}
		}
	}
	if !hasReasoningBlock {
		t.Fatalf("ReasoningReplay=true with empty reasoning must inject placeholder ReasoningBlock, got blocks: %#v", assistant.Blocks)
	}
}

// TestReasoningReplayKeepsActualReasoning ensures non-empty reasoning is
// forwarded as-is (not replaced by the placeholder).
func TestReasoningReplayKeepsActualReasoning(t *testing.T) {
	req := ChatRequest{
		Model: "deepseek/deepseek-r1",
		Messages: []ChatMessage{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "ok", Reasoning: "I thought about it."},
		},
		ReasoningReplay: true,
	}
	cr := ToCoreRequest(req, domain.ProviderChat, true)
	for _, msg := range cr.Messages {
		if msg.Role != core.RoleAssistant {
			continue
		}
		for _, block := range msg.Blocks {
			if rb, ok := block.(core.ReasoningBlock); ok {
				if rb.Text != "I thought about it." {
					t.Fatalf("reasoning = %q, want original text", rb.Text)
				}
				return
			}
		}
		t.Fatalf("expected ReasoningBlock with original text, got blocks: %#v", msg.Blocks)
	}
}

// TestReasoningReplayOffDoesNotInjectPlaceholder ensures that when
// ReasoningReplay is false, no placeholder or reasoning block is injected
// (providers that don't require replay should not receive reasoning_content).
func TestReasoningReplayOffDoesNotInjectPlaceholder(t *testing.T) {
	req := ChatRequest{
		Model: "openai/gpt-5",
		Messages: []ChatMessage{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "ok", Reasoning: ""},
		},
		ReasoningReplay: false,
	}
	cr := ToCoreRequest(req, domain.ProviderResponses, false)
	for _, msg := range cr.Messages {
		if msg.Role != core.RoleAssistant {
			continue
		}
		for _, block := range msg.Blocks {
			if _, ok := block.(core.ReasoningBlock); ok {
				t.Fatalf("ReasoningReplay=false must not inject any ReasoningBlock, got: %#v", block)
			}
		}
	}
}
