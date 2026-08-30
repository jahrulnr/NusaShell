package embeddings

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewEmbedderNormalizesBaseURLAndNamesModel(t *testing.T) {
	e := NewEmbedder("https://example.test/v1///", "key", "text-embedding-3-small")

	if e.BaseURL != "https://example.test/v1" {
		t.Fatalf("BaseURL = %q, want trailing slashes removed", e.BaseURL)
	}
	if e.Name() != "openai-compat/text-embedding-3-small" {
		t.Fatalf("Name() = %q, want openai-compat/text-embedding-3-small", e.Name())
	}
	if e.Client == nil {
		t.Fatal("NewEmbedder returned nil HTTP client")
	}
}

func TestEmbedBatchPostsRequestAndCachesDimension(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("path = %s, want /v1/embeddings", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want Bearer test-key", got)
		}
		var payload struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if payload.Model != "test-embed" {
			t.Errorf("model = %q, want test-embed", payload.Model)
		}
		if strings.Join(payload.Input, "|") != "first|second" {
			t.Errorf("input = %#v, want [first second]", payload.Input)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.1,0.2]},{"embedding":[0.3,0.4]}]}`))
	}))
	defer server.Close()

	e := NewEmbedder(server.URL+"/v1", "test-key", "test-embed")
	e.Client = server.Client()
	got, err := e.EmbedBatch(context.Background(), []string{"first", "second"})
	if err != nil {
		t.Fatalf("EmbedBatch failed: %v", err)
	}
	if len(got) != 2 || len(got[0]) != 2 || got[0][1] != 0.2 {
		t.Fatalf("EmbedBatch = %#v, want two 2-dimensional vectors", got)
	}
	if got := e.Dim(); got != 2 {
		t.Fatalf("Dim() = %d, want 2", got)
	}
	if requests != 1 {
		t.Fatalf("Dim() made an extra request after EmbedBatch: requests = %d, want 1", requests)
	}
}

func TestEmbedReturnsFirstVector(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"embedding":[1,2,3]}]}`))
	}))
	defer server.Close()

	e := NewEmbedder(server.URL, "", "model")
	e.Client = server.Client()
	got, err := e.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}
	if len(got) != 3 || got[0] != 1 || got[2] != 3 {
		t.Fatalf("Embed = %#v, want [1 2 3]", got)
	}
}

func TestEmbedBatchHandlesEmptyInputAndEmptyResponse(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()

	e := NewEmbedder(server.URL, "", "model")
	e.Client = server.Client()
	got, err := e.EmbedBatch(context.Background(), nil)
	if err != nil || got != nil {
		t.Fatalf("EmbedBatch(nil) = %#v, %v; want nil, nil", got, err)
	}
	if requests != 0 {
		t.Fatalf("EmbedBatch(nil) made %d requests, want 0", requests)
	}
	if _, err := e.Embed(context.Background(), "empty"); err == nil || !strings.Contains(err.Error(), "empty response") {
		t.Fatalf("Embed(empty response) error = %v, want empty response error", err)
	}
}

func TestEmbedBatchReportsHTTPAndDecodeErrors(t *testing.T) {
	tests := []struct {
		name string
		body string
		code int
		want string
	}{
		{name: "HTTP status", body: "quota exceeded", code: http.StatusTooManyRequests, want: "embeddings: 429 Too Many Requests: quota exceeded"},
		{name: "invalid JSON", body: "not-json", code: http.StatusOK, want: "embeddings: decode:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.code)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			e := NewEmbedder(server.URL, "", "model")
			e.Client = server.Client()
			_, err := e.EmbedBatch(context.Background(), []string{"hello"})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("EmbedBatch error = %v, want substring %q", err, tt.want)
			}
		})
	}
}
