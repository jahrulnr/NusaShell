package codex

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nusashell/infrastructure/ai/core"
)

// TestReasoningSummaryNotDuplicatedAtItemDone guards the visible-thinking
// contract: reasoning text streams only through
// response.reasoning_summary_text.delta events, and the reasoning item on
// response.output_item.done contributes opaque Extra (encrypted_content for
// replay) without re-emitting the summary text. Re-emitting it duplicated the
// whole thinking block — the UI glued the same text twice and the persisted
// transcript stored the duplicate.
func TestReasoningSummaryNotDuplicatedAtItemDone(t *testing.T) {
	sse := strings.Join([]string{
		"event: response.created",
		`data: {"type":"response.created","response":{"id":"resp_1"}}`,
		"",
		"event: response.reasoning_summary_text.delta",
		`data: {"type":"response.reasoning_summary_text.delta","delta":"First thought. "}`,
		"",
		"event: response.reasoning_summary_text.delta",
		`data: {"type":"response.reasoning_summary_text.delta","delta":"Second thought."}`,
		"",
		"event: response.output_item.done",
		`data: {"type":"response.output_item.done","item":{"id":"rs_1","type":"reasoning","status":"completed","summary":[{"type":"summary_text","text":"First thought. "},{"type":"summary_text","text":"Second thought."}],"encrypted_content":"ENC-REASON"}}`,
		"",
		"event: response.completed",
		`data: {"type":"response.completed","response":{"id":"resp_1"}}`,
		"",
	}, "\n")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sse))
	}))
	defer server.Close()

	provider, err := New(Config{
		BaseURL:    server.URL,
		APIKey:     "access-token",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stream, err := provider.Stream(t.Context(), &core.Request{
		Model: "gpt-5-codex",
		Messages: []core.Message{{
			Role:   core.RoleUser,
			Blocks: []core.Block{core.TextBlock{Text: "hi"}},
		}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()

	response, err := core.Collect(stream)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	var reasoning strings.Builder
	reasoningBlocks := 0
	encrypted := false
	for _, block := range response.Blocks {
		rb, ok := block.(core.ReasoningBlock)
		if !ok {
			continue
		}
		reasoningBlocks++
		reasoning.WriteString(rb.Text)
		if strings.Contains(string(rb.Extra), `"encrypted_content":"ENC-REASON"`) {
			encrypted = true
		}
	}
	want := "First thought. Second thought."
	if reasoning.String() != want {
		t.Fatalf("reasoning text = %q, want %q (summary text must be emitted once, from deltas)", reasoning.String(), want)
	}
	if reasoningBlocks != 1 {
		t.Fatalf("reasoning blocks = %d, want 1", reasoningBlocks)
	}
	if !encrypted {
		t.Fatalf("reasoning Extra must carry encrypted_content for replay")
	}
}
