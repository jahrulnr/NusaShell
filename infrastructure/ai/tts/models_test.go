package tts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSpeechModelListerUsesModalitiesFilter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.URL.Query().Get("output_modalities") != "speech" {
			t.Fatalf("output_modalities = %q", r.URL.Query().Get("output_modalities"))
		}
		if r.Header.Get("Authorization") != "Bearer or-key" {
			t.Fatalf("auth = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "deepgram/flux-tts:free"},
				{"id": "fish-audio/s2.1-pro-free:free"},
			},
		})
	}))
	defer server.Close()

	lister := NewModelLister(server.URL+"/v1", server.Client())
	ids, err := lister.ListSpeechModels(context.Background(), "or-key")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "deepgram/flux-tts:free" {
		t.Fatalf("ids = %v", ids)
	}
}

func TestSpeechModelListerMissingEndpointIsEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()
	lister := NewModelLister(server.URL+"/v1", server.Client())
	ids, err := lister.ListSpeechModels(context.Background(), "sk")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("ids = %v", ids)
	}
}
