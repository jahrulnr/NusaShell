package embeddings

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestModelLister_FetchEmbeddingModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings/models" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("missing auth header: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "openai/text-embedding-3-small"},
				{"id": "google/gemini-embedding-2"},
			},
		})
	}))
	defer server.Close()

	lister := NewModelLister(server.URL+"/v1", server.Client())
	ids, err := lister.ListEmbeddingModels(context.Background(), "test-key")
	if err != nil {
		t.Fatalf("ListEmbeddingModels failed: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 ids, got %d", len(ids))
	}
	if ids[0] != "openai/text-embedding-3-small" {
		t.Errorf("ids[0] = %q, want openai/text-embedding-3-small", ids[0])
	}
}

func TestModelLister_EmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
	}))
	defer server.Close()

	lister := NewModelLister(server.URL+"/v1", server.Client())
	ids, err := lister.ListEmbeddingModels(context.Background(), "")
	if err != nil {
		t.Fatalf("ListEmbeddingModels failed: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("expected 0 ids, got %d", len(ids))
	}
}

func TestModelLister_404ReturnsEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	lister := NewModelLister(server.URL+"/v1", server.Client())
	ids, err := lister.ListEmbeddingModels(context.Background(), "")
	if err != nil {
		t.Fatalf("ListEmbeddingModels should not error on 404: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("expected 0 ids on 404, got %d", len(ids))
	}
}

func TestModelLister_NilClientUsesDefault(t *testing.T) {
	lister := NewModelLister("http://localhost:11434", nil)
	if lister.Client == nil {
		t.Fatal("expected non-nil client when nil is passed")
	}
}
