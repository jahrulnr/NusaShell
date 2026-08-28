package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"nusashell/application"
	"nusashell/domain"
)

func TestAdapterCompactServerPrependsBlobAndLiveMessages(t *testing.T) {
	var capturedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses/compact" {
			t.Errorf("path = %q, want /v1/responses/compact", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &capturedBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"cmp_1","object":"response.compaction",
			"output":[
				{"type":"compaction","encrypted_content":"ENC-NEW"},
				{"type":"message","role":"user","content":[{"type":"input_text","text":"kept"}]}
			],
			"usage":{"input_tokens":100,"output_tokens":20,"total_tokens":120}
		}`))
	}))
	defer srv.Close()

	adapter := &Adapter{
		Driver:       domain.ProviderDriverOpenAI,
		ProviderKind: domain.ProviderResponses,
		BaseURL:      srv.URL + "/v1",
		APIKey:       "k",
		Client:       srv.Client(),
	}
	req := application.ChatRequest{
		Model:          "gpt-5.2",
		System:         "be brief",
		CompactionBlob: `[{"type":"compaction","encrypted_content":"ENC-OLD"}]`,
		Messages: []application.ChatMessage{
			{Role: "user", Content: "hello"},
		},
	}
	result, err := adapter.CompactServer(context.Background(), req)
	if err != nil {
		t.Fatalf("CompactServer: %v", err)
	}
	// The returned blob is the marshalled output array.
	var blobItems []json.RawMessage
	if err := json.Unmarshal([]byte(result.Blob), &blobItems); err != nil {
		t.Fatalf("result.Blob not a JSON array: %v", err)
	}
	if len(blobItems) != 2 {
		t.Fatalf("blob items = %d, want 2", len(blobItems))
	}
	if result.InputTokens != 100 || result.OutputTokens != 20 || result.TotalTokens != 120 {
		t.Fatalf("usage = %+v, want {100 20 120}", result)
	}
	// The wire input must be an array with the old blob item first, then the live user message.
	inputArr, ok := capturedBody["input"].([]any)
	if !ok {
		t.Fatalf("input = %#v, want array", capturedBody["input"])
	}
	if len(inputArr) != 2 {
		t.Fatalf("wire input len = %d, want 2 (blob + live)", len(inputArr))
	}
	first := inputArr[0].(map[string]any)
	if first["type"] != "compaction" || first["encrypted_content"] != "ENC-OLD" {
		t.Fatalf("wire input[0] = %#v, want compaction/ENC-OLD", first)
	}
	second := inputArr[1].(map[string]any)
	if second["type"] != "message" || second["role"] != "user" {
		t.Fatalf("wire input[1] = %#v, want message/user", second)
	}
	if capturedBody["instructions"] != "be brief" {
		t.Fatalf("instructions = %#v, want 'be brief'", capturedBody["instructions"])
	}
	if capturedBody["model"] != "gpt-5.2" {
		t.Fatalf("model = %#v, want gpt-5.2", capturedBody["model"])
	}
}

func TestAdapterCompactServerRejectsNonOpenAIResponsesDriver(t *testing.T) {
	adapter := &Adapter{
		Driver:       domain.ProviderDriverOpenRouter,
		ProviderKind: domain.ProviderResponses,
	}
	_, err := adapter.CompactServer(context.Background(), application.ChatRequest{Model: "gpt-5.2"})
	if err == nil {
		t.Fatal("expected error for non-openai driver, got nil")
	}
}
