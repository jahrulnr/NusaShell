package embeddings

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOllamaTagLister_ListEmbeddingModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[
			{"name":"nomic-embed-text:latest","capabilities":["embedding"]},
			{"name":"gemma4:e2b","capabilities":["completion","tools","thinking"]},
			{"name":"qwen3-embedding:4b","capabilities":["embedding"]},
			{"name":"no-caps-model"}
		]}`))
	}))
	defer srv.Close()

	l := NewOllamaTagLister(srv.URL, srv.Client())
	ids, err := l.ListEmbeddingModels(context.Background(), "")
	if err != nil {
		t.Fatalf("ListEmbeddingModels returned error: %v", err)
	}
	want := []string{"nomic-embed-text:latest", "qwen3-embedding:4b"}
	if len(ids) != len(want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Errorf("ids[%d] = %q, want %q", i, ids[i], want[i])
		}
	}
}

func TestOllamaTagLister_MissingEndpointReturnsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	l := NewOllamaTagLister(srv.URL+"/v1", srv.Client()) // /v1 suffix must be normalized away
	ids, err := l.ListEmbeddingModels(context.Background(), "")
	if err != nil {
		t.Fatalf("expected nil error for missing endpoint, got %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("ids = %v, want empty", ids)
	}
}
