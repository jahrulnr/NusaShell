package openai

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"nusashell/infrastructure/ai/core"
)

// TestResponsesStreamSeparatesReasoningSummaryParts verifies that chunks
// within one OpenAI Responses summary part stay contiguous, while a new
// summary_index gets a paragraph boundary. Without the index, separate bold
// summaries were persisted as `**one****two**` and rendered as glued text.
func TestResponsesStreamSeparatesReasoningSummaryParts(t *testing.T) {
	sse := strings.Join([]string{
		"event: response.created",
		`data: {"type":"response.created","response":{"id":"resp_parts"}}`,
		"",
		"event: response.reasoning_summary_text.delta",
		`data: {"type":"response.reasoning_summary_text.delta","item_id":"rs_parts","summary_index":0,"delta":"**First**"}`,
		"",
		"event: response.reasoning_summary_text.delta",
		`data: {"type":"response.reasoning_summary_text.delta","item_id":"rs_parts","summary_index":0,"delta":" continuation"}`,
		"",
		"event: response.reasoning_summary_text.delta",
		`data: {"type":"response.reasoning_summary_text.delta","item_id":"rs_parts","summary_index":1,"delta":"**Second**"}`,
		"",
		"event: response.completed",
		`data: {"type":"response.completed","response":{"id":"resp_parts"}}`,
		"",
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
	defer stream.Close()

	var streamed strings.Builder
	resp, err := core.HandleWith(stream, core.StreamHandler{
		Reasoning: func(text string) error {
			streamed.WriteString(text)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("HandleWith: %v", err)
	}
	want := "**First** continuation\n\n**Second**"
	if got := streamed.String(); got != want {
		t.Fatalf("streamed reasoning = %q, want %q", got, want)
	}
	if got := resp.Reasoning(); got != want {
		t.Fatalf("collected reasoning = %q, want %q", got, want)
	}
}
