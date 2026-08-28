package openai

import (
	"encoding/json"
	"strings"
	"testing"

	"nusashell/infrastructure/ai/core"
)

func TestConvertResponsesResponseCapturesCompactionItem(t *testing.T) {
	compactionRaw := json.RawMessage(`{"id":"comp_001","type":"compaction","encrypted_content":"ENC-XYZ","summary":[]}`)
	resp := &responsesResponse{
		Model:  "gpt-5.2",
		Status: "completed",
		Output: []responsesOutputItem{
			{Type: "compaction", Raw: compactionRaw},
			{Type: "message", Content: []responsesContentItem{{Type: "output_text", Text: "answer"}}},
		},
	}
	out, err := convertResponsesResponse(resp, "")
	if err != nil {
		t.Fatalf("convertResponsesResponse: %v", err)
	}
	if len(out.CompactionItems) != 1 {
		t.Fatalf("CompactionItems len = %d, want 1", len(out.CompactionItems))
	}
	if !strings.Contains(string(out.CompactionItems[0]), `"compaction"`) || !strings.Contains(string(out.CompactionItems[0]), `"ENC-XYZ"`) {
		t.Fatalf("CompactionItems[0] = %s, want compaction/ENC-XYZ", out.CompactionItems[0])
	}
}

func TestConvertResponsesResponseHandlesMultipleCompactionItems(t *testing.T) {
	resp := &responsesResponse{
		Model:  "gpt-5.2",
		Status: "completed",
		Output: []responsesOutputItem{
			{Type: "compaction", Raw: json.RawMessage(`{"type":"compaction","encrypted_content":"ENC-1"}`)},
			{Type: "message", Content: []responsesContentItem{{Type: "output_text", Text: "text"}}},
			{Type: "compaction", Raw: json.RawMessage(`{"type":"compaction","encrypted_content":"ENC-2"}`)},
		},
	}
	out, err := convertResponsesResponse(resp, "")
	if err != nil {
		t.Fatalf("convertResponsesResponse: %v", err)
	}
	if len(out.CompactionItems) != 2 {
		t.Fatalf("CompactionItems len = %d, want 2", len(out.CompactionItems))
	}
	if !strings.Contains(string(out.CompactionItems[0]), "ENC-1") {
		t.Fatalf("CompactionItems[0] = %s, want ENC-1", out.CompactionItems[0])
	}
	if !strings.Contains(string(out.CompactionItems[1]), "ENC-2") {
		t.Fatalf("CompactionItems[1] = %s, want ENC-2", out.CompactionItems[1])
	}
}

func TestResponsesStreamCapturesCompactionItemFromOutputItemDone(t *testing.T) {
	sse := strings.Join([]string{
		`event: response.created`,
		`data: {"type":"response.created","sequence_number":1,"response":{"id":"resp_1"}}`,
		``,
		`event: response.output_item.done`,
		`data: {"type":"response.output_item.done","sequence_number":2,"output_index":0,"item":{"id":"comp_001","type":"compaction","encrypted_content":"ENC-STREAM","summary":[]}}`,
		``,
		`event: response.completed`,
		`data: {"type":"response.completed","sequence_number":3,"response":{"model":"gpt-5.2","status":"completed","usage":{"input_tokens":10,"output_tokens":20,"total_tokens":30}}}`,
		``,
	}, "\n")
	stream := newResponsesStream(streamResponse(sse), "gpt-5.2")
	resp, err := core.Collect(stream)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(resp.CompactionItems) != 1 {
		t.Fatalf("CompactionItems len = %d, want 1", len(resp.CompactionItems))
	}
	if !strings.Contains(string(resp.CompactionItems[0]), "ENC-STREAM") {
		t.Fatalf("CompactionItems[0] = %s, want ENC-STREAM", resp.CompactionItems[0])
	}
}

func TestBuildResponsesRequestSetsContextManagement(t *testing.T) {
	provider := mustProvider(t)
	wire, err := provider.buildResponsesRequest(&ResponsesRequest{
		Model: "gpt-5.2",
		Input: "hello",
		providerOptions: map[string]any{
			ProviderOptionContextManagement: []map[string]any{
				{"type": "compaction", "compact_threshold": 360000},
			},
		},
	}, false)
	if err != nil {
		t.Fatalf("buildResponsesRequest: %v", err)
	}
	if len(wire.ContextManagement) != 1 {
		t.Fatalf("ContextManagement len = %d, want 1", len(wire.ContextManagement))
	}
	if wire.ContextManagement[0]["type"] != "compaction" {
		t.Fatalf("ContextManagement[0].type = %v, want compaction", wire.ContextManagement[0]["type"])
	}
	if wire.ContextManagement[0]["compact_threshold"] != 360000 {
		t.Fatalf("ContextManagement[0].compact_threshold = %v, want 360000", wire.ContextManagement[0]["compact_threshold"])
	}
}

func TestBuildResponsesRequestPrependsCompactionItems(t *testing.T) {
	provider := mustProvider(t)
	items := `[{"type":"compaction","encrypted_content":"ENC-OLD"}]`
	wire, err := provider.buildResponsesRequest(&ResponsesRequest{
		Model:           "gpt-5.2",
		Input:           "hello",
		CompactionItems: items,
	}, false)
	if err != nil {
		t.Fatalf("buildResponsesRequest: %v", err)
	}
	itemsArr, ok := wire.Input.([]responsesInputItem)
	if !ok {
		t.Fatalf("Input = %T, want []responsesInputItem", wire.Input)
	}
	if len(itemsArr) != 2 {
		t.Fatalf("items len = %d, want 2 (compaction + message)", len(itemsArr))
	}
	blob0, _ := itemsArr[0].MarshalJSON()
	if !strings.Contains(string(blob0), `"compaction"`) || !strings.Contains(string(blob0), `"ENC-OLD"`) {
		t.Fatalf("items[0] raw = %s, want compaction/ENC-OLD", blob0)
	}
	if itemsArr[1].Type != "message" || itemsArr[1].Role != "user" {
		t.Fatalf("items[1] = %+v, want message/user", itemsArr[1])
	}
}

func TestOptionContextManagementRejectsNonCompactionType(t *testing.T) {
	_, err := optionContextManagement("context_management", []map[string]any{
		{"type": "something_else"},
	})
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("expected not supported error, got %v", err)
	}
}

func TestOptionContextManagementRejectsMissingType(t *testing.T) {
	_, err := optionContextManagement("context_management", []map[string]any{
		{"compact_threshold": 1000},
	})
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("expected missing type error, got %v", err)
	}
}
