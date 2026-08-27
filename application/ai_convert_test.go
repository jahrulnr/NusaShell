package application

import (
	"fmt"
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
// ReasoningReplay is false and reasoning is empty, no placeholder or
// reasoning block is injected.
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
				t.Fatalf("ReasoningReplay=false with empty reasoning must not inject any ReasoningBlock, got: %#v", block)
			}
		}
	}
}

// TestReasoningSentEvenWhenReplayOff proves that reasoning from the
// persisted conversation is always sent when present, regardless of the
// ReasoningReplay flag. This is the conversation-store-as-source-of-truth
// approach: models that intermittently think (task-dependent reasoning)
// store reasoning on turns where they did think, and those turns replay
// correctly. Non-reasoning models safely ignore the reasoning field
// (verified against OpenRouter: minimax-m3 accepts reasoning without error).
//
// This fixes the original bug where reasoning was captured from the
// response but silently dropped on the next turn's replay because
// ReasoningReplay was false (model not in the catalog whitelist).
func TestReasoningSentEvenWhenReplayOff(t *testing.T) {
	req := ChatRequest{
		Model: "openrouter/some-glm-variant",
		Messages: []ChatMessage{
			{Role: "user", Content: "What is 2+2?"},
			{Role: "assistant", Content: "Four", Reasoning: "User asks 2+2. Answer is 4."},
			{Role: "user", Content: "What is 3+3?"},
		},
		ReasoningReplay: false, // not in catalog whitelist
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
	var reasoningBlock *core.ReasoningBlock
	for i, block := range assistant.Blocks {
		if rb, ok := block.(core.ReasoningBlock); ok {
			reasoningBlock = &rb
			_ = i
			break
		}
	}
	if reasoningBlock == nil {
		t.Fatalf("expected ReasoningBlock with persisted reasoning text even when ReasoningReplay=false, got blocks: %#v", assistant.Blocks)
	}
	if reasoningBlock.Text != "User asks 2+2. Answer is 4." {
		t.Fatalf("reasoning = %q, want original persisted text", reasoningBlock.Text)
	}
}

// TestReasoningSentForRoutingModel proves that reasoning from persisted
// conversation is sent even for routing models (openrouter/auto). The
// conversation store tracks reasoning per-turn, so even if the underlying
// model changes between turns, the reasoning from the turn that produced
// it is replayed. Non-reasoning routes safely ignore the field.
func TestReasoningSentForRoutingModel(t *testing.T) {
	req := ChatRequest{
		Model: "openrouter/auto",
		Messages: []ChatMessage{
			{Role: "user", Content: "What is 2+2?"},
			{Role: "assistant", Content: "Four", Reasoning: "User asks 2+2. Answer is 4."},
			{Role: "user", Content: "hi"}, // simple chit-chat, no reasoning expected
		},
		ReasoningReplay: false,
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
	var hasReasoning bool
	for _, block := range assistant.Blocks {
		if rb, ok := block.(core.ReasoningBlock); ok {
			hasReasoning = true
			if rb.Text != "User asks 2+2. Answer is 4." {
				t.Fatalf("reasoning = %q, want original persisted text", rb.Text)
			}
		}
	}
	if !hasReasoning {
		t.Fatalf("routing model should still receive persisted reasoning, got blocks: %#v", assistant.Blocks)
	}
}

// TestAssistantBlockCombinations proves that all combinations of
// reasoning/text/tool blocks in assistant messages are converted correctly:
//   - reasoning only
//   - text only
//   - tool only
//   - reasoning + text
//   - reasoning + tool
//   - text + tool
//   - reasoning + text + tool
//
// The key invariant: ReasoningBlock must always be first (Anthropic requires
// thinking blocks to be the first block in an assistant message). Every
// combination must produce valid blocks without errors.
func TestAssistantBlockCombinations(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		reasoning string
		toolCalls []domain.ToolCall
		wantOrder []string // block type names in expected order
	}{
		{
			name:      "reasoning only",
			reasoning: "I thought about it.",
			wantOrder: []string{"ReasoningBlock"},
		},
		{
			name:      "text only",
			content:   "Hello!",
			wantOrder: []string{"TextBlock"},
		},
		{
			name:      "tool only",
			toolCalls: []domain.ToolCall{{ID: "call_1", Name: "calc", Args: "{}"}},
			wantOrder: []string{"ToolUseBlock"},
		},
		{
			name:      "reasoning + text",
			reasoning: "Thinking...",
			content:   "Answer.",
			wantOrder: []string{"ReasoningBlock", "TextBlock"},
		},
		{
			name:      "reasoning + tool",
			reasoning: "Need to calculate.",
			toolCalls: []domain.ToolCall{{ID: "call_1", Name: "calc", Args: "{}"}},
			wantOrder: []string{"ReasoningBlock", "ToolUseBlock"},
		},
		{
			name:      "text + tool",
			content:   "Let me check.",
			toolCalls: []domain.ToolCall{{ID: "call_1", Name: "calc", Args: "{}"}},
			wantOrder: []string{"TextBlock", "ToolUseBlock"},
		},
		{
			name:      "reasoning + text + tool",
			reasoning: "I should use a tool.",
			content:   "Let me calculate.",
			toolCalls: []domain.ToolCall{{ID: "call_1", Name: "calc", Args: "{}"}},
			wantOrder: []string{"ReasoningBlock", "TextBlock", "ToolUseBlock"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := ChatRequest{
				Model: "test/model",
				Messages: []ChatMessage{
					{Role: "user", Content: "hi"},
					{Role: "assistant", Content: tt.content, Reasoning: tt.reasoning, ToolCalls: tt.toolCalls},
				},
				ReasoningReplay: false,
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
				t.Fatalf("no assistant message found")
			}

			if len(assistant.Blocks) != len(tt.wantOrder) {
				t.Fatalf("block count = %d, want %d. Blocks: %#v", len(assistant.Blocks), len(tt.wantOrder), assistant.Blocks)
			}

			for i, want := range tt.wantOrder {
				got := blockTypeName(assistant.Blocks[i])
				if got != want {
					t.Errorf("block[%d] = %s, want %s", i, got, want)
				}
			}
		})
	}
}

// TestReasoningBlockAlwaysFirstForAnthropic proves the Anthropic ordering
// requirement: if a ReasoningBlock is present, it must be the first block.
// Anthropic rejects with 400: "If an assistant message contains any thinking
// blocks, the first block must be `thinking` or `redacted_thinking`".
func TestReasoningBlockAlwaysFirstForAnthropic(t *testing.T) {
	req := ChatRequest{
		Model: "claude-sonnet-4-6",
		Messages: []ChatMessage{
			{Role: "user", Content: "What is 2+2?"},
			{Role: "assistant", Content: "Four", Reasoning: "User asks 2+2. Answer is 4.", ToolCalls: []domain.ToolCall{{ID: "c1", Name: "verify", Args: "{}"}}},
			{Role: "user", Content: "Thanks!"},
		},
	}
	cr := ToCoreRequest(req, domain.ProviderMessages, false)

	var assistant *core.Message
	for i := range cr.Messages {
		if cr.Messages[i].Role == core.RoleAssistant {
			assistant = &cr.Messages[i]
			break
		}
	}
	if assistant == nil {
		t.Fatalf("no assistant message found")
	}
	if len(assistant.Blocks) == 0 {
		t.Fatalf("expected blocks, got none")
	}
	if _, ok := assistant.Blocks[0].(core.ReasoningBlock); !ok {
		t.Fatalf("first block must be ReasoningBlock for Anthropic, got %T: %#v", assistant.Blocks[0], assistant.Blocks[0])
	}
}

func blockTypeName(b core.Block) string {
	switch b.(type) {
	case core.ReasoningBlock:
		return "ReasoningBlock"
	case core.TextBlock:
		return "TextBlock"
	case core.ToolUseBlock:
		return "ToolUseBlock"
	default:
		return fmt.Sprintf("%T", b)
	}
}

func TestToCoreRequestCopiesToolChoice(t *testing.T) {
	choice := map[string]any{"type": "function", "function": map[string]any{"name": "summary"}}
	cr := ToCoreRequest(ChatRequest{Model: "m", ToolChoice: choice}, domain.ProviderChat, false)
	if cr.ToolChoice == nil {
		t.Fatal("ToolChoice dropped during conversion")
	}
}
