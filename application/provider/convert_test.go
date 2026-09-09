package provider

import (
	"encoding/json"
	"fmt"
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

func TestMessagesKindStripsCodexReasoningExtra(t *testing.T) {
	extra := json.RawMessage(`{"type":"reasoning","encrypted_content":"ENC-REASON"}`)
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
	if rb.Text != "think" || len(rb.Extra) != 0 {
		t.Fatalf("ReasoningBlock = %#v, want plaintext only", rb)
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
}

func TestFromCoreResponseOmitsEmptyReasoningExtra(t *testing.T) {
	out := FromCoreResponse(&core.Response{
		Blocks: []core.Block{core.ReasoningBlock{Text: "think"}},
	})
	if len(out.ReasoningExtra) != 0 {
		t.Fatalf("ReasoningExtra = %s, want empty", out.ReasoningExtra)
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

func TestNewProviderContextCustomOpenCodeUsesOpenRouterProfile(t *testing.T) {
	p := &domain.Provider{
		ID:      "prov_b9587aa5f937c4f2",
		Driver:  domain.ProviderDriverOpenRouter,
		Kind:    domain.ProviderChat,
		BaseURL: "https://opencode.ai/zen/go/v1",
	}
	pc := NewProviderContext(p, nil)
	if !pc.OpenRouter {
		t.Fatal("custom OpenCode zen/go must convert requests with the OpenRouter profile")
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
