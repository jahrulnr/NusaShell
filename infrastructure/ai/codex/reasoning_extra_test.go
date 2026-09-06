package codex

import (
	"encoding/json"
	"strings"
	"testing"

	"nusashell/infrastructure/ai/core"
)

func TestReasoningItemReplaysEncryptedContentFromExtra(t *testing.T) {
	extra := json.RawMessage(`{"id":"rs_1","type":"reasoning","summary":[{"type":"summary_text","text":"think"}],"encrypted_content":"ENC-REASON"}`)
	item, err := reasoningItem(core.ReasoningBlock{Text: "think", Extra: extra})
	if err != nil {
		t.Fatalf("reasoningItem: %v", err)
	}
	if item.Type != ItemTypeReasoning {
		t.Fatalf("Type = %q, want reasoning", item.Type)
	}
	body, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(body), `"encrypted_content":"ENC-REASON"`) {
		t.Fatalf("wire item missing encrypted_content: %s", body)
	}
}
