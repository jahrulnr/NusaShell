package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConversationEstimateTokensIncludesCompactionBlob(t *testing.T) {
	c := &Conversation{
		Messages: []Message{
			{Role: RoleUser, Content: "hello world"},
		},
	}
	withoutBlob := c.EstimateTokens()
	// A 300-char opaque blob at ~chars/3 adds 100 tokens.
	c.CompactionBlob = string(make([]byte, 300))
	withBlob := c.EstimateTokens()
	if withBlob <= withoutBlob {
		t.Fatalf("EstimateTokens with blob = %d, want > %d (without blob)", withBlob, withoutBlob)
	}
	if got, want := withBlob-withoutBlob, 100; got != want {
		t.Fatalf("blob token contribution = %d, want %d", got, want)
	}
}

func TestConversationEstimateTokensEmptyBlobUnchanged(t *testing.T) {
	c := &Conversation{
		Messages: []Message{
			{Role: RoleUser, Content: "hello world"},
		},
	}
	base := c.EstimateTokens()
	c.CompactionBlob = ""
	if got := c.EstimateTokens(); got != base {
		t.Fatalf("EstimateTokens with empty blob = %d, want %d", got, base)
	}
}

func TestMessageReasoningExtraOmittedWhenEmpty(t *testing.T) {
	raw, err := json.Marshal(Message{ID: "m1", Role: RoleAssistant, Content: "ok", Reasoning: "think"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "ReasoningExtra") {
		t.Fatalf("empty ReasoningExtra must be omitted, got %s", raw)
	}
}

func TestMessageReasoningExtraPersistedWhenSet(t *testing.T) {
	extra := json.RawMessage(`{"type":"reasoning","encrypted_content":"ENC-1"}`)
	raw, err := json.Marshal(Message{
		ID: "m1", Role: RoleAssistant, Reasoning: "think", ReasoningExtra: extra,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("probe: %v", err)
	}
	if string(probe["ReasoningExtra"]) != string(extra) {
		t.Fatalf("ReasoningExtra = %s, want %s", probe["ReasoningExtra"], extra)
	}
	if strings.Contains(string(raw), "ENC-1") == false {
		t.Fatalf("expected encrypted payload in storage JSON: %s", raw)
	}
}

// TestMessageReasoningExtraForPersistedWhenSet proves the producer marker
// round-trips through storage JSON with the extra it describes, and is
// omitted when empty. Empty-marker messages stay on the legacy unmarked
// replay rules.
func TestMessageReasoningExtraForPersistedWhenSet(t *testing.T) {
	raw, err := json.Marshal(Message{
		ID: "m1", Role: RoleAssistant, Reasoning: "think",
		ReasoningExtra:    json.RawMessage(`{"signature":"sig-1"}`),
		ReasoningExtraFor: "anthropic",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("probe: %v", err)
	}
	if string(probe["ReasoningExtraFor"]) != `"anthropic"` {
		t.Fatalf("ReasoningExtraFor = %s, want %q", probe["ReasoningExtraFor"], `"anthropic"`)
	}

	var decoded Message
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.ReasoningExtraFor != "anthropic" || string(decoded.ReasoningExtra) != `{"signature":"sig-1"}` {
		t.Fatalf("decoded message = %+v, want marker and extra intact", decoded)
	}

	empty, err := json.Marshal(Message{ID: "m2", Role: RoleAssistant, Content: "ok"})
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	if strings.Contains(string(empty), "ReasoningExtraFor") {
		t.Fatalf("empty ReasoningExtraFor must be omitted, got %s", empty)
	}
}

// TestMessageReasoningExtraUpstreamPersistedWhenSet proves the gateway
// upstream provenance round-trips through storage JSON alongside the extra
// it describes, and is omitted when empty.
func TestMessageReasoningExtraUpstreamPersistedWhenSet(t *testing.T) {
	raw, err := json.Marshal(Message{
		ID: "m1", Role: RoleAssistant, Reasoning: "think",
		ReasoningExtra:         json.RawMessage(`[{"type":"reasoning.encrypted","data":"c"}]`),
		ReasoningExtraFor:      "openrouter",
		ReasoningExtraUpstream: "Nvidia",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("probe: %v", err)
	}
	if string(probe["ReasoningExtraUpstream"]) != `"Nvidia"` {
		t.Fatalf("ReasoningExtraUpstream = %s, want %q", probe["ReasoningExtraUpstream"], `"Nvidia"`)
	}
	var decoded Message
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.ReasoningExtraUpstream != "Nvidia" {
		t.Fatalf("decoded upstream = %q, want Nvidia", decoded.ReasoningExtraUpstream)
	}
	empty, err := json.Marshal(Message{ID: "m2", Role: RoleAssistant, Content: "ok"})
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	if strings.Contains(string(empty), "ReasoningExtraUpstream") {
		t.Fatalf("empty ReasoningExtraUpstream must be omitted, got %s", empty)
	}
}

// TestMessageServedModelPersistedWhenSet proves the provider-reported served
// model round-trips through storage JSON and is omitted when empty so legacy
// transcripts keep a single Model field.
func TestMessageServedModelPersistedWhenSet(t *testing.T) {
	raw, err := json.Marshal(Message{
		ID: "m1", Role: RoleAssistant, Model: "openrouter/free", ServedModel: "nvidia/nemotron-3-super-120b-a12b:free",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded Message
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.ServedModel != "nvidia/nemotron-3-super-120b-a12b:free" || decoded.Model != "openrouter/free" {
		t.Fatalf("decoded message = %+v, want served model and requested alias intact", decoded)
	}
	empty, err := json.Marshal(Message{ID: "m2", Role: RoleAssistant, Content: "ok"})
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	if strings.Contains(string(empty), "ServedModel") {
		t.Fatalf("empty ServedModel must be omitted, got %s", empty)
	}
}
