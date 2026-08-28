package openai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"nusashell/infrastructure/ai/core"
)

func TestProviderCompactPostsToResponsesCompact(t *testing.T) {
	var capturedPath string
	var capturedBody map[string]any
	provider, err := New(Config{
		API:     APIResponses,
		APIKey:  "test-key",
		BaseURL: "https://example.test",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			capturedPath = req.URL.Path
			if err := json.NewDecoder(req.Body).Decode(&capturedBody); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			return jsonResponse(http.StatusOK, `{
				"id":"cmp_123",
				"object":"response.compaction",
				"output":[
					{"type":"compaction","encrypted_content":"ENC-BLOB"},
					{"type":"message","role":"user","content":[{"type":"input_text","text":"kept"}]}
				],
				"usage":{"input_tokens":100,"output_tokens":20,"total_tokens":120}
			}`), nil
		}),
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	resp, err := provider.Compact(context.Background(), ResponsesCompactRequest{
		Model:        "gpt-5.2",
		Input:        []map[string]any{{"type": "message", "role": "user", "content": "hi"}},
		Instructions: "be brief",
	})
	if err != nil {
		t.Fatalf("Compact returned error: %v", err)
	}
	if capturedPath != "/v1/responses/compact" {
		t.Fatalf("path = %q, want /v1/responses/compact", capturedPath)
	}
	if capturedBody["model"] != "gpt-5.2" {
		t.Fatalf("model = %#v, want gpt-5.2", capturedBody["model"])
	}
	if capturedBody["instructions"] != "be brief" {
		t.Fatalf("instructions = %#v", capturedBody["instructions"])
	}
	if _, ok := capturedBody["input"].([]any); !ok {
		t.Fatalf("input = %#v, want JSON array", capturedBody["input"])
	}
	if len(resp.Output) != 2 {
		t.Fatalf("Output len = %d, want 2", len(resp.Output))
	}
	if string(resp.Output[0]) == "" {
		t.Fatalf("Output[0] is empty")
	}
	var first map[string]any
	if err := json.Unmarshal(resp.Output[0], &first); err != nil {
		t.Fatalf("Output[0] not valid JSON: %v", err)
	}
	if first["type"] != "compaction" {
		t.Fatalf("Output[0].type = %#v, want compaction", first["type"])
	}
	if resp.Usage.InputTokens != 100 || resp.Usage.OutputTokens != 20 || resp.Usage.TotalTokens != 120 {
		t.Fatalf("Usage = %+v, want {100 20 120}", resp.Usage)
	}
}

func TestProviderCompactNon2xxReturnsHTTPError(t *testing.T) {
	provider, err := New(Config{
		API:     APIResponses,
		APIKey:  "test-key",
		BaseURL: "https://example.test",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusNotFound, `{"error":{"message":"not found"}}`), nil
		}),
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = provider.Compact(context.Background(), ResponsesCompactRequest{
		Model: "gpt-5.2",
		Input: []map[string]any{{"type": "message", "role": "user", "content": "hi"}},
	})
	if err == nil {
		t.Fatalf("expected error for 404, got nil")
	}
	var httpErr *core.LiteLLMError
	if !errors.As(err, &httpErr) {
		t.Fatalf("expected *core.LiteLLMError, got %T: %v", err, err)
	}
	if httpErr.StatusCode != http.StatusNotFound {
		t.Fatalf("StatusCode = %d, want 404", httpErr.StatusCode)
	}
}

func TestProviderCompactOmitsEmptyInstructions(t *testing.T) {
	var capturedBody map[string]any
	provider, err := New(Config{
		API:     APIResponses,
		APIKey:  "test-key",
		BaseURL: "https://example.test",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if err := json.NewDecoder(req.Body).Decode(&capturedBody); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			return jsonResponse(http.StatusOK, `{
				"id":"cmp_1","object":"response.compaction",
				"output":[{"type":"compaction","encrypted_content":"ENC"}],
				"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}
			}`), nil
		}),
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if _, err := provider.Compact(context.Background(), ResponsesCompactRequest{
		Model: "gpt-5.2",
		Input: []map[string]any{{"type": "message", "role": "user", "content": "hi"}},
	}); err != nil {
		t.Fatalf("Compact returned error: %v", err)
	}
	if _, ok := capturedBody["instructions"]; ok {
		t.Fatalf("instructions should be omitted when empty, got %#v", capturedBody["instructions"])
	}
}
