package ai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"nusashell/application"
	"nusashell/domain"
)

func TestCodexSearchFactoryNormalizesTextResults(t *testing.T) {
	var gotRequest map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":"summary","results":[{"type":"image_result","url":"https://example.com/image"},{"type":"text_result","url":"https://example.com","title":"Example","snippet":"snippet"}]}`))
	}))
	defer server.Close()

	factory := NewCodexSearchFactory(nil)
	backend, err := factory(t.Context(), &domain.Provider{ID: "codex", Kind: domain.ProviderCodex, BaseURL: server.URL}, "access-token")
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	response, err := backend.Search(t.Context(), application.CodexSearchRequest{
		ConversationID: "conversation-1",
		Model:          "gpt-5-codex",
		Query:          "example query",
		Limit:          5,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotRequest["id"] != "conversation-1" || gotRequest["model"] != "gpt-5-codex" {
		t.Fatalf("request identity = %#v", gotRequest)
	}
	if response.Summary != "summary" || len(response.Results) != 1 {
		t.Fatalf("response = %+v", response)
	}
	if response.Results[0].Title != "Example" || response.Results[0].URL != "https://example.com" {
		t.Fatalf("result = %+v", response.Results[0])
	}
}
