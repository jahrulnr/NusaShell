package chatcompletion

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

func TestChatCompletionAdapterEncodesAttachments(t *testing.T) {
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

	body := marshalRequest(t, buildRequest(req, false))
	if !containsAll(body, "Attached text file: notes.txt\\n\\nLocal notes", "image_url", "data:image/png;base64,iVBORw0KGgo=", "[Attached document: brief.pdf (application/pdf)]") {
		t.Fatalf("chat attachment mapping = %s", body)
	}
	if strings.Contains(body, "file_data") {
		t.Fatalf("chat completions must use the Electron-compatible document marker: %s", body)
	}
}

func TestOpenAIListModelsParsesOpenRouterFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{
					"id":             "openai/gpt-4o",
					"context_length": 128000,
					"max_tokens":     16384,
					"description":    "GPT-4o is a multimodal model.",
					"pricing": map[string]any{
						"prompt":     "0.000005",
						"completion": "0.000015",
					},
				},
				{
					"id":             "anthropic/claude-3.5-sonnet",
					"context_length": 200000,
					"max_tokens":     8192,
					"description":    "Claude 3.5 Sonnet.",
					"pricing": map[string]any{
						"prompt": "0.000003",
					},
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
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}

	m1 := models[0]
	if m1.ID != "openai/gpt-4o" {
		t.Fatalf("model 0 id = %q, want openai/gpt-4o", m1.ID)
	}
	if m1.Context != 128000 {
		t.Fatalf("model 0 context = %d, want 128000", m1.Context)
	}
	if m1.MaxOutput != 16384 {
		t.Fatalf("model 0 max_output = %d, want 16384", m1.MaxOutput)
	}
	if m1.Description != "GPT-4o is a multimodal model." {
		t.Fatalf("model 0 description = %q, want GPT-4o is a multimodal model.", m1.Description)
	}
	if m1.InputCost != 5 {
		t.Fatalf("model 0 input_cost = %v, want 5 (USD per 1M = 0.000005 * 1e6)", m1.InputCost)
	}

	m2 := models[1]
	if m2.Context != 200000 || m2.MaxOutput != 8192 {
		t.Fatalf("model 1: context=%d max_output=%d, want 200000/8192", m2.Context, m2.MaxOutput)
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
