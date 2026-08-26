package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"nusashell/infrastructure/ai/core"
)

// streamResponseForTest wraps a string body as an SSE HTTP response.
func streamResponseForTest(body string) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// TestResponsesNonStreamingRoundTripsEncryptedContentWithEmptySummary is based
// on a real OpenAI Responses API chat history (gpt-5.6-luna). Reasoning items
// carry encrypted_content that MUST be echoed back on replay, even when the
// summary array is empty. This test verifies the non-streaming round-trip:
// response → ReasoningBlock → request item preserves encrypted_content.
func TestResponsesNonStreamingRoundTripsEncryptedContentWithEmptySummary(t *testing.T) {
	rawItem := json.RawMessage(`{"id":"rs_03e443ae","type":"reasoning","status":"completed","content":[],"encrypted_content":"gAAAAABqj0PKs95gNt","summary":[]}`)
	resp, err := convertResponsesResponse(&responsesResponse{
		Model:  "gpt-5.6-luna",
		Status: "completed",
		Output: []responsesOutputItem{
			{
				ID:               "rs_03e443ae",
				Type:             "reasoning",
				Status:           "completed",
				EncryptedContent: "gAAAAABqj0PKs95gNt",
				Summary:          []responsesSummaryItem{},
				Raw:              rawItem,
			},
		},
	}, "")
	if err != nil {
		t.Fatalf("convertResponsesResponse: %v", err)
	}
	if len(resp.Blocks) != 1 {
		t.Fatalf("expected 1 reasoning block, got %d blocks", len(resp.Blocks))
	}
	reasoning, ok := resp.Blocks[0].(core.ReasoningBlock)
	if !ok {
		t.Fatalf("expected ReasoningBlock, got %T", resp.Blocks[0])
	}
	// Text is empty because summary is empty, but Extra must carry the
	// encrypted_content for replay.
	if reasoning.Text != "" {
		t.Fatalf("text = %q, want empty (summary was empty)", reasoning.Text)
	}
	if !strings.Contains(string(reasoning.Extra), `"encrypted_content":"gAAAAABqj0PKs95gNt"`) {
		t.Fatalf("Extra must contain encrypted_content, got: %s", reasoning.Extra)
	}

	// Round-trip back to request items — must preserve encrypted_content.
	items, err := responsesInputItems([]core.Message{core.Assistant(resp.Blocks...)})
	if err != nil {
		t.Fatalf("responsesInputItems: %v", err)
	}
	data, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("marshal items: %v", err)
	}
	if !strings.Contains(string(data), `"encrypted_content":"gAAAAABqj0PKs95gNt"`) {
		t.Fatalf("round-trip lost encrypted_content: %s", data)
	}
}

// TestResponsesNonStreamingRoundTripsMultipleReasoningItemsWithSummary
// verifies multiple reasoning items (as seen in real chat histories) each
// round-trip their encrypted_content and summary text independently.
func TestResponsesNonStreamingRoundTripsMultipleReasoningItemsWithSummary(t *testing.T) {
	items := []responsesOutputItem{
		{
			ID:               "rs_001",
			Type:             "reasoning",
			Status:           "completed",
			EncryptedContent: "enc_001",
			Summary:          []responsesSummaryItem{{Text: "Considering delta rendering"}},
			Raw:              json.RawMessage(`{"id":"rs_001","type":"reasoning","encrypted_content":"enc_001","summary":[{"type":"summary_text","text":"Considering delta rendering"}]}`),
		},
		{
			ID:               "rs_002",
			Type:             "reasoning",
			Status:           "completed",
			EncryptedContent: "enc_002",
			Summary:          []responsesSummaryItem{}, // empty summary
			Raw:              json.RawMessage(`{"id":"rs_002","type":"reasoning","encrypted_content":"enc_002","summary":[]}`),
		},
		{
			ID:               "rs_003",
			Type:             "reasoning",
			Status:           "completed",
			EncryptedContent: "enc_003",
			Summary:          []responsesSummaryItem{{Text: "Considering server setup"}},
			Raw:              json.RawMessage(`{"id":"rs_003","type":"reasoning","encrypted_content":"enc_003","summary":[{"type":"summary_text","text":"Considering server setup"}]}`),
		},
	}
	resp, err := convertResponsesResponse(&responsesResponse{
		Model:  "gpt-5.6-luna",
		Status: "completed",
		Output: items,
	}, "")
	if err != nil {
		t.Fatalf("convertResponsesResponse: %v", err)
	}
	if len(resp.Blocks) != 3 {
		t.Fatalf("expected 3 reasoning blocks, got %d", len(resp.Blocks))
	}
	// Block 0: has summary text + encrypted_content
	b0, ok := resp.Blocks[0].(core.ReasoningBlock)
	if !ok || b0.Text != "Considering delta rendering" || !strings.Contains(string(b0.Extra), "enc_001") {
		t.Fatalf("block 0 = %#v", resp.Blocks[0])
	}
	// Block 1: empty summary but has encrypted_content
	b1, ok := resp.Blocks[1].(core.ReasoningBlock)
	if !ok || b1.Text != "" || !strings.Contains(string(b1.Extra), "enc_002") {
		t.Fatalf("block 1 = %#v", resp.Blocks[1])
	}
	// Block 2: has summary text + encrypted_content
	b2, ok := resp.Blocks[2].(core.ReasoningBlock)
	if !ok || b2.Text != "Considering server setup" || !strings.Contains(string(b2.Extra), "enc_003") {
		t.Fatalf("block 2 = %#v", resp.Blocks[2])
	}

	// Round-trip all blocks
	rtItems, err := responsesInputItems([]core.Message{core.Assistant(resp.Blocks...)})
	if err != nil {
		t.Fatalf("responsesInputItems: %v", err)
	}
	data, err := json.Marshal(rtItems)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, enc := range []string{"enc_001", "enc_002", "enc_003"} {
		if !strings.Contains(string(data), `"encrypted_content":"`+enc+`"`) {
			t.Fatalf("round-trip lost %s: %s", enc, data)
		}
	}
}

// TestResponsesStreamCapturesEncryptedContentFromOutputItemDone is the
// critical streaming test. In real OpenAI Responses streams, the
// `response.output_item.done` event carries the reasoning item's
// encrypted_content. This MUST be captured as a ReasoningBlock with Extra
// set, so it can be echoed back on replay. Currently this event is treated
// as a no-op ProviderEvent, silently dropping encrypted_content.
func TestResponsesStreamCapturesEncryptedContentFromOutputItemDone(t *testing.T) {
	sse := strings.Join([]string{
		`event: response.created`,
		`data: {"type":"response.created","sequence_number":1,"response":{"id":"resp_1"}}`,
		``,
		`event: response.reasoning_summary_text.delta`,
		`data: {"type":"response.reasoning_summary_text.delta","delta":"Considering delta","sequence_number":2}`,
		``,
		`event: response.output_item.done`,
		`data: {"type":"response.output_item.done","sequence_number":3,"output_index":0,"item":{"id":"rs_001","type":"reasoning","status":"completed","content":[],"encrypted_content":"gAAAAABqj0PKs95gNt","summary":[{"type":"summary_text","text":"Considering delta"}]}}`,
		``,
		`event: response.completed`,
		`data: {"type":"response.completed","sequence_number":4,"response":{"model":"gpt-5.6-luna","status":"completed","usage":{"input_tokens":10,"output_tokens":20,"total_tokens":30}}}`,
		``,
	}, "\n")

	provider, err := New(Config{
		APIKey:  "test-key",
		BaseURL: "https://example.test",
		HTTPClient: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return streamResponseForTest(sse), nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stream, err := provider.ResponsesStream(context.Background(), &ResponsesRequest{
		Model:    "gpt-5.6-luna",
		Messages: []core.Message{core.UserText("hello")},
	})
	if err != nil {
		t.Fatalf("ResponsesStream: %v", err)
	}
	resp, err := core.Collect(stream)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	// The response must contain a ReasoningBlock with the encrypted_content
	// in Extra, so it can be replayed in the next turn.
	var foundEncrypted bool
	for _, block := range resp.Blocks {
		if rb, ok := block.(core.ReasoningBlock); ok {
			if strings.Contains(string(rb.Extra), `"encrypted_content":"gAAAAABqj0PKs95gNt"`) {
				foundEncrypted = true
			}
		}
	}
	if !foundEncrypted {
		t.Fatalf("streaming response lost encrypted_content — ReasoningBlock Extra must carry it for replay; blocks: %#v", resp.Blocks)
	}
}
