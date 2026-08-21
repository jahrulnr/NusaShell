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
