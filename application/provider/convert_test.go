package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

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

func TestStreamIdleTimeoutUsesCodexUpstreamWindowForRemoteCompaction(t *testing.T) {
	if got := streamIdleTimeout(ChatRequest{RemoteCompaction: true}, domain.ProviderCodex); got != 5*time.Minute {
		t.Fatalf("Codex remote compaction timeout = %s, want 5m", got)
	}
	if got := streamIdleTimeout(ChatRequest{RemoteCompaction: false}, domain.ProviderCodex); got != 60*time.Second {
		t.Fatalf("ordinary Codex stream timeout = %s, want 1m", got)
	}
	if got := streamIdleTimeout(ChatRequest{RemoteCompaction: true}, domain.ProviderResponses); got != 60*time.Second {
		t.Fatalf("non-Codex remote compaction timeout = %s, want 1m", got)
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
		kind      domain.ProviderKind
		content   string
		reasoning string
		toolCalls []domain.ToolCall
		wantOrder []string // block type names in expected order
	}{
		{
			// Reasoning-only is a legal standalone item on Responses;
			// message-shaped wires (Chat/Messages/Gemini) drop it because
			// they require content or tool calls on every assistant entry.
			name:      "reasoning only",
			kind:      domain.ProviderResponses,
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
			kind := tt.kind
			if kind == "" {
				kind = domain.ProviderChat
			}
			req := ChatRequest{
				Model: "test/model",
				Messages: []ChatMessage{
					{Role: "user", Content: "hi"},
					{Role: "assistant", Content: tt.content, Reasoning: tt.reasoning, ToolCalls: tt.toolCalls},
				},
				ReasoningReplay: false,
			}
			cr := ToCoreRequest(req, kind, true)

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
// blocks, the first block must be `thinking` or `redacted_thinking`". The
// message carries the Anthropic-shaped replay extra a signed thinking block
// is emitted from.
func TestReasoningBlockAlwaysFirstForAnthropic(t *testing.T) {
	req := ChatRequest{
		Model: "claude-sonnet-4-6",
		Messages: []ChatMessage{
			{Role: "user", Content: "What is 2+2?"},
			{Role: "assistant", Content: "Four", Reasoning: "User asks 2+2. Answer is 4.", ReasoningExtra: json.RawMessage(`{"signature":"sig-1"}`), ReasoningExtraFor: "anthropic", ToolCalls: []domain.ToolCall{{ID: "c1", Name: "verify", Args: "{}"}}},
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

func TestToCoreRequestDropsEmptyAssistantPlaceholderAfterParallelTools(t *testing.T) {
	calls := []domain.ToolCall{
		{ID: "call_1", Name: "file_read", Args: `{}`},
		{ID: "call_2", Name: "file_list", Args: `{}`},
		{ID: "call_3", Name: "grep", Args: `{}`},
		{ID: "call_4", Name: "exec", Args: `{}`},
	}
	messages := []ChatMessage{
		{Role: "user", Content: "inspect the project"},
		{Role: "assistant", ToolCalls: calls},
		{Role: "tool", ToolResult: &ToolResult{ToolCallID: "call_1", Content: "file"}},
		{Role: "tool", ToolResult: &ToolResult{ToolCallID: "call_2", Content: "listing"}},
		{Role: "tool", ToolResult: &ToolResult{ToolCallID: "call_3", Content: "matches"}},
		{Role: "tool", ToolResult: &ToolResult{ToolCallID: "call_4", Content: "done"}},
		// A fresh assistant placeholder may be present while the next
		// round is being prepared. It is not a provider-visible message.
		{Role: "assistant"},
	}

	cr := ToCoreRequest(ChatRequest{
		Model:           "deepseek-v4.1-flash",
		Messages:        messages,
		ReasoningReplay: true,
	}, domain.ProviderChat, false)
	assistantCount := 0
	for _, message := range cr.Messages {
		if message.Role == core.RoleAssistant {
			assistantCount++
		}
	}
	if assistantCount != 1 {
		t.Fatalf("assistant messages = %d, want only the assistant tool-call batch; messages=%+v", assistantCount, cr.Messages)
	}
}

func TestToCoreRequestDropsAssistantWhenChatReasoningExtraIsStripped(t *testing.T) {
	cr := ToCoreRequest(ChatRequest{
		Messages: []ChatMessage{{
			Role:           "assistant",
			ReasoningExtra: json.RawMessage(`{"type":"reasoning","encrypted_content":"opaque"}`),
		}},
	}, domain.ProviderChat, false)
	if len(cr.Messages) != 0 {
		t.Fatalf("messages = %+v, want provider-extra-only assistant to be omitted", cr.Messages)
	}
}

// TestToCoreRequestDropsReasoningOnlyAssistantOnChat reproduces the
// Console Go 400 "Invalid assistant message: content or tool_calls must be
// set". An interrupted thinking stream persists an assistant message with
// reasoning but no text and no tool calls; on message-shaped wires (Chat,
// Messages, Gemini) it serializes to reasoning_content alone, which strict
// gateways reject. The reasoning-only entry must be dropped while the
// following real answer survives.
func TestToCoreRequestDropsReasoningOnlyAssistantOnChat(t *testing.T) {
	messages := []ChatMessage{
		{Role: "user", Content: "fix the scroll bug"},
		{Role: "assistant", Reasoning: "**Investigating scrollBy**\n\npartial thinking"},
		{Role: "assistant", Content: "the real answer"},
	}
	for _, kind := range []domain.ProviderKind{domain.ProviderChat, domain.ProviderMessages, domain.ProviderGemini} {
		cr := ToCoreRequest(ChatRequest{Model: "deepseek-flash", Messages: messages, ReasoningReplay: true}, kind, false)
		assistants := 0
		for _, m := range cr.Messages {
			if m.Role == core.RoleAssistant {
				assistants++
				if len(m.Blocks) == 1 {
					if _, ok := m.Blocks[0].(core.ReasoningBlock); ok {
						t.Fatalf("kind %s kept a reasoning-only assistant message", kind)
					}
				}
			}
		}
		if assistants != 1 {
			t.Fatalf("kind %s assistant messages = %d, want only the real answer", kind, assistants)
		}
	}
}

// TestToCoreRequestKeepsReasoningOnlyAssistantOnItemWires proves the
// drop above is kind-gated: on Responses/Codex a reasoning item is a
// first-class input item, so a reasoning-only assistant entry remains
// legal and keeps encrypted replay state alive.
func TestToCoreRequestKeepsReasoningOnlyAssistantOnItemWires(t *testing.T) {
	messages := []ChatMessage{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Reasoning: "thinking only"},
	}
	for _, kind := range []domain.ProviderKind{domain.ProviderResponses, domain.ProviderCodex} {
		cr := ToCoreRequest(ChatRequest{Model: "gpt-5.6-luna", Messages: messages}, kind, false)
		found := false
		for _, m := range cr.Messages {
			if m.Role != core.RoleAssistant {
				continue
			}
			found = true
			if _, ok := m.Blocks[0].(core.ReasoningBlock); !ok {
				t.Fatalf("kind %s assistant blocks = %#v, want ReasoningBlock", kind, m.Blocks)
			}
		}
		if !found {
			t.Fatalf("kind %s dropped the reasoning-only assistant message", kind)
		}
	}
}

// TestToCoreRequestDropsWhitespaceContentAssistantOnChat covers the
// companion edge: whitespace-only text survives emptyAssistantMessage when
// reasoning is present, but the wire drops the empty text part and the
// entry is left reasoning-only again.
func TestToCoreRequestDropsWhitespaceContentAssistantOnChat(t *testing.T) {
	cr := ToCoreRequest(ChatRequest{
		Model: "deepseek-flash",
		Messages: []ChatMessage{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Reasoning: "partial thinking", Content: "   \n"},
			{Role: "assistant", Content: "answer"},
		},
	}, domain.ProviderChat, false)
	for _, m := range cr.Messages {
		if m.Role != core.RoleAssistant {
			continue
		}
		onlyReasoning := true
		for _, b := range m.Blocks {
			if tb, ok := b.(core.TextBlock); ok && strings.TrimSpace(tb.Text) != "" {
				onlyReasoning = false
			}
			if _, ok := b.(core.ToolUseBlock); ok {
				onlyReasoning = false
			}
		}
		if onlyReasoning {
			t.Fatalf("kept a reasoning-only assistant message: %#v", m.Blocks)
		}
	}
}

// TestReasoningExtraReplayPreservesEncryptedContent proves OpenAI-family
// encrypted reasoning state survives ChatMessage → core.ReasoningBlock so
// Responses/Codex/compat adapters can echo Extra byte-for-byte.
func TestReasoningExtraReplayPreservesEncryptedContent(t *testing.T) {
	extra := json.RawMessage(`{"id":"rs_1","type":"reasoning","encrypted_content":"ENC-REASON","summary":[{"type":"summary_text","text":"think"}]}`)
	cr := ToCoreRequest(ChatRequest{
		Model: "gpt-5.6-sol",
		Messages: []ChatMessage{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "ok", Reasoning: "think", ReasoningExtra: extra},
		},
	}, domain.ProviderResponses, false)

	var got *core.ReasoningBlock
	for _, msg := range cr.Messages {
		if msg.Role != core.RoleAssistant {
			continue
		}
		for _, block := range msg.Blocks {
			if rb, ok := block.(core.ReasoningBlock); ok {
				got = &rb
			}
		}
	}
	if got == nil {
		t.Fatal("expected ReasoningBlock on assistant message")
	}
	if got.Text != "think" {
		t.Fatalf("Text = %q, want think", got.Text)
	}
	if string(got.Extra) != string(extra) {
		t.Fatalf("Extra = %s, want %s", got.Extra, extra)
	}
}

func TestReasoningExtraAloneCreatesReasoningBlock(t *testing.T) {
	extra := json.RawMessage(`{"type":"reasoning","encrypted_content":"ENC-ONLY"}`)
	cr := ToCoreRequest(ChatRequest{
		Model: "gpt-5.6-sol",
		Messages: []ChatMessage{
			{Role: "assistant", ReasoningExtra: extra},
		},
	}, domain.ProviderResponses, false)
	rb, ok := cr.Messages[0].Blocks[0].(core.ReasoningBlock)
	if !ok {
		t.Fatalf("blocks[0] = %#v, want ReasoningBlock", cr.Messages[0].Blocks[0])
	}
	if rb.Text != "" || string(rb.Extra) != string(extra) {
		t.Fatalf("ReasoningBlock = %#v", rb)
	}
}

// TestChatKindStripsCodexReasoningExtra keeps plaintext reasoning when a
// Codex/Responses room is continued on OpenAI Chat (e.g. OpenCode DeepSeek).
// Chat wire rejects Extra and would fail the turn before the request leaves.
func TestChatKindStripsCodexReasoningExtra(t *testing.T) {
	extra := json.RawMessage(`{"id":"rs_1","type":"reasoning","encrypted_content":"ENC-REASON","summary":[{"type":"summary_text","text":"think"}]}`)
	cr := ToCoreRequest(ChatRequest{
		Model: "deepseek-v4-flash",
		Messages: []ChatMessage{
			{Role: "assistant", Content: "ok", Reasoning: "think", ReasoningExtra: extra},
		},
	}, domain.ProviderChat, false)

	rb, ok := cr.Messages[0].Blocks[0].(core.ReasoningBlock)
	if !ok {
		t.Fatalf("blocks[0] = %#v, want ReasoningBlock with plaintext", cr.Messages[0].Blocks[0])
	}
	if rb.Text != "think" {
		t.Fatalf("Text = %q, want think", rb.Text)
	}
	if len(rb.Extra) != 0 {
		t.Fatalf("Extra = %s, want stripped for chat kind", rb.Extra)
	}
}

func TestChatKindDropsExtraOnlyReasoningWithoutReplay(t *testing.T) {
	extra := json.RawMessage(`{"type":"reasoning","encrypted_content":"ENC-ONLY"}`)
	cr := ToCoreRequest(ChatRequest{
		Model: "deepseek-v4-flash",
		Messages: []ChatMessage{
			{Role: "assistant", Content: "ok", ReasoningExtra: extra},
		},
	}, domain.ProviderChat, false)
	for _, block := range cr.Messages[0].Blocks {
		if _, ok := block.(core.ReasoningBlock); ok {
			t.Fatalf("chat must not emit Extra-only ReasoningBlock, got %#v", block)
		}
	}
}

// TestMessagesKindStripsCodexReasoningExtra proves foreign encrypted
// reasoning items never reach the Anthropic Messages wire: the extra has no
// Anthropic signature, so no unsigned thinking block may be emitted either.
// The visible assistant text still survives.
func TestMessagesKindStripsCodexReasoningExtra(t *testing.T) {
	extra := json.RawMessage(`{"type":"reasoning","encrypted_content":"ENC-REASON"}`)
	cr := ToCoreRequest(ChatRequest{
		Model: "claude-sonnet-4-6",
		Messages: []ChatMessage{
			{Role: "assistant", Content: "ok", Reasoning: "think", ReasoningExtra: extra},
		},
	}, domain.ProviderMessages, false)
	if len(cr.Messages) != 1 {
		t.Fatalf("messages = %+v, want the assistant entry kept", cr.Messages)
	}
	for _, block := range cr.Messages[0].Blocks {
		if rb, ok := block.(core.ReasoningBlock); ok {
			t.Fatalf("ReasoningBlock = %#v, want unsigned reasoning dropped for messages", rb)
		}
	}
	tb, ok := cr.Messages[0].Blocks[0].(core.TextBlock)
	if !ok || tb.Text != "ok" {
		t.Fatalf("blocks[0] = %#v, want TextBlock %q", cr.Messages[0].Blocks[0], "ok")
	}
}

// TestMessagesKindReplaysAnthropicReasoningExtra proves an anthropic-marked
// opaque extra survives the app boundary onto the ReasoningBlock, so the
// wire adapter can echo a signed thinking block.
func TestMessagesKindReplaysAnthropicReasoningExtra(t *testing.T) {
	extra := json.RawMessage(`{"signature":"sig-1"}`)
	cr := ToCoreRequest(ChatRequest{
		Model: "claude-sonnet-4-6",
		Messages: []ChatMessage{
			{Role: "assistant", Content: "ok", Reasoning: "think", ReasoningExtra: extra, ReasoningExtraFor: "anthropic"},
		},
	}, domain.ProviderMessages, false)
	rb, ok := cr.Messages[0].Blocks[0].(core.ReasoningBlock)
	if !ok {
		t.Fatalf("blocks[0] = %#v, want ReasoningBlock", cr.Messages[0].Blocks[0])
	}
	if rb.Text != "think" || string(rb.Extra) != string(extra) {
		t.Fatalf("ReasoningBlock = %#v, want text plus anthropic extra", rb)
	}
}

// TestMessagesKindReplaysLegacyAnthropicShapedExtra proves the unmarked
// (pre-marker) path keeps working: an extra with an Anthropic replay key is
// still forwarded to Messages on shape alone.
func TestMessagesKindReplaysLegacyAnthropicShapedExtra(t *testing.T) {
	extra := json.RawMessage(`{"signature":"sig-legacy"}`)
	cr := ToCoreRequest(ChatRequest{
		Model: "claude-sonnet-4-6",
		Messages: []ChatMessage{
			{Role: "assistant", Content: "ok", Reasoning: "think", ReasoningExtra: extra},
		},
	}, domain.ProviderMessages, false)
	rb, ok := cr.Messages[0].Blocks[0].(core.ReasoningBlock)
	if !ok {
		t.Fatalf("blocks[0] = %#v, want ReasoningBlock", cr.Messages[0].Blocks[0])
	}
	if string(rb.Extra) != string(extra) {
		t.Fatalf("Extra = %s, want anthropic-shaped legacy extra kept", rb.Extra)
	}
}

// TestMessagesKindReplaysRedactedReasoningExtra proves a redacted_thinking
// payload is treated as a valid Anthropic replay state even without a
// signature.
func TestMessagesKindReplaysRedactedReasoningExtra(t *testing.T) {
	extra := json.RawMessage(`{"redacted":"opaque-data"}`)
	cr := ToCoreRequest(ChatRequest{
		Model: "claude-sonnet-4-6",
		Messages: []ChatMessage{
			{Role: "assistant", Content: "ok", ReasoningExtra: extra, ReasoningExtraFor: "anthropic"},
		},
	}, domain.ProviderMessages, false)
	rb, ok := cr.Messages[0].Blocks[0].(core.ReasoningBlock)
	if !ok {
		t.Fatalf("blocks[0] = %#v, want ReasoningBlock", cr.Messages[0].Blocks[0])
	}
	if string(rb.Extra) != string(extra) {
		t.Fatalf("Extra = %s, want redacted payload kept", rb.Extra)
	}
}

// TestMessagesKindDropsUnsignedReasoning proves plaintext-only persisted
// reasoning is not emitted as an unsigned Anthropic thinking block — the
// provider would reject it with a 400.
func TestMessagesKindDropsUnsignedReasoning(t *testing.T) {
	cr := ToCoreRequest(ChatRequest{
		Model: "claude-sonnet-4-6",
		Messages: []ChatMessage{
			{Role: "assistant", Content: "ok", Reasoning: "think"},
		},
	}, domain.ProviderMessages, false)
	for _, block := range cr.Messages[0].Blocks {
		if rb, ok := block.(core.ReasoningBlock); ok {
			t.Fatalf("ReasoningBlock = %#v, want plaintext reasoning dropped for messages", rb)
		}
	}
}

// TestMessagesKindEmitsSignedReasoningWithoutText proves a signed extra
// produces a thinking block even when the visible reasoning text is empty
// (Anthropic display omitted); the tool-loop protocol requires an assistant
// message to start with a thinking block.
func TestMessagesKindEmitsSignedReasoningWithoutText(t *testing.T) {
	extra := json.RawMessage(`{"signature":"sig-1"}`)
	cr := ToCoreRequest(ChatRequest{
		Model: "claude-sonnet-4-6",
		Messages: []ChatMessage{
			{Role: "assistant", Content: "ok", ReasoningExtra: extra, ReasoningExtraFor: "anthropic", ToolCalls: []domain.ToolCall{{ID: "c1", Name: "run", Args: "{}"}}},
		},
	}, domain.ProviderMessages, false)
	rb, ok := cr.Messages[0].Blocks[0].(core.ReasoningBlock)
	if !ok {
		t.Fatalf("blocks[0] = %#v, want ReasoningBlock from signed extra", cr.Messages[0].Blocks[0])
	}
	if rb.Text != "" || string(rb.Extra) != string(extra) {
		t.Fatalf("ReasoningBlock = %#v, want empty text plus signature extra", rb)
	}
}

// TestMessagesKindDropsForeignMarkedExtra proves the producer marker wins
// over payload shape: an Anthropic-shaped extra recorded as coming from
// another family is still dropped.
func TestMessagesKindDropsForeignMarkedExtra(t *testing.T) {
	extra := json.RawMessage(`{"signature":"sig-1"}`)
	cr := ToCoreRequest(ChatRequest{
		Model: "claude-sonnet-4-6",
		Messages: []ChatMessage{
			{Role: "assistant", Content: "ok", Reasoning: "think", ReasoningExtra: extra, ReasoningExtraFor: "codex"},
		},
	}, domain.ProviderMessages, false)
	for _, block := range cr.Messages[0].Blocks {
		if rb, ok := block.(core.ReasoningBlock); ok {
			t.Fatalf("ReasoningBlock = %#v, want foreign-marked extra dropped", rb)
		}
	}
}

// TestReasoningExtraForMismatchStripsOtherKinds proves the producer marker
// gates every wire, not just Messages: an extra whose recorded family does
// not match the target wire family is stripped even when its shape would
// otherwise pass the per-kind rule.
func TestReasoningExtraForMismatchStripsOtherKinds(t *testing.T) {
	arrayExtra := json.RawMessage(`[{"type":"reasoning.text","text":"step"}]`)
	objectExtra := json.RawMessage(`{"type":"reasoning","encrypted_content":"ENC"}`)
	anthropicExtra := json.RawMessage(`{"signature":"sig-1"}`)
	tests := []struct {
		name       string
		kind       domain.ProviderKind
		openRouter bool
		extra      json.RawMessage
		extraFor   string
	}{
		{name: "openai-marked array on openrouter chat", kind: domain.ProviderChat, openRouter: true, extra: arrayExtra, extraFor: "openai"},
		{name: "openrouter-marked array on responses", kind: domain.ProviderResponses, extra: arrayExtra, extraFor: "openrouter"},
		{name: "anthropic-marked extra on responses", kind: domain.ProviderResponses, extra: anthropicExtra, extraFor: "anthropic"},
		{name: "codex-marked extra on responses", kind: domain.ProviderResponses, extra: objectExtra, extraFor: "codex"},
		{name: "openai-marked extra on codex", kind: domain.ProviderCodex, extra: objectExtra, extraFor: "openai"},
		{name: "anthropic-marked extra on gemini", kind: domain.ProviderGemini, extra: anthropicExtra, extraFor: "anthropic"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cr := ToCoreRequest(ChatRequest{
				Model: "model",
				Messages: []ChatMessage{
					{Role: "assistant", Content: "ok", Reasoning: "think", ReasoningExtra: tt.extra, ReasoningExtraFor: tt.extraFor},
				},
			}, tt.kind, tt.openRouter)
			var rb core.ReasoningBlock
			var found bool
			for _, message := range cr.Messages {
				for _, block := range message.Blocks {
					if candidate, ok := block.(core.ReasoningBlock); ok {
						rb = candidate
						found = true
					}
				}
			}
			if !found {
				t.Fatalf("no ReasoningBlock converted; blocks = %+v", cr.Messages)
			}
			if len(rb.Extra) != 0 {
				t.Fatalf("Extra = %s, want stripped for producer family %q on kind %s", rb.Extra, tt.extraFor, tt.kind)
			}
		})
	}
}

// TestReasoningExtraForMatchingProducerKept proves a marker matching the
// target family forwards the extra through the normal per-kind rules.
func TestReasoningExtraForMatchingProducerKept(t *testing.T) {
	tests := []struct {
		name       string
		kind       domain.ProviderKind
		openRouter bool
		extra      json.RawMessage
		extraFor   string
	}{
		{name: "anthropic on messages", kind: domain.ProviderMessages, extra: json.RawMessage(`{"signature":"sig-1"}`), extraFor: "anthropic"},
		{name: "openai on responses", kind: domain.ProviderResponses, extra: json.RawMessage(`{"type":"reasoning","encrypted_content":"ENC"}`), extraFor: "openai"},
		{name: "codex on codex", kind: domain.ProviderCodex, extra: json.RawMessage(`{"type":"reasoning","encrypted_content":"ENC"}`), extraFor: "codex"},
		{name: "openrouter on openrouter chat", kind: domain.ProviderChat, openRouter: true, extra: json.RawMessage(`[{"type":"reasoning.text","text":"step"}]`), extraFor: "openrouter"},
		{name: "gemini on gemini", kind: domain.ProviderGemini, extra: json.RawMessage(`{"thought_signature":"sig"}`), extraFor: "gemini"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cr := ToCoreRequest(ChatRequest{
				Model: "model",
				Messages: []ChatMessage{
					{Role: "assistant", Content: "ok", Reasoning: "think", ReasoningExtra: tt.extra, ReasoningExtraFor: tt.extraFor},
				},
			}, tt.kind, tt.openRouter)
			rb, ok := cr.Messages[0].Blocks[0].(core.ReasoningBlock)
			if !ok {
				t.Fatalf("blocks[0] = %#v, want ReasoningBlock", cr.Messages[0].Blocks[0])
			}
			if string(rb.Extra) != string(tt.extra) {
				t.Fatalf("Extra = %s, want %s for matching producer family", rb.Extra, tt.extra)
			}
		})
	}
}

func TestOpenRouterChatKeepsArrayReasoningExtra(t *testing.T) {
	extra := json.RawMessage(`[{"type":"reasoning.text","text":"step 1"}]`)
	cr := ToCoreRequest(ChatRequest{
		Model: "openrouter/free",
		Messages: []ChatMessage{
			{Role: "assistant", Content: "ok", Reasoning: "step 1", ReasoningExtra: extra},
		},
	}, domain.ProviderChat, true)
	rb, ok := cr.Messages[0].Blocks[0].(core.ReasoningBlock)
	if !ok {
		t.Fatalf("blocks[0] = %#v, want ReasoningBlock", cr.Messages[0].Blocks[0])
	}
	if string(rb.Extra) != string(extra) {
		t.Fatalf("Extra = %s, want openrouter reasoning_details array kept", rb.Extra)
	}
}

func TestOpenRouterChatStripsCodexObjectReasoningExtra(t *testing.T) {
	extra := json.RawMessage(`{"type":"reasoning","encrypted_content":"ENC-REASON"}`)
	cr := ToCoreRequest(ChatRequest{
		Model: "openrouter/free",
		Messages: []ChatMessage{
			{Role: "assistant", Content: "ok", Reasoning: "think", ReasoningExtra: extra},
		},
	}, domain.ProviderChat, true)
	rb, ok := cr.Messages[0].Blocks[0].(core.ReasoningBlock)
	if !ok {
		t.Fatalf("blocks[0] = %#v, want ReasoningBlock", cr.Messages[0].Blocks[0])
	}
	if len(rb.Extra) != 0 {
		t.Fatalf("Extra = %s, want Codex object stripped on OpenRouter chat", rb.Extra)
	}
}

// TestReasoningExtraProviderInstanceGate proves an opaque extra replays only
// to the provider instance that produced it: two credentials on the same
// wire family must not inherit each other's encrypted reasoning state.
// Empty on either side is legacy data and never gates.
func TestReasoningExtraProviderInstanceGate(t *testing.T) {
	extra := json.RawMessage(`{"type":"reasoning","encrypted_content":"ENC"}`)
	tests := []struct {
		name      string
		msgProvID string
		reqProvID string
		wantExtra bool
	}{
		{name: "mismatch drops", msgProvID: "openai-a", reqProvID: "openai-b", wantExtra: false},
		{name: "match keeps", msgProvID: "openai-a", reqProvID: "openai-a", wantExtra: true},
		{name: "empty message id keeps legacy", msgProvID: "", reqProvID: "openai-b", wantExtra: true},
		{name: "empty request id keeps legacy", msgProvID: "openai-a", reqProvID: "", wantExtra: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cr := ToCoreRequest(ChatRequest{
				Model:      "gpt-5.6-sol",
				ProviderID: tt.reqProvID,
				Messages: []ChatMessage{
					{Role: "assistant", Content: "ok", Reasoning: "think", ReasoningExtra: extra, ReasoningExtraFor: "openai", ProviderID: tt.msgProvID},
				},
			}, domain.ProviderResponses, false)
			rb, ok := cr.Messages[0].Blocks[0].(core.ReasoningBlock)
			if !ok {
				t.Fatalf("blocks[0] = %#v, want ReasoningBlock", cr.Messages[0].Blocks[0])
			}
			if tt.wantExtra && string(rb.Extra) != string(extra) {
				t.Fatalf("Extra = %s, want %s kept for matching/legacy provider ids", rb.Extra, extra)
			}
			if !tt.wantExtra && len(rb.Extra) != 0 {
				t.Fatalf("Extra = %s, want dropped for provider instance %q → %q", rb.Extra, tt.msgProvID, tt.reqProvID)
			}
		})
	}
}

// TestReasoningExtraUpstreamRouteGate proves a captured OpenRouter upstream
// ("provider" field, e.g. "Nvidia") only replays when the request does not
// pin a different provider route. Replaying another upstream's details under
// a pinned route sends that upstream's opaque payload to a different
// provider — which rejects or misinterprets it. Auto-routed requests and
// extras with no recorded upstream never gate.
func TestReasoningExtraUpstreamRouteGate(t *testing.T) {
	extra := json.RawMessage(`[{"type":"reasoning.text","text":"step"}]`)
	tests := []struct {
		name     string
		upstream string
		route    string
		wantKeep bool
	}{
		{name: "pinned route matching upstream keeps", upstream: "Nvidia", route: "Nvidia", wantKeep: true},
		{name: "pinned route different case keeps", upstream: "Nvidia", route: "nvidia", wantKeep: true},
		{name: "pinned route mismatch drops", upstream: "Nvidia", route: "Wafer", wantKeep: false},
		{name: "slug spelling of display name keeps", upstream: "Amazon Bedrock", route: "amazon-bedrock", wantKeep: true},
		{name: "route variant suffix keeps", upstream: "deepinfra", route: "deepinfra/fp4", wantKeep: true},
		{name: "auto route keeps any upstream", upstream: "Nvidia", route: "", wantKeep: true},
		{name: "no recorded upstream keeps pinned route", upstream: "", route: "Wafer", wantKeep: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cr := ToCoreRequest(ChatRequest{
				Model:         "openai/gpt-5",
				ProviderRoute: tt.route,
				Messages: []ChatMessage{
					{Role: "assistant", Content: "ok", Reasoning: "think", ReasoningExtra: extra, ReasoningExtraFor: "openrouter", ReasoningExtraUpstream: tt.upstream},
				},
			}, domain.ProviderChat, true)
			rb, ok := cr.Messages[0].Blocks[0].(core.ReasoningBlock)
			if !ok {
				t.Fatalf("blocks[0] = %#v, want ReasoningBlock", cr.Messages[0].Blocks[0])
			}
			if tt.wantKeep && string(rb.Extra) != string(extra) {
				t.Fatalf("Extra = %s, want kept for upstream %q / route %q", rb.Extra, tt.upstream, tt.route)
			}
			if !tt.wantKeep && len(rb.Extra) != 0 {
				t.Fatalf("Extra = %s, want dropped for upstream %q vs pinned route %q", rb.Extra, tt.upstream, tt.route)
			}
		})
	}
}

// TestOpenRouterReasoningFormatVendorMismatchDrops proves a reasoning_details
// item whose declared format belongs to a different upstream vendor than the
// requested model's slug prefix is dropped instead of sticky-failing the
// upstream (open-webui#28240 pattern).
func TestOpenRouterReasoningFormatVendorMismatchDrops(t *testing.T) {
	extra := json.RawMessage(`[{"type":"reasoning.encrypted","data":"cipher","format":"anthropic-claude-v1"}]`)
	cr := ToCoreRequest(ChatRequest{
		Model: "openai/gpt-5",
		Messages: []ChatMessage{
			{Role: "assistant", Content: "ok", Reasoning: "think", ReasoningExtra: extra},
		},
	}, domain.ProviderChat, true)
	rb, ok := cr.Messages[0].Blocks[0].(core.ReasoningBlock)
	if !ok {
		t.Fatalf("blocks[0] = %#v, want ReasoningBlock", cr.Messages[0].Blocks[0])
	}
	if len(rb.Extra) != 0 {
		t.Fatalf("Extra = %s, want anthropic-format details dropped for openai/ model", rb.Extra)
	}
}

// TestOpenRouterReasoningFormatVendorMatchKeeps proves same-vendor formats
// and format-less items keep replaying; vendor variants like :free and a
// models/ prefix normalize before the comparison.
func TestOpenRouterReasoningFormatVendorMatchKeeps(t *testing.T) {
	tests := []struct {
		name  string
		model string
		extra string
	}{
		{name: "anthropic format on anthropic model", model: "anthropic/claude-sonnet-4", extra: `[{"type":"reasoning.encrypted","data":"c","format":"anthropic-claude-v1"}]`},
		{name: "openai format on openai model", model: "openai/gpt-5", extra: `[{"type":"reasoning.encrypted","data":"c","format":"openai-responses-v1"}]`},
		{name: "xai format on x-ai model", model: "x-ai/grok-4-fast", extra: `[{"type":"reasoning.encrypted","data":"c","format":"xai-responses-v1"}]`},
		{name: "gemini format on google model", model: "google/gemini-3-flash", extra: `[{"type":"reasoning.encrypted","data":"c","format":"google-gemini-v1"}]`},
		{name: "variant suffix stripped", model: "anthropic/claude-sonnet-4:free", extra: `[{"type":"reasoning.encrypted","data":"c","format":"anthropic-claude-v1"}]`},
		{name: "models prefix stripped", model: "models/anthropic/claude-sonnet-4", extra: `[{"type":"reasoning.encrypted","data":"c","format":"anthropic-claude-v1"}]`},
		{name: "unknown format ignored", model: "openai/gpt-5", extra: `[{"type":"reasoning.encrypted","data":"c","format":"unknown"}]`},
		{name: "no format ignored", model: "openai/gpt-5", extra: `[{"type":"reasoning.summary","summary":"s"}]`},
		{name: "unmapped format ignored", model: "openai/gpt-5", extra: `[{"type":"reasoning.encrypted","data":"c","format":"future-v9"}]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extra := json.RawMessage(tt.extra)
			cr := ToCoreRequest(ChatRequest{
				Model: tt.model,
				Messages: []ChatMessage{
					{Role: "assistant", Content: "ok", Reasoning: "think", ReasoningExtra: extra},
				},
			}, domain.ProviderChat, true)
			rb, ok := cr.Messages[0].Blocks[0].(core.ReasoningBlock)
			if !ok {
				t.Fatalf("blocks[0] = %#v, want ReasoningBlock", cr.Messages[0].Blocks[0])
			}
			if string(rb.Extra) != string(extra) {
				t.Fatalf("Extra = %s, want %s kept", rb.Extra, extra)
			}
		})
	}
}

// TestOpenRouterReasoningFormatUndeterminedVendorKeeps proves routing aliases
// and bare model names leave the extra alone: when the target vendor cannot
// be derived there is no positive mismatch to drop on.
func TestOpenRouterReasoningFormatUndeterminedVendorKeeps(t *testing.T) {
	extra := json.RawMessage(`[{"type":"reasoning.encrypted","data":"cipher","format":"anthropic-claude-v1"}]`)
	for _, model := range []string{"openrouter/auto", "openrouter/free", "gpt-5"} {
		cr := ToCoreRequest(ChatRequest{
			Model: model,
			Messages: []ChatMessage{
				{Role: "assistant", Content: "ok", Reasoning: "think", ReasoningExtra: extra},
			},
		}, domain.ProviderChat, true)
		rb, ok := cr.Messages[0].Blocks[0].(core.ReasoningBlock)
		if !ok {
			t.Fatalf("model %q: blocks[0] = %#v, want ReasoningBlock", model, cr.Messages[0].Blocks[0])
		}
		if string(rb.Extra) != string(extra) {
			t.Fatalf("model %q: Extra = %s, want forwarded unchanged", model, rb.Extra)
		}
	}
}

// TestFromCoreResponseCarriesModelAndUpstream proves the provider-reported
// served model and gateway upstream flow through conversion so the agent
// layer can persist them as provenance.
func TestFromCoreResponseCarriesModelAndUpstream(t *testing.T) {
	out := FromCoreResponse(&core.Response{
		Provider: "openrouter",
		Model:    "nvidia/nemotron-3-super-120b-a12b:free",
		Upstream: "Nvidia",
		Blocks:   []core.Block{core.TextBlock{Text: "ok"}},
	})
	if out.Model != "nvidia/nemotron-3-super-120b-a12b:free" {
		t.Fatalf("Model = %q, want served model", out.Model)
	}
	if out.Upstream != "Nvidia" {
		t.Fatalf("Upstream = %q, want gateway upstream", out.Upstream)
	}
}

func TestCodexKindKeepsReasoningExtra(t *testing.T) {
	extra := json.RawMessage(`{"type":"reasoning","encrypted_content":"ENC-REASON"}`)
	cr := ToCoreRequest(ChatRequest{
		Model: "gpt-5.6-luna",
		Messages: []ChatMessage{
			{Role: "assistant", Content: "ok", Reasoning: "think", ReasoningExtra: extra},
		},
	}, domain.ProviderCodex, false)
	rb, ok := cr.Messages[0].Blocks[0].(core.ReasoningBlock)
	if !ok {
		t.Fatalf("blocks[0] = %#v, want ReasoningBlock", cr.Messages[0].Blocks[0])
	}
	if string(rb.Extra) != string(extra) {
		t.Fatalf("Extra = %s, want preserved for codex", rb.Extra)
	}
}

func TestReasoningExtraAbsentLeavesExtraNil(t *testing.T) {
	cr := ToCoreRequest(ChatRequest{
		Model: "gpt-5.6-sol",
		Messages: []ChatMessage{
			{Role: "assistant", Content: "ok", Reasoning: "think"},
		},
	}, domain.ProviderResponses, false)
	rb, ok := cr.Messages[0].Blocks[0].(core.ReasoningBlock)
	if !ok {
		t.Fatalf("blocks[0] = %#v", cr.Messages[0].Blocks[0])
	}
	if len(rb.Extra) != 0 {
		t.Fatalf("Extra = %s, want empty/nil when ReasoningExtra absent", rb.Extra)
	}
}

func TestFromCoreResponseCarriesReasoningExtra(t *testing.T) {
	extra := json.RawMessage(`{"type":"reasoning","encrypted_content":"ENC-1"}`)
	out := FromCoreResponse(&core.Response{
		Provider: "openai",
		Blocks: []core.Block{
			core.ReasoningBlock{Text: "think", Extra: extra},
			core.TextBlock{Text: "answer"},
		},
	})
	if out.Reasoning != "think" {
		t.Fatalf("Reasoning = %q, want think", out.Reasoning)
	}
	if string(out.ReasoningExtra) != string(extra) {
		t.Fatalf("ReasoningExtra = %s, want %s", out.ReasoningExtra, extra)
	}
	if out.ReasoningExtraFor != "openai" {
		t.Fatalf("ReasoningExtraFor = %q, want openai producer marker", out.ReasoningExtraFor)
	}
}

// TestFromCoreResponseReasoningExtraForFallsBackToUsage proves the producer
// marker also reads Usage.Provider when the response itself is unstamped,
// and stays empty when neither field reports a provider.
func TestFromCoreResponseReasoningExtraForFallsBackToUsage(t *testing.T) {
	extra := json.RawMessage(`{"signature":"sig-1"}`)
	out := FromCoreResponse(&core.Response{
		Usage:  core.Usage{Provider: "anthropic"},
		Blocks: []core.Block{core.ReasoningBlock{Extra: extra}},
	})
	if out.ReasoningExtraFor != "anthropic" {
		t.Fatalf("ReasoningExtraFor = %q, want anthropic from usage stamp", out.ReasoningExtraFor)
	}

	out = FromCoreResponse(&core.Response{
		Blocks: []core.Block{core.ReasoningBlock{Extra: extra}},
	})
	if out.ReasoningExtraFor != "" {
		t.Fatalf("ReasoningExtraFor = %q, want empty for unstamped response", out.ReasoningExtraFor)
	}
}

func TestFromCoreResponseOmitsEmptyReasoningExtra(t *testing.T) {
	out := FromCoreResponse(&core.Response{
		Provider: "openai",
		Blocks:   []core.Block{core.ReasoningBlock{Text: "think"}},
	})
	if len(out.ReasoningExtra) != 0 {
		t.Fatalf("ReasoningExtra = %s, want empty", out.ReasoningExtra)
	}
	if out.ReasoningExtraFor != "" {
		t.Fatalf("ReasoningExtraFor = %q, want empty when no extra was captured", out.ReasoningExtraFor)
	}
}

// unstampedProvider returns a fixed core response without a provider stamp,
// covering the kind-based family fallback in CompleteViaCore.
type unstampedProvider struct {
	resp *core.Response
}

func (p unstampedProvider) Name() string { return "test-unstamped" }
func (p unstampedProvider) Chat(context.Context, *core.Request) (*core.Response, error) {
	return p.resp, nil
}
func (p unstampedProvider) Stream(context.Context, *core.Request) (core.Stream, error) {
	return nil, fmt.Errorf("unimplemented")
}

// TestCompleteViaCoreStampsReasoningExtraForFromKind proves the wire-kind
// fallback: a provider whose response carries no producer stamp still gets
// the family that interprets the target wire recorded on ReasoningExtraFor.
func TestCompleteViaCoreStampsReasoningExtraForFromKind(t *testing.T) {
	extra := json.RawMessage(`{"signature":"sig-1"}`)
	out, err := CompleteViaCore(context.Background(), unstampedProvider{
		resp: &core.Response{Blocks: []core.Block{core.ReasoningBlock{Text: "think", Extra: extra}}},
	}, ChatRequest{
		Model:    "claude-sonnet-4-6",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}, domain.ProviderMessages, false)
	if err != nil {
		t.Fatalf("CompleteViaCore: %v", err)
	}
	if out.ReasoningExtraFor != "anthropic" {
		t.Fatalf("ReasoningExtraFor = %q, want anthropic kind fallback", out.ReasoningExtraFor)
	}
}

func TestToCoreRequestSetsCompactionItemsForResponses(t *testing.T) {
	cr := ToCoreRequest(ChatRequest{Model: "gpt-5.2", CompactionBlob: `[{"type":"compaction"}]`}, domain.ProviderResponses, false)
	if got := cr.ProviderOptions["compaction_items"]; got != `[{"type":"compaction"}]` {
		t.Fatalf("compaction_items = %#v, want the blob", got)
	}
}

func TestToCoreRequestOmitsCompactionItemsForChatKind(t *testing.T) {
	cr := ToCoreRequest(ChatRequest{Model: "gpt-4o", CompactionBlob: `[{"type":"compaction"}]`}, domain.ProviderChat, false)
	if _, ok := cr.ProviderOptions["compaction_items"]; ok {
		t.Fatal("compaction_items must not be set for chat kind")
	}
}

func TestToCoreRequestCodexForwardsCompactionItemsNotContextManagement(t *testing.T) {
	blob := `[{"type":"compaction","encrypted_content":"ENC-1"}]`
	cr := ToCoreRequest(ChatRequest{
		Model:                    "gpt-5-codex",
		System:                   "current instructions",
		CompactionBlob:           blob,
		CompactionPrefixMessages: 2,
		ContextManagement: []map[string]any{
			{"type": "compaction", "compact_threshold": 360000},
		},
	}, domain.ProviderCodex, false)
	if got := cr.ProviderOptions["compaction_items"]; got != blob {
		t.Fatalf("compaction_items = %#v, want %q", got, blob)
	}
	if got := cr.ProviderOptions["compaction_prefix_messages"]; got != 3 {
		t.Fatalf("compaction_prefix_messages = %#v, want 3 including the prepended system message", got)
	}
	if _, ok := cr.ProviderOptions["context_management"]; ok {
		t.Fatal("context_management must not be set for codex kind")
	}
}

func TestToCoreRequestCodexSetsCompactionTrigger(t *testing.T) {
	cr := ToCoreRequest(ChatRequest{
		Model:            "gpt-5-codex",
		RemoteCompaction: true,
		ConversationID:   "conv_codex",
	}, domain.ProviderCodex, false)
	if got := cr.ProviderOptions["compaction_trigger"]; got != true {
		t.Fatalf("compaction_trigger = %#v, want true", got)
	}
	if got := cr.ProviderOptions["session_id"]; got != "conv_codex" {
		t.Fatalf("session_id = %#v, want conversation id", got)
	}
}

func TestToCoreRequestCodexForwardsReasoningSummary(t *testing.T) {
	cr := ToCoreRequest(ChatRequest{Model: "gpt-5.6-terra", ReasoningSummary: "detailed"}, domain.ProviderCodex, false)
	if got := cr.ProviderOptions["reasoning_summary"]; got != "detailed" {
		t.Fatalf("reasoning_summary = %#v, want detailed", got)
	}
	nonCodex := ToCoreRequest(ChatRequest{Model: "gpt-5.6", ReasoningSummary: "detailed"}, domain.ProviderResponses, false)
	if _, ok := nonCodex.ProviderOptions["reasoning_summary"]; ok {
		t.Fatal("responses provider must not receive Codex reasoning_summary option")
	}
}

func TestNewProviderContextCarriesCodexReasoningSummary(t *testing.T) {
	pc := NewProviderContext(&domain.Provider{
		ID:               "codex",
		Kind:             domain.ProviderCodex,
		ReasoningSummary: domain.ReasoningSummaryDetailed,
	}, nil)

	got := pc.withProviderOptions(ChatRequest{})
	if got.ReasoningSummary != domain.ReasoningSummaryDetailed {
		t.Fatalf("ReasoningSummary = %q, want detailed", got.ReasoningSummary)
	}
}

func TestToCoreRequestMessagesCacheControlUsesTTL(t *testing.T) {
	req := ChatRequest{
		Model:         "claude-sonnet-4-6",
		System:        "you are helpful",
		PromptCaching: true,
		PromptCache:   &PromptCachePolicy{Mode: "auto", TTL: "1h", Key: "pc_unused"},
	}
	cr := ToCoreRequest(req, domain.ProviderMessages, false)
	if len(cr.Messages) == 0 {
		t.Fatal("expected system message")
	}
	tb, ok := cr.Messages[0].Blocks[0].(core.TextBlock)
	if !ok || tb.Cache == nil || tb.Cache.TTL != core.CacheTTL1h {
		t.Fatalf("system cache = %#v, want ttl 1h", cr.Messages[0].Blocks)
	}
	if cr.ProviderOptions["prompt_cache_key"] != nil {
		t.Fatal("messages kind must not send prompt_cache_key")
	}
}

// TestToCoreRequestDirectVanillaChatNeverPutsTTLOnSystemBreakpoint verifies
// the direct OpenAI Chat conversion path. A 5m/1h TTL is not represented as
// cache_control there, and the incompatible direct-Chat TTL option is omitted.
func TestToCoreRequestDirectVanillaChatNeverPutsTTLOnSystemBreakpoint(t *testing.T) {
	req := ChatRequest{
		Model:         "deepseek-v4-flash",
		System:        "you are helpful",
		PromptCaching: true,
		PromptCache:   &PromptCachePolicy{Mode: "auto", TTL: "1h", Key: "pc_oc"},
	}
	cr := ToCoreRequest(req, domain.ProviderChat, false)
	tb, ok := cr.Messages[0].Blocks[0].(core.TextBlock)
	if !ok {
		t.Fatalf("system block = %#v", cr.Messages[0].Blocks)
	}
	if tb.Cache != nil {
		t.Fatalf("vanilla chat system cache = %#v, want nil (direct Chat has no cache_control breakpoint)", tb.Cache)
	}
	if cr.ProviderOptions["prompt_cache_options"] != nil {
		t.Fatalf("vanilla chat must not send prompt_cache_options for 1h TTL (Console Go only accepts 5m|1h on cache_control, OpenAI Chat only accepts 30m): %#v", cr.ProviderOptions["prompt_cache_options"])
	}
	if got := cr.ProviderOptions["prompt_cache_key"]; got != "pc_oc" {
		t.Fatalf("prompt_cache_key = %#v, want pc_oc", got)
	}
}

func TestNewProviderContextCustomOpenCodeKeepsVanillaChatWire(t *testing.T) {
	p := &domain.Provider{
		ID:      "prov_b9587aa5f937c4f2",
		Driver:  domain.ProviderDriverOpenRouter,
		Kind:    domain.ProviderChat,
		BaseURL: "https://opencode.ai/zen/go/v1",
	}
	pc := NewProviderContext(p, nil)
	if pc.OpenRouter {
		t.Fatal("custom OpenCode zen/go must convert Chat requests with the vanilla OpenAI wire")
	}
	if pc.Driver != domain.ProviderDriverOpenRouter {
		t.Fatalf("Driver = %q, want stored openrouter, OpenRouter=%v", pc.Driver, pc.OpenRouter)
	}
	if pc.BaseURL != p.BaseURL {
		t.Fatalf("BaseURL = %q, want %q", pc.BaseURL, p.BaseURL)
	}
}

func TestBuildPromptCachePolicyForContextOpenCodeIgnoresOpenRouterFlag(t *testing.T) {
	settings := domain.Settings{PromptCaching: true}
	adapter := Context{
		ProviderID: "prov_oc",
		Kind:       domain.ProviderChat,
		Driver:     domain.ProviderDriverOpenRouter,
		OpenRouter: false,
		BaseURL:    "https://opencode.ai/zen/go/v1",
	}
	policy := BuildPromptCachePolicyForContext(settings, adapter, "deepseek-v4-flash", "conv_abc", domain.PromptCacheConversationPrefix)
	if policy == nil || policy.TTL != "5m" {
		t.Fatalf("opencode context TTL = %+v, want 5m (5m/1h enum, not 30m)", policy)
	}
}

func TestToCoreRequestResponsesSendsPromptCacheOptions(t *testing.T) {
	req := ChatRequest{
		Model:          "gpt-5",
		System:         "you are helpful",
		PromptCaching:  true,
		PromptCache:    &PromptCachePolicy{Mode: "auto", TTL: "30m", Key: "pc_abc"},
		CompactionBlob: `[{"type":"compaction"}]`,
	}
	cr := ToCoreRequest(req, domain.ProviderResponses, false)
	if got := cr.ProviderOptions["prompt_cache_key"]; got != "pc_abc" {
		t.Fatalf("prompt_cache_key = %#v", got)
	}
	opts, ok := cr.ProviderOptions["prompt_cache_options"].(map[string]any)
	if !ok || opts["ttl"] != "30m" {
		t.Fatalf("prompt_cache_options = %#v, want ttl 30m", cr.ProviderOptions["prompt_cache_options"])
	}
	if got := cr.ProviderOptions["compaction_items"]; got != `[{"type":"compaction"}]` {
		t.Fatalf("compaction_items dropped when merging cache options: %#v", got)
	}
}

func TestToCoreRequestMiniMaxChatSendsReasoningSplit(t *testing.T) {
	cr := ToCoreRequest(ChatRequest{Model: "minimax-m3"}, domain.ProviderChat, false)
	if cr.ProviderOptions["reasoning_split"] != true {
		t.Fatalf("MiniMax Chat reasoning_split = %#v, want true", cr.ProviderOptions["reasoning_split"])
	}
}

func TestToCoreRequestNonMiniMaxChatOmitsReasoningSplit(t *testing.T) {
	cr := ToCoreRequest(ChatRequest{Model: "glm-5.3-flash"}, domain.ProviderChat, false)
	if _, ok := cr.ProviderOptions["reasoning_split"]; ok {
		t.Fatalf("GLM Chat must not send reasoning_split, got %#v", cr.ProviderOptions)
	}
}

func TestToCoreRequestOpenRouterMiniMaxOmitsReasoningSplit(t *testing.T) {
	cr := ToCoreRequest(ChatRequest{Model: "minimax/minimax-m3:free"}, domain.ProviderChat, true)
	if _, ok := cr.ProviderOptions["reasoning_split"]; ok {
		t.Fatalf("OpenRouter MiniMax uses reasoning object, must not send reasoning_split: %#v", cr.ProviderOptions)
	}
}

func TestToCoreRequestMiniMaxMessagesOmitsReasoningSplit(t *testing.T) {
	cr := ToCoreRequest(ChatRequest{Model: "minimax-m3"}, domain.ProviderMessages, false)
	if _, ok := cr.ProviderOptions["reasoning_split"]; ok {
		t.Fatalf("Messages MiniMax uses thinking blocks, must not send reasoning_split: %#v", cr.ProviderOptions)
	}
}

func TestToCoreRequestMiniMaxChatHonorsStripReasoningSplit(t *testing.T) {
	cr := ToCoreRequest(ChatRequest{
		Model:       "minimax-m3",
		StripParams: []string{"reasoning_split"},
	}, domain.ProviderChat, false)
	if _, ok := cr.ProviderOptions["reasoning_split"]; ok {
		t.Fatalf("stripped MiniMax Chat must omit reasoning_split, got %#v", cr.ProviderOptions)
	}
}

func TestToCoreRequestChatSendsPromptCacheKey(t *testing.T) {
	req := ChatRequest{
		Model:         "gpt-5",
		System:        "you are helpful",
		PromptCaching: true,
		PromptCache:   &PromptCachePolicy{Mode: "auto", TTL: "30m", Key: "pc_chat"},
	}
	cr := ToCoreRequest(req, domain.ProviderChat, false)
	if got := cr.ProviderOptions["prompt_cache_key"]; got != "pc_chat" {
		t.Fatalf("chat prompt_cache_key = %#v", got)
	}
	opts, ok := cr.ProviderOptions["prompt_cache_options"].(map[string]any)
	if !ok || opts["ttl"] != "30m" {
		t.Fatalf("chat prompt_cache_options = %#v, want ttl 30m", cr.ProviderOptions["prompt_cache_options"])
	}
}

func TestToCoreRequestOpenRouterChatSendsPromptCacheKeyAndSessionID(t *testing.T) {
	req := ChatRequest{
		Model:         "anthropic/claude-sonnet-4",
		System:        "you are helpful",
		PromptCaching: true,
		PromptCache:   &PromptCachePolicy{Mode: "auto", TTL: "1h", Key: "nusashell_cv_0123456789012345678"},
	}
	cr := ToCoreRequest(req, domain.ProviderChat, true)
	if got := cr.ProviderOptions["prompt_cache_key"]; got != req.PromptCache.Key {
		t.Fatalf("OpenRouter chat prompt_cache_key = %#v, want %q", got, req.PromptCache.Key)
	}
	if got := cr.ProviderOptions["session_id"]; got != req.PromptCache.Key {
		t.Fatalf("OpenRouter chat session_id = %#v, want %q", got, req.PromptCache.Key)
	}
	tb, ok := cr.Messages[0].Blocks[0].(core.TextBlock)
	if !ok || tb.Cache == nil || tb.Cache.TTL != core.CacheTTL1h {
		t.Fatalf("openrouter system cache = %#v, want ttl 1h", cr.Messages[0].Blocks)
	}
}

func TestToCoreRequestOpenRouterDelegatedKindsCarrySessionID(t *testing.T) {
	for _, kind := range []domain.ProviderKind{domain.ProviderMessages, domain.ProviderResponses} {
		t.Run(string(kind), func(t *testing.T) {
			key := "nusashell_bg_0123456789012345678"
			cr := ToCoreRequest(ChatRequest{
				Model:         "model",
				PromptCaching: true,
				PromptCache:   &PromptCachePolicy{Mode: "auto", Key: key},
			}, kind, true)
			if got := cr.ProviderOptions["session_id"]; got != key {
				t.Fatalf("session_id = %#v, want %q", got, key)
			}
		})
	}
}

func TestMCPCallDisablesResponsesStrictMode(t *testing.T) {
	for _, kind := range []domain.ProviderKind{domain.ProviderResponses, domain.ProviderCodex} {
		t.Run(string(kind), func(t *testing.T) {
			schema := map[string]any{"type": "object", "properties": map[string]any{"arguments_json": map[string]any{"type": "object", "properties": map[string]any{}}}}
			req := ToCoreRequest(ChatRequest{Tools: []ToolDef{{Name: "mcp_call", InputSchema: schema}, {Name: "file_read", InputSchema: schema}}}, kind, false)
			if req.Tools[0].Strict != core.StrictDisabled {
				t.Fatalf("mcp_call strict = %v, want disabled", req.Tools[0].Strict)
			}
			if req.Tools[1].Strict != core.StrictDefault {
				t.Fatal("unrelated tool strict changed")
			}
			want, _ := json.Marshal(schema)
			if string(req.Tools[0].Parameters) != string(want) {
				t.Fatal("MCP schema changed during conversion")
			}
		})
	}
}

func TestMCPCallKeepsDefaultStrictModeOnOtherProviderKinds(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{}}
	for _, kind := range []domain.ProviderKind{domain.ProviderMessages, domain.ProviderChat, domain.ProviderGemini} {
		t.Run(string(kind), func(t *testing.T) {
			req := ToCoreRequest(ChatRequest{Tools: []ToolDef{{Name: "mcp_call", InputSchema: schema}}}, kind, false)
			if req.Tools[0].Strict != core.StrictDefault {
				t.Fatalf("mcp_call strict = %v, want default for %s", req.Tools[0].Strict, kind)
			}
		})
	}
}
