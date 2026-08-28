package openai

import (
	"encoding/json"
	"strings"
	"testing"

	"nusashell/infrastructure/ai/core"
)

func TestBuildResponsesRequestPrependsCompactionBlobBeforeMessages(t *testing.T) {
	provider := mustProvider(t)
	blobItems := []json.RawMessage{
		json.RawMessage(`{"type":"compaction","encrypted_content":"ENC-1"}`),
	}
	blob, err := json.Marshal(blobItems)
	if err != nil {
		t.Fatalf("marshal blob: %v", err)
	}
	wire, err := provider.buildResponsesRequest(&ResponsesRequest{
		Model:          "gpt-5.2",
		CompactionBlob: string(blob),
		Messages: []core.Message{
			core.UserText("hello"),
		},
	}, false)
	if err != nil {
		t.Fatalf("buildResponsesRequest: %v", err)
	}
	items, ok := wire.Input.([]responsesInputItem)
	if !ok {
		t.Fatalf("Input = %#v, want []responsesInputItem", wire.Input)
	}
	if len(items) != 2 {
		t.Fatalf("items len = %d, want 2 (blob + message)", len(items))
	}
	blob0, _ := items[0].MarshalJSON()
	if !strings.Contains(string(blob0), `"compaction"`) || !strings.Contains(string(blob0), `"ENC-1"`) {
		t.Fatalf("items[0] raw = %s, want compaction/ENC-1", blob0)
	}
	if items[1].Type != "message" || items[1].Role != "user" {
		t.Fatalf("items[1] = %+v, want message/user", items[1])
	}
}

func TestBuildResponsesRequestBlobConvertsStringInputToItems(t *testing.T) {
	provider := mustProvider(t)
	blob, err := json.Marshal([]json.RawMessage{
		json.RawMessage(`{"type":"compaction","encrypted_content":"ENC"}`),
	})
	if err != nil {
		t.Fatalf("marshal blob: %v", err)
	}
	wire, err := provider.buildResponsesRequest(&ResponsesRequest{
		Model:          "gpt-5.2",
		Input:          "plain string input",
		CompactionBlob: string(blob),
	}, false)
	if err != nil {
		t.Fatalf("buildResponsesRequest: %v", err)
	}
	items, ok := wire.Input.([]responsesInputItem)
	if !ok {
		t.Fatalf("Input = %#v, want []responsesInputItem", wire.Input)
	}
	if len(items) != 2 {
		t.Fatalf("items len = %d, want 2", len(items))
	}
	blob0, _ := items[0].MarshalJSON()
	if !strings.Contains(string(blob0), `"compaction"`) {
		t.Fatalf("items[0] raw = %s, want compaction first", blob0)
	}
	if items[1].Type != "message" || items[1].Role != "user" {
		t.Fatalf("items[1] = %+v, want message/user from string input", items[1])
	}
}

func TestBuildResponsesRequestBlobOnlyWhenInputNil(t *testing.T) {
	provider := mustProvider(t)
	blob, err := json.Marshal([]json.RawMessage{
		json.RawMessage(`{"type":"compaction","encrypted_content":"ENC"}`),
		json.RawMessage(`{"type":"message","role":"user","content":[{"type":"input_text","text":"kept"}]}`),
	})
	if err != nil {
		t.Fatalf("marshal blob: %v", err)
	}
	wire, err := provider.buildResponsesRequest(&ResponsesRequest{
		Model:          "gpt-5.2",
		CompactionBlob: string(blob),
	}, false)
	if err != nil {
		t.Fatalf("buildResponsesRequest: %v", err)
	}
	items, ok := wire.Input.([]responsesInputItem)
	if !ok {
		t.Fatalf("Input = %#v, want []responsesInputItem", wire.Input)
	}
	if len(items) != 2 {
		t.Fatalf("items len = %d, want 2 (blob only)", len(items))
	}
	blob0, _ := items[0].MarshalJSON()
	if !strings.Contains(string(blob0), `"compaction"`) {
		t.Fatalf("items[0] raw = %s, want compaction", blob0)
	}
}

func TestBuildResponsesRequestRejectsNonArrayCompactionBlob(t *testing.T) {
	provider := mustProvider(t)
	_, err := provider.buildResponsesRequest(&ResponsesRequest{
		Model:          "gpt-5.2",
		CompactionBlob: `{"not":"an array"}`,
		Messages:       []core.Message{core.UserText("hi")},
	}, false)
	if err == nil || !strings.Contains(err.Error(), "compaction_blob") {
		t.Fatalf("expected compaction_blob error, got %v", err)
	}
}

func TestApplyResponsesProviderOptionsSetsCompactionBlob(t *testing.T) {
	req := &ResponsesRequest{
		providerOptions: map[string]any{
			ProviderOptionCompactionBlob: `[{"type":"compaction","encrypted_content":"ENC"}]`,
		},
	}
	if err := applyResponsesProviderOptions(req, false); err != nil {
		t.Fatalf("applyResponsesProviderOptions: %v", err)
	}
	if req.CompactionBlob != `[{"type":"compaction","encrypted_content":"ENC"}]` {
		t.Fatalf("CompactionBlob = %q", req.CompactionBlob)
	}
}
