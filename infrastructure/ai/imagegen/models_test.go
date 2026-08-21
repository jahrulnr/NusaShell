package imagegen

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestModelListerFetchesImagesModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/models" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer or-key" {
			t.Fatalf("auth = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "openai/gpt-image-2"},
				{"id": "black-forest-labs/flux-1.1-pro"},
			},
		})
	}))
	defer server.Close()

	lister := NewModelLister(server.URL+"/v1", server.Client())
	ids, err := lister.ListImageModels(context.Background(), "or-key")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "openai/gpt-image-2" {
		t.Fatalf("ids = %v", ids)
	}
}

func TestModelListerMissingEndpointIsEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()
	lister := NewModelLister(server.URL+"/v1", server.Client())
	ids, err := lister.ListImageModels(context.Background(), "sk")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("ids = %v", ids)
	}
}
