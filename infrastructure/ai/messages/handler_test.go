package messages

import (
	"encoding/json"
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
