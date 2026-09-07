package codex

import (
	"strings"
	"testing"

	"nusashell/infrastructure/ai/core"
)

func TestProviderStreamSeparatesReasoningSummaryParts(t *testing.T) {
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

	stream := &providerStream{
		raw:   NewResponsesStream(strings.NewReader(sse)),
		model: "gpt-5.6-luna",
	}
	response, err := core.Collect(stream)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	want := "**First** continuation\n\n**Second**"
	if got := response.Reasoning(); got != want {
		t.Fatalf("reasoning = %q, want %q", got, want)
	}
}
