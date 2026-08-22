package responses

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nusashell/application"
	"nusashell/domain"
)

func TestResponsesAdapterEncodesAttachments(t *testing.T) {
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

	body := marshalRequest(t, buildResponsesRequest(req, false))
	if !containsAll(body, "input_image", "input_file", "brief.pdf", "[Attached text file: notes.txt - full content included below]\\n\\nLocal notes") {
		t.Fatalf("responses attachment mapping = %s", body)
	}
}

func TestResponsesAdapterEncodesToolResultAttachments(t *testing.T) {
	req := application.ChatRequest{
		Model: "test-model",
		Messages: []application.ChatMessage{
			{
				Role:      "assistant",
				ToolCalls: []domain.ToolCall{{ID: "call_1", Name: "generate_image", Args: `{"prompt":"boat"}`}},
			},
			{
				Role: "tool",
				ToolResult: &application.ToolResult{
					ToolCallID: "call_1",
					Name:       "generate_image",
					Content:    "Image saved to /tmp/gen-call_1.png.",
					Attachments: []domain.Attachment{{
						Type: "image", Name: "gen-call_1.png", MediaType: "image/png",
						DataURL: "data:image/png;base64,iVBORw0KGgo=",
					}},
				},
			},
		},
	}
	body := marshalRequest(t, buildResponsesRequest(req, false))
	if !containsAll(body, "function_call_output", "input_text", "input_image", "data:image/png;base64,iVBORw0KGgo=", "Image saved to") {
		t.Fatalf("tool result attachments = %s", body)
	}
	if strings.Contains(body, `"output":"Image saved`) {
		t.Fatalf("tool output with attachments must be an array, got %s", body)
	}
}

// TestResponsesAudioAttachmentUsesInputAudio: audio attachments must be
// encoded as input_audio (not input_image) on the Responses API. Sending
// audio as input_image causes providers like Nvidia to attempt image
// decoding and fail with "Failed to load image" errors.
func TestResponsesAudioAttachmentUsesInputAudio(t *testing.T) {
	req := application.ChatRequest{
		Model: "test-model",
		Messages: []application.ChatMessage{{
			Role:    "user",
			Content: "Listen to this",
			Attachments: []domain.Attachment{
				{Type: "audio", Name: "rec.mp3", MediaType: "audio/mpeg", DataURL: "data:audio/mpeg;base64,//NkxAAAA"},
			},
		}},
	}
	body := marshalRequest(t, buildResponsesRequest(req, false))
	if !containsAll(body, "input_audio", "//NkxAAAA", "mp3") {
		t.Fatalf("audio attachment must use input_audio with base64 data and format, got %s", body)
	}
	if strings.Contains(body, "input_image") {
		t.Fatalf("audio attachment must NOT use input_image, got %s", body)
	}
}

// TestResponsesToolResultAudioUsesInputAudio: audio attachments in tool
// results (e.g. read_audio) must also use input_audio, not input_image.
func TestResponsesToolResultAudioUsesInputAudio(t *testing.T) {
	req := application.ChatRequest{
		Model: "test-model",
		Messages: []application.ChatMessage{
			{Role: "assistant", ToolCalls: []domain.ToolCall{{ID: "c1", Name: "read_audio", Args: `{}`}}},
			{Role: "tool", ToolResult: &application.ToolResult{
				ToolCallID: "c1", Name: "read_audio", Content: "Audio loaded.",
				Attachments: []domain.Attachment{
					{Type: "audio", Name: "rec.mp3", MediaType: "audio/mpeg", DataURL: "data:audio/mpeg;base64,//NkxAAAA"},
				},
			}},
		},
	}
	body := marshalRequest(t, buildResponsesRequest(req, false))
	if !containsAll(body, "input_audio", "//NkxAAAA", "mp3") {
		t.Fatalf("tool result audio must use input_audio, got %s", body)
	}
	if strings.Contains(body, "input_image") {
		t.Fatalf("tool result audio must NOT use input_image, got %s", body)
	}
}

func TestResponsesToolResultWithoutAttachmentsStaysString(t *testing.T) {
	req := application.ChatRequest{
		Model: "test-model",
		Messages: []application.ChatMessage{{
			Role: "tool",
			ToolResult: &application.ToolResult{
				ToolCallID: "call_2", Name: "docs_search", Content: "docs/mcp.md",
			},
		}},
	}
	body := marshalRequest(t, buildResponsesRequest(req, false))
	if !strings.Contains(body, `"output":"docs/mcp.md"`) {
		t.Fatalf("text-only tool output must stay a string, got %s", body)
	}
}

func TestTextOnlyRequestsKeepScalarContent(t *testing.T) {
	req := application.ChatRequest{Model: "test-model", Messages: []application.ChatMessage{{Role: "user", Content: "Hello"}}}
	body, err := json.Marshal(buildResponsesRequest(req, false))
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Input []struct {
			Content any `json:"content"`
		} `json:"input"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Input) != 1 {
		t.Fatalf("input = %s", body)
	}
	if _, ok := decoded.Input[0].Content.(string); !ok {
		t.Fatalf("text-only content must stay a string, got %T", decoded.Input[0].Content)
	}
}

func TestResponsesListModelsParsesOpenRouterFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{
					"id":             "openai/o3-mini",
					"context_length": 200000,
					"max_tokens":     100000,
					"description":    "Reasoning model.",
					"pricing":        map[string]any{"prompt": "0.0000011"},
				},
			},
		})
	}))
	defer server.Close()

	adapter := &Adapter{BaseURL: server.URL + "/v1", Client: server.Client()}
	models, err := adapter.ListModels(context.Background(), "test-key")
	if err != nil {
		t.Fatalf("ListModels failed: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	m := models[0]
	if m.Context != 200000 || m.MaxOutput != 100000 || m.Description != "Reasoning model." {
		t.Fatalf("enriched model = %+v, want context=200000 max_output=100000", m)
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

// TestResponsesSanitizesHallucinatedToolNames verifies that tool names with
// characters outside the OpenAI Responses API pattern ^[a-zA-Z0-9_-]+$ are
// sanitized on the wire so a conversation with a hallucinated tool call
// (e.g. "terminal:exec" produced by DeepSeek) can still be replayed against
// a Responses API provider without HTTP 400 "Invalid 'input[N].name'".
func TestResponsesSanitizesHallucinatedToolNames(t *testing.T) {
	req := application.ChatRequest{
		Model: "test-model",
		Messages: []application.ChatMessage{
			{Role: "user", Content: "list files"},
			{
				Role: "assistant",
				ToolCalls: []domain.ToolCall{
					{ID: "call_1", Name: "terminal:exec", Args: `{"command":"ls"}`},
				},
			},
			{
				Role: "tool",
				ToolResult: &application.ToolResult{
					ToolCallID: "call_1",
					Content:    "file1\nfile2",
				},
			},
			{Role: "assistant", Content: "Here are the files."},
		},
	}
	body := marshalRequest(t, buildResponsesRequest(req, false))
	if strings.Contains(body, `"name":"terminal:exec"`) {
		t.Fatalf("unsanitized tool name leaked to wire: %s", body)
	}
	if !strings.Contains(body, `"name":"terminal_exec"`) {
		t.Fatalf("expected sanitized name terminal_exec, got: %s", body)
	}
	// call_id pairing must be preserved so the function_call_output still
	// matches the function_call after name sanitization.
	if !strings.Contains(body, `"call_id":"call_1"`) {
		t.Fatalf("call_id missing on function_call: %s", body)
	}
	if !strings.Contains(body, `"type":"function_call_output"`) {
		t.Fatalf("function_call_output missing: %s", body)
	}
}
