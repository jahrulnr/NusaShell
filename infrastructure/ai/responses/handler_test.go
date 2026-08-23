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

// TestResponsesMessageItemsIncludeTypeMessage: every user/assistant message
// input item MUST include type:"message" — the Responses API validates input
// items as a union and rejects items without a type field (strict providers
// like Stealth return HTTP 400 invalid_prompt). This is a regression test for
// a latent bug where message items were sent without type:"message".
func TestResponsesMessageItemsIncludeTypeMessage(t *testing.T) {
	req := application.ChatRequest{
		Model: "test-model",
		Messages: []application.ChatMessage{
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi there"},
		},
	}
	body := marshalRequest(t, buildResponsesRequest(req, false))
	if !containsAll(body, `"type":"message"`, `"role":"user"`, `"role":"assistant"`) {
		t.Fatalf("message items must include type:message, got %s", body)
	}
}

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

// TestResponsesImageBlockIncludesDetailField proves that input_image blocks
// carry the `detail` field required by the OpenAI Responses API spec.
// ResponseInputImageParam marks detail as Required — omitting it causes
// HTTP 400 "Field required" from strict providers (observed when switching
// to a Responses-API-compatible model mid-conversation with image history).
func TestResponsesImageBlockIncludesDetailField(t *testing.T) {
	req := application.ChatRequest{
		Model: "test-model",
		Messages: []application.ChatMessage{{
			Role:    "user",
			Content: "What is this?",
			Attachments: []domain.Attachment{
				{Type: "image", Name: "cat.png", MediaType: "image/png", DataURL: "data:image/png;base64,iVBORw0KGgo="},
			},
		}},
	}
	body := marshalRequest(t, buildResponsesRequest(req, false))
	if !containsAll(body, `"detail":"auto"`, "input_image") {
		t.Fatalf("input_image block must include detail:auto, got %s", body)
	}
}

// TestResponsesToolResultImageBlockIncludesDetailField: same requirement
// applies to input_image blocks in tool result attachments (read_image,
// generate_image tool outputs).
func TestResponsesToolResultImageBlockIncludesDetailField(t *testing.T) {
	req := application.ChatRequest{
		Model: "test-model",
		Messages: []application.ChatMessage{
			{Role: "assistant", ToolCalls: []domain.ToolCall{{ID: "c1", Name: "read_image", Args: `{}`}}},
			{Role: "tool", ToolResult: &application.ToolResult{
				ToolCallID: "c1", Name: "read_image", Content: "Image loaded.",
				Attachments: []domain.Attachment{
					{Type: "image", Name: "gen-call_1.png", MediaType: "image/png",
						DataURL: "data:image/png;base64,iVBORw0KGgo="},
				},
			}},
		},
	}
	body := marshalRequest(t, buildResponsesRequest(req, false))
	if !containsAll(body, `"detail":"auto"`, "input_image") {
		t.Fatalf("tool result input_image must include detail:auto, got %s", body)
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

// TestResponsesVideoAttachmentUsesVideoURL: video attachments must be
// encoded as video_url (not input_image) on the Responses API. Sending
// video as input_image causes providers to reject it with HTTP 400 because
// they attempt image decoding on a video payload.
func TestResponsesVideoAttachmentUsesVideoURL(t *testing.T) {
	req := application.ChatRequest{
		Model: "test-model",
		Messages: []application.ChatMessage{{
			Role:    "user",
			Content: "Describe this clip",
			Attachments: []domain.Attachment{
				{Type: "video", Name: "clip.mp4", MediaType: "video/mp4", DataURL: "data:video/mp4;base64,AAAA"},
			},
		}},
	}
	body := marshalRequest(t, buildResponsesRequest(req, false))
	if !containsAll(body, "video_url", "data:video/mp4;base64,AAAA") {
		t.Fatalf("video attachment must use video_url, got %s", body)
	}
	if strings.Contains(body, "input_image") {
		t.Fatalf("video attachment must NOT use input_image, got %s", body)
	}
}

// TestResponsesToolResultVideoUsesVideoURL: video attachments in tool
// results (e.g. read_video) must also use video_url, not input_image.
func TestResponsesToolResultVideoUsesVideoURL(t *testing.T) {
	req := application.ChatRequest{
		Model: "test-model",
		Messages: []application.ChatMessage{
			{Role: "assistant", ToolCalls: []domain.ToolCall{{ID: "c1", Name: "read_video", Args: `{}`}}},
			{Role: "tool", ToolResult: &application.ToolResult{
				ToolCallID: "c1", Name: "read_video", Content: "Video loaded.",
				Attachments: []domain.Attachment{
					{Type: "video", Name: "clip.mp4", MediaType: "video/mp4", DataURL: "data:video/mp4;base64,AAAA"},
				},
			}},
		},
	}
	body := marshalRequest(t, buildResponsesRequest(req, false))
	if !containsAll(body, "video_url", "data:video/mp4;base64,AAAA") {
		t.Fatalf("tool result video must use video_url, got %s", body)
	}
	if strings.Contains(body, "input_image") {
		t.Fatalf("tool result video must NOT use input_image, got %s", body)
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

// TestResponsesReasoningReplayInjectsReasoningItem proves that when
// ReasoningReplay is true, a {type:"reasoning"} item with a summary is
// emitted before each assistant message item, so thinking-mode upstreams
// (GLM, DeepSeek V4, Kimi, ox-alpha) can reconstruct the thinking state
// across turns. When false, no reasoning item is emitted.
func TestResponsesReasoningReplayInjectsReasoningItem(t *testing.T) {
	msgs := []application.ChatMessage{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there", Reasoning: "I should greet the user."},
		{Role: "user", Content: "What is 2+2?"},
	}

	// Without replay: no reasoning items
	req := application.ChatRequest{Model: "test-model", Messages: msgs}
	body := marshalRequest(t, buildResponsesRequest(req, false))
	if strings.Contains(body, `"type":"reasoning"`) {
		t.Errorf("no replay: reasoning item should not be present, got %s", body)
	}

	// With replay: reasoning item emitted before assistant message
	req = application.ChatRequest{Model: "test-model", Messages: msgs, ReasoningReplay: true}
	body = marshalRequest(t, buildResponsesRequest(req, false))
	if !strings.Contains(body, `"type":"reasoning"`) {
		t.Errorf("with replay: reasoning item missing, got %s", body)
	}
	if !strings.Contains(body, `"summary_text"`) {
		t.Errorf("with replay: summary_text missing, got %s", body)
	}
	if !strings.Contains(body, "I should greet the user.") {
		t.Errorf("with replay: persisted reasoning text missing, got %s", body)
	}
}

// TestResponsesReasoningReplayPlaceholder proves that when ReasoningReplay
// is true but the assistant message has no persisted reasoning, a
// non-empty placeholder is injected as the summary text.
func TestResponsesReasoningReplayPlaceholder(t *testing.T) {
	msgs := []application.ChatMessage{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there", Reasoning: ""},
	}
	req := application.ChatRequest{Model: "test-model", Messages: msgs, ReasoningReplay: true}
	body := marshalRequest(t, buildResponsesRequest(req, false))
	if !strings.Contains(body, domain.ReasoningPlaceholder) {
		t.Errorf("placeholder missing, got %s", body)
	}
}
