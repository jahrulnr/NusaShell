package codex

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNewResponsesAPIRequestDefaultsStatelessStream(t *testing.T) {
	history := []ResponseItem{NewTextMessage(RoleUser, ContentInputText, "hi")}
	req := NewResponsesAPIRequest("gpt-5.6-luna", history)
	if req.Store {
		t.Fatalf("Store = true, want false")
	}
	if !req.Stream {
		t.Fatalf("Stream = false, want true")
	}
	if req.Model != "gpt-5.6-luna" {
		t.Fatalf("Model = %q", req.Model)
	}
	if len(req.Input) != 1 || req.Input[0].Content[0].Text != "hi" {
		t.Fatalf("Input = %+v, want copied history", req.Input)
	}
	// The request owns a copy: mutating the original history must not leak.
	history[0].Content[0].Text = "mutated"
	if req.Input[0].Content[0].Text != "hi" {
		t.Fatalf("NewResponsesAPIRequest did not copy input")
	}
}

func TestResponsesAPIRequestWireShape(t *testing.T) {
	req := NewResponsesAPIRequest("gpt-5.6-luna", []ResponseItem{NewTextMessage(RoleUser, ContentInputText, "hi")})
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var probe map[string]any
	if err := json.Unmarshal(body, &probe); err != nil {
		t.Fatalf("probe decode: %v", err)
	}
	for _, key := range []string{"model", "input", "store", "stream"} {
		if _, ok := probe[key]; !ok {
			t.Fatalf("wire body missing %q: %s", key, body)
		}
	}
	for _, key := range []string{"instructions", "tools", "service_tier", "prompt_cache_key"} {
		if _, ok := probe[key]; ok {
			t.Fatalf("wire body unexpectedly carries %q: %s", key, body)
		}
	}
	if probe["store"] != false || probe["stream"] != true {
		t.Fatalf("store/stream = %v/%v, want false/true", probe["store"], probe["stream"])
	}
}

func TestBuildCompactionV2RequestAppendsTriggerLast(t *testing.T) {
	prompt := CompactionPrompt{
		Model:             "gpt-5.6-luna",
		Instructions:      "compact this",
		Tools:             []json.RawMessage{json.RawMessage(`{"type":"function","name":"noop"}`)},
		ParallelToolCalls: true,
		Input: []ResponseItem{
			NewTextMessage(RoleUser, ContentInputText, "hello"),
			{Type: ItemTypeReasoning, ID: "rs_1", EncryptedContent: "ENC-1"},
		},
	}
	wire := BuildCompactionV2Request(prompt)
	if wire.Store || !wire.Stream {
		t.Fatalf("store/stream = %v/%v, want false/true", wire.Store, wire.Stream)
	}
	if len(wire.Input) != 3 {
		t.Fatalf("input len = %d, want 3 (history + trigger)", len(wire.Input))
	}
	last := wire.Input[len(wire.Input)-1]
	if !last.IsCompactionTrigger() {
		t.Fatalf("last input item = %+v, want compaction_trigger", last)
	}
	// The original prompt input never carries the trigger.
	if prompt.Input[len(prompt.Input)-1].IsCompactionTrigger() {
		t.Fatalf("prompt input was mutated with a trigger")
	}
	if len(wire.Tools) != 1 || string(wire.Tools[0]) != `{"type":"function","name":"noop"}` {
		t.Fatalf("tools = %s, want passthrough", wire.Tools)
	}
	// The trigger item serializes as the minimal control object and the
	// history items keep their opaque bytes.
	body, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(body), `{"type":"compaction_trigger"}`) {
		t.Fatalf("request body missing trigger control: %s", body)
	}
	if !strings.Contains(string(body), "ENC-1") {
		t.Fatalf("request body missing encrypted reasoning replay: %s", body)
	}
}

func TestAppendCompactionTriggerCopies(t *testing.T) {
	input := []ResponseItem{NewTextMessage(RoleUser, ContentInputText, "hi")}
	out := AppendCompactionTrigger(input)
	if len(input) != 1 {
		t.Fatalf("original input mutated: len = %d", len(input))
	}
	if len(out) != 2 || !out[1].IsCompactionTrigger() {
		t.Fatalf("out = %+v, want [message, trigger]", out)
	}
}
