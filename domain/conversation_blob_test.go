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
