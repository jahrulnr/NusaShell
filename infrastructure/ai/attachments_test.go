package ai

import (
	"encoding/json"
	"strings"
	"testing"

	"nusashell/application"
	"nusashell/domain"
)

func TestAdaptersEncodeAttachmentsOnlyWhenPresent(t *testing.T) {
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

	chatBody := marshalAttachmentRequest(t, buildRequest(req, false))
	if !containsAll(chatBody, "Attached text file: notes.txt\\n\\nLocal notes", "image_url", "data:image/png;base64,iVBORw0KGgo=", "[Attached document: brief.pdf (application/pdf)]") {
		t.Fatalf("chat attachment mapping = %s", chatBody)
	}
	if strings.Contains(chatBody, "file_data") {
		t.Fatalf("chat completions must use the Electron-compatible document marker: %s", chatBody)
	}

	responsesBody := marshalAttachmentRequest(t, buildResponsesRequest(req, false))
	if !containsAll(responsesBody, "input_image", "input_file", "brief.pdf", "Attached text file: notes.txt\\n\\nLocal notes") {
		t.Fatalf("responses attachment mapping = %s", responsesBody)
	}

	anthropicBody := marshalAttachmentRequest(t, buildAnthropicRequest(req, false))
	if !containsAll(anthropicBody, `"type":"image"`, `"type":"document"`, "brief.pdf", "Attached text file: notes.txt\\n\\nLocal notes") {
		t.Fatalf("anthropic attachment mapping = %s", anthropicBody)
	}
}

func TestTextOnlyAdapterRequestsKeepScalarContent(t *testing.T) {
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

func containsAll(value string, want ...string) bool {
	for _, item := range want {
		if !strings.Contains(value, item) {
			return false
		}
	}
	return true
}

func marshalAttachmentRequest(t *testing.T, value any) string {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
