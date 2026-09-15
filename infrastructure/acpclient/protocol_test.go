package acpclient

import (
	"encoding/json"
	"testing"
)

// A real tool_call_update carries structured content as an array plus
// rawInput/rawOutput. This is the shape that used to fail unmarshal and take
// the whole update down — including the title and status riding along.
func TestSessionUpdateParsesToolCallContentArray(t *testing.T) {
	raw := []byte(`{
		"sessionId": "s1",
		"update": {
			"sessionUpdate": "tool_call_update",
			"toolCallId": "call_1",
			"title": "Read SKILL.md",
			"kind": "read",
			"status": "completed",
			"content": [
				{"type": "content", "content": {"type": "text", "text": "file body"}},
				{"type": "diff", "path": "a.md", "oldText": "old", "newText": "new"}
			],
			"rawInput": {"path": "a.md"},
			"rawOutput": "file body"
		}
	}`)
	var params SessionUpdateParams
	if err := json.Unmarshal(raw, &params); err != nil {
		t.Fatalf("tool_call_update with content array must parse: %v", err)
	}
	update := params.Update
	if update.ToolCallID != "call_1" || update.Status != "completed" || update.Title != "Read SKILL.md" {
		t.Fatalf("tool fields = %+v", update)
	}
	if update.Content == nil || len(update.Content.Blocks) != 2 {
		t.Fatalf("content blocks = %+v", update.Content)
	}
	if got := update.Content.Blocks[0].Content; got == nil || got.Text != "file body" {
		t.Fatalf("text block = %+v", update.Content.Blocks[0])
	}
	diff := update.Content.Blocks[1]
	if diff.Path != "a.md" || diff.OldText == nil || *diff.OldText != "old" || diff.NewText != "new" {
		t.Fatalf("diff block = %+v", diff)
	}
	if string(update.RawInput) != `{"path": "a.md"}` {
		t.Fatalf("rawInput = %s", update.RawInput)
	}
	if string(update.RawOutput) != `"file body"` {
		t.Fatalf("rawOutput = %s", update.RawOutput)
	}
}

// Message chunks keep the single-block content shape.
func TestSessionUpdateParsesMessageChunkContent(t *testing.T) {
	raw := []byte(`{"sessionId": "s1", "update": {"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "hello"}}}`)
	var params SessionUpdateParams
	if err := json.Unmarshal(raw, &params); err != nil {
		t.Fatal(err)
	}
	if got := params.Update.Content.Text(); got != "hello" {
		t.Fatalf("content text = %q", got)
	}
	if params.Update.Content.Blocks != nil {
		t.Fatalf("blocks = %+v", params.Update.Content.Blocks)
	}
}

// Null content parses cleanly for status-only updates; absent content
// decodes to a nil pointer either way.
func TestSessionUpdateParsesNullContent(t *testing.T) {
	raw := []byte(`{"sessionId": "s1", "update": {"sessionUpdate": "tool_call_update", "toolCallId": "c", "status": "in_progress", "content": null}}`)
	var params SessionUpdateParams
	if err := json.Unmarshal(raw, &params); err != nil {
		t.Fatal(err)
	}
	if params.Update.Content.Text() != "" {
		t.Fatalf("null content must decode to empty text, got %q", params.Update.Content.Text())
	}
	if params.Update.Content != nil && len(params.Update.Content.Blocks) != 0 {
		t.Fatalf("blocks = %+v", params.Update.Content.Blocks)
	}
}

// UpdateContent must round-trip both shapes: the array form is what the
// server side emits for tool calls.
func TestUpdateContentRoundTrip(t *testing.T) {
	array := UpdateContent{Blocks: []ToolCallContent{{Type: "content", Content: &ContentBlock{Type: "text", Text: "x"}}}}
	encoded, err := json.Marshal(array)
	if err != nil {
		t.Fatal(err)
	}
	var back UpdateContent
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Blocks) != 1 || back.Blocks[0].Content == nil || back.Blocks[0].Content.Text != "x" {
		t.Fatalf("array round trip = %+v", back)
	}

	single := UpdateContent{Block: &ContentBlock{Type: "text", Text: "y"}}
	encoded, err = json.Marshal(single)
	if err != nil {
		t.Fatal(err)
	}
	var back2 UpdateContent
	if err := json.Unmarshal(encoded, &back2); err != nil {
		t.Fatal(err)
	}
	if back2.Text() != "y" {
		t.Fatalf("single round trip = %+v", back2)
	}
}
