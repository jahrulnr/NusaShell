package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"nusashell/infrastructure/ai/core"
)

// TestAnthropicStreamThinkingAndSignatureDeltaRoundTrip is based on the
// official Anthropic streaming docs. When extended thinking is enabled,
// Claude streams thinking_delta events followed by a signature_delta event
// just before content_block_stop. The signature verifies thinking integrity
// and MUST be echoed back on replay. This test verifies the streaming path
// captures both thinking text and signature into a ReasoningBlock.
func TestAnthropicStreamThinkingAndSignatureDeltaRoundTrip(t *testing.T) {
	sse := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-6","stop_reason":null,"usage":{"input_tokens":10,"output_tokens":1}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"I need to find the GCD of 1071 and 462 using the Euclidean algorithm."}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"\n\n1071 = 2 × 462 + 147"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"EqQBCgIYAhIM1gbcDa9GJwZA2b3hGgxBdjrkzLoky3dl1pkiMOYds"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"The GCD is 21."}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":1}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":50}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")

	provider, err := New(Config{
		APIKey:  "test-key",
		BaseURL: "https://example.test",
		HTTPClient: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return streamResponse(sse), nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	maxTokens := 20000
	stream, err := provider.Stream(context.Background(), &core.Request{
		Model:     "claude-sonnet-4-6",
		MaxTokens: &maxTokens,
		Messages:  []core.Message{core.UserText("Find the GCD of 1071 and 462")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	resp, err := core.Collect(stream)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	// Verify thinking text was captured
	reasoning := resp.Reasoning()
	if !strings.Contains(reasoning, "GCD of 1071 and 462") || !strings.Contains(reasoning, "1071 = 2") {
		t.Fatalf("reasoning text = %q, want GCD thinking content", reasoning)
	}

	// Verify signature was captured — it must be present for replay
	var hasSignature bool
	for _, block := range resp.Blocks {
		if rb, ok := block.(core.ReasoningBlock); ok {
			if rb.Signature == "EqQBCgIYAhIM1gbcDa9GJwZA2b3hGgxBdjrkzLoky3dl1pkiMOYds" {
				hasSignature = true
			}
		}
	}
	if !hasSignature {
		t.Fatalf("streaming response lost thinking signature — blocks: %#v", resp.Blocks)
	}

	// Verify text content
	if resp.Text() != "The GCD is 21." {
		t.Fatalf("text = %q, want %q", resp.Text(), "The GCD is 21.")
	}
}

// TestAnthropicNonStreamingThinkingBlockWithSignatureRoundTrip verifies
// that a thinking block with text + signature from a non-streaming response
// round-trips correctly through the request builder. Anthropic requires
// the signature to be echoed back on replay.
func TestAnthropicNonStreamingThinkingBlockWithSignatureRoundTrip(t *testing.T) {
	provider, err := New(Config{
		APIKey:  "test-key",
		BaseURL: "https://example.test",
		HTTPClient: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return streamResponse(`{
				"content":[
					{"type":"thinking","thinking":"I should call the tool.","signature":"sig-thinking"},
					{"type":"text","text":"done"}
				],
				"stop_reason":"end_turn",
				"usage":{"input_tokens":10,"output_tokens":20}
			}`), nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	maxTokens := 20000
	resp, err := provider.Chat(context.Background(), &core.Request{
		Model:     "claude-sonnet-4-6",
		MaxTokens: &maxTokens,
		Messages:  []core.Message{core.UserText("hello")},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	// Find the reasoning block with signature
	var reasoningBlock core.ReasoningBlock
	var foundReasoning bool
	for _, block := range resp.Blocks {
		if rb, ok := block.(core.ReasoningBlock); ok && rb.Signature != "" {
			reasoningBlock = rb
			foundReasoning = true
			break
		}
	}
	if !foundReasoning {
		t.Fatalf("no reasoning block with signature found, blocks: %#v", resp.Blocks)
	}
	if reasoningBlock.Text != "I should call the tool." || reasoningBlock.Signature != "sig-thinking" {
		t.Fatalf("reasoning block = %+v", reasoningBlock)
	}

	// Now round-trip: send the blocks back as assistant history
	var capturedBody map[string]any
	provider.cfg.HTTPClient = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(req.Body).Decode(&capturedBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		return streamResponse(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":1}}`), nil
	})
	_, err = provider.Chat(context.Background(), &core.Request{
		Model:     "claude-sonnet-4-6",
		MaxTokens: &maxTokens,
		Messages: []core.Message{
			core.UserText("hello"),
			core.Assistant(resp.Blocks...),
			core.UserText("follow up"),
		},
	})
	if err != nil {
		t.Fatalf("replay Chat: %v", err)
	}
	messages := capturedBody["messages"].([]any)
	// messages[0] = user "hello", messages[1] = assistant with thinking, messages[2] = user "follow up"
	var foundThinking bool
	for _, msg := range messages {
		m := msg.(map[string]any)
		if m["role"] != "assistant" {
			continue
		}
		content, ok := m["content"].([]any)
		if !ok {
			continue
		}
		for _, c := range content {
			block := c.(map[string]any)
			if block["type"] == "thinking" && block["thinking"] == "I should call the tool." && block["signature"] == "sig-thinking" {
				foundThinking = true
			}
		}
	}
	if !foundThinking {
		t.Fatalf("round-trip lost thinking block with signature: %#v", capturedBody["messages"])
	}
}

// TestAnthropicThinkingBlockSignatureOnlyPassesGuard verifies that a
// ReasoningBlock with empty text but non-empty signature is accepted by
// our guard. Anthropic's `display: "omitted"` mode produces thinking blocks
// with a signature but no visible thinking text — these are valid and must
// be replayed.
func TestAnthropicThinkingBlockSignatureOnlyPassesGuard(t *testing.T) {
	_, err := convertBlocks([]core.Block{
		core.ReasoningBlock{Text: "", Signature: "sig-omitted"},
	})
	if err != nil {
		t.Fatalf("signature-only thinking block must pass guard, got error: %v", err)
	}
}
