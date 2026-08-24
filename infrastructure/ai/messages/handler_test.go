package messages

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nusashell/application"
	"nusashell/domain"
)

func TestAnthropicAdapterEncodesAttachments(t *testing.T) {
	req := application.ChatRequest{
		Model: "test-model",
		Messages: []application.ChatMessage{{
			Role:    "user",
			Content: "Review these files",
			Attachments: []domain.Attachment{
				{Type: "text", Name: "notes.txt", MediaType: "text/plain", Content: "Local notes"},
				{Type: "image", Name: "pixel.png", MediaType: "image/png", DataURL: "data:image/png;base64,iVBORw0KGgo="},
				{Type: "file", Name: "brief.pdf", MediaType: "application/pdf", DataURL: "data:application/pdf;base64,JVBERi0="},
			},
		}},
	}

	body := marshalRequest(t, buildAnthropicRequest(req, false))
	if !containsAll(body, `"type":"image"`, `"type":"document"`, "brief.pdf", "[Attached text file: notes.txt - full content included below]\\n\\nLocal notes") {
		t.Fatalf("anthropic attachment mapping = %s", body)
	}
}

func TestMergeAnthropicUsageKeepsMessageStartInput(t *testing.T) {
	start := anthropicUsageToChat(anthropicUsage{InputTokens: 120, CacheReadInputTokens: 80})
	got := mergeAnthropicUsage(start, anthropicUsage{OutputTokens: 15})
	if got.InputTokens != 120 || got.OutputTokens != 15 || got.CacheRead != 80 {
		t.Fatalf("merged usage = %+v", got)
	}
}

func containsAll(value string, want ...string) bool {
	for _, item := range want {
		if !strings.Contains(value, item) {
			return false
		}
	}
	return true
}

func marshalRequest(t *testing.T, value any) string {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// TestStreamCompletedEmptyFallsBackToComplete verifies that an Anthropic
// messages stream which terminates cleanly (message_stop) but carries no
// content/reasoning/tool calls falls back to a non-streaming Complete
// request instead of failing the turn with "provider returned empty
// content". Unstable gateways that return a 200 with an empty SSE body
// often serve non-streaming fine.
func TestStreamCompletedEmptyFallsBackToComplete(t *testing.T) {
	var streamCalls, completeCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Stream bool `json:"stream"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Stream {
			streamCalls++
			w.Header().Set("Content-Type", "text/event-stream")
			// Completed but empty: message_start + message_stop, no deltas.
			_, _ = w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n"))
			_, _ = w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
			return
		}
		completeCalls++
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "Hello from complete"},
			},
		})
	}))
	defer server.Close()

	adapter := &Adapter{BaseURL: server.URL + "/v1", Client: server.Client()}
	var got string
	resp, err := adapter.Stream(context.Background(), application.ChatRequest{
		Model:     "test-model",
		MaxTokens: 64,
		Messages:  []application.ChatMessage{{Role: "user", Content: "hi"}},
	}, func(delta string) { got += delta }, nil)
	if err != nil {
		t.Fatalf("Stream returned error on completed-empty stream, want fallback to Complete: %v", err)
	}
	if streamCalls != 1 {
		t.Errorf("stream request count = %d, want 1", streamCalls)
	}
	if completeCalls != 1 {
		t.Errorf("non-streaming fallback count = %d, want 1", completeCalls)
	}
	if resp.Content != "Hello from complete" {
		t.Errorf("Content = %q, want %q", resp.Content, "Hello from complete")
	}
	if got != "Hello from complete" {
		t.Errorf("onDelta delivered %q, want %q", got, "Hello from complete")
	}
}

// TestStreamCompletedEmptyFallbackAlsoEmptyReturnsRetryable verifies that
// when both the streaming response and the non-streaming fallback return
// empty content, the adapter surfaces a retryable UpstreamError so the
// agent retry loop can re-request rather than silently ending the turn.
func TestStreamCompletedEmptyFallbackAlsoEmptyReturnsRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Stream bool `json:"stream"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n"))
			_, _ = w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": ""},
			},
		})
	}))
	defer server.Close()

	adapter := &Adapter{BaseURL: server.URL + "/v1", Client: server.Client()}
	_, err := adapter.Stream(context.Background(), application.ChatRequest{
		Model:     "test-model",
		MaxTokens: 64,
		Messages:  []application.ChatMessage{{Role: "user", Content: "hi"}},
	}, func(string) {}, nil)
	if err == nil {
		t.Fatalf("Stream with empty stream + empty fallback should return a retryable error, got nil")
	}
	var upstream *application.UpstreamError
	if !errors.As(err, &upstream) {
		t.Fatalf("error must be *application.UpstreamError, got %T: %v", err, err)
	}
	if !upstream.Temporary {
		t.Errorf("UpstreamError.Temporary = false, want true (retryable)")
	}
}
