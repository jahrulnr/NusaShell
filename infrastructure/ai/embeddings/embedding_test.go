package embeddings

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestNewEmbedderNormalizesBaseURLAndNamesModel(t *testing.T) {
	e := NewEmbedder("https://example.test/v1///", "key", "text-embedding-3-small", 0)

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

	e := NewEmbedder(server.URL+"/v1", "test-key", "test-embed", 0)
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

func TestEmbedBatchAddsOpenRouterAttributionHeaders(t *testing.T) {
	var got http.Header
	e := NewEmbedder("https://openrouter.ai/api/v1", "test-key", "liquid/lfm-2.5-embedding-350m:free", 0)
	e.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		got = r.Header.Clone()
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"data":[{"embedding":[0.1]}]}`)),
			Request:    r,
		}, nil
	})}

	if _, err := e.Embed(context.Background(), "identify this app"); err != nil {
		t.Fatalf("Embed failed: %v", err)
	}
	if got.Get("User-Agent") != "NusaShell" {
		t.Fatalf("User-Agent = %q, want NusaShell", got.Get("User-Agent"))
	}
	if got.Get("HTTP-Referer") != "https://github.com/jahrulnr/NusaShell" {
		t.Fatalf("HTTP-Referer = %q, want NusaShell repository URL", got.Get("HTTP-Referer"))
	}
	if got.Get("X-OpenRouter-Title") != "NusaShell" {
		t.Fatalf("X-OpenRouter-Title = %q, want NusaShell", got.Get("X-OpenRouter-Title"))
	}
	if got.Get("X-OpenRouter-Categories") == "" {
		t.Fatal("missing X-OpenRouter-Categories")
	}
}

func TestEmbedBatchDoesNotSendOpenRouterAttributionToOtherHosts(t *testing.T) {
	var got http.Header
	e := NewEmbedder("https://api.example.test/v1", "test-key", "test-embed", 0)
	e.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		got = r.Header.Clone()
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"data":[{"embedding":[0.1]}]}`)),
			Request:    r,
		}, nil
	})}

	if _, err := e.Embed(context.Background(), "do not impersonate a router"); err != nil {
		t.Fatalf("Embed failed: %v", err)
	}
	for _, name := range []string{"HTTP-Referer", "X-OpenRouter-Title", "X-OpenRouter-Categories"} {
		if value := got.Get(name); value != "" {
			t.Errorf("%s = %q, want absent for non-OpenRouter host", name, value)
		}
	}
}

func TestEmbedReturnsFirstVector(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"embedding":[1,2,3]}]}`))
	}))
	defer server.Close()

	e := NewEmbedder(server.URL, "", "model", 0)
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

	e := NewEmbedder(server.URL, "", "model", 0)
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

func TestEmbedBatchTruncatesOversizedInputsToTokenCap(t *testing.T) {
	var received []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		received = payload.Input
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.1]},{"embedding":[0.2]}]}`))
	}))
	defer server.Close()

	// ~6000 runes ≈ 1500 tokens at 4 chars/token — far over any small-model cap.
	long := strings.Repeat("lorem ipsum dolor sit amet ", 500) // 25 runes per repeat
	short := "keep me as-is"
	e := NewEmbedder(server.URL, "", "test-embed", 512)
	e.Client = server.Client()
	if _, err := e.EmbedBatch(context.Background(), []string{long, short}); err != nil {
		t.Fatalf("EmbedBatch failed: %v", err)
	}

	if len(received) != 2 {
		t.Fatalf("received %d inputs, want 2", len(received))
	}
	if got := len([]rune(received[0])); got > 512*embeddingCharsPerToken {
		t.Fatalf("oversized input sent as %d runes, want <= %d", got, 512*embeddingCharsPerToken)
	}
	if !strings.Contains(received[0], "[truncated]") {
		t.Fatalf("truncated input %q missing omission marker", received[0])
	}
	if received[1] != short {
		t.Fatalf("short input = %q, want unchanged %q", received[1], short)
	}
}

func TestEmbedBatchTruncatesWithDefaultTokenCap(t *testing.T) {
	var received []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		received = payload.Input
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.1]}]}`))
	}))
	defer server.Close()

	long := strings.Repeat("abcdefghij ", 3000)       // ~33000 runes
	e := NewEmbedder(server.URL, "", "test-embed", 0) // no cap passed → EmbeddingMaxTokens
	e.Client = server.Client()
	if _, err := e.EmbedBatch(context.Background(), []string{long}); err != nil {
		t.Fatalf("EmbedBatch failed: %v", err)
	}
	if got := len([]rune(received[0])); got > EmbeddingMaxTokens*embeddingCharsPerToken {
		t.Fatalf("input sent as %d runes, want <= %d", got, EmbeddingMaxTokens*embeddingCharsPerToken)
	}
}

func TestEmbedBatchRetriesProviderTokenOverflowWithSmallerInputs(t *testing.T) {
	var requests int
	var receivedTokens []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var payload struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		// Reproduce the reported provider behavior exactly: the local 3-char
		// estimate fills 512 tokens, then the model tokenizer adds 3 tokens.
		tokens := (len([]rune(payload.Input[0]))+2)/3 + 3
		receivedTokens = append(receivedTokens, tokens)
		if tokens > 512 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"HTTP 400: {\"message\":\"Embedding input has 515 tokens, exceeding the model maximum of 512.\"}"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.1,0.2]}]}`))
	}))
	defer server.Close()

	e := NewEmbedder(server.URL, "", "test-embed", 512)
	e.Client = server.Client()
	got, err := e.EmbedBatch(context.Background(), []string{strings.Repeat("abc", 512)})
	if err != nil {
		t.Fatalf("EmbedBatch failed after provider reported its exact token count: %v", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want one rejected request plus one bounded retry", requests)
	}
	if len(got) != 1 || len(got[0]) != 2 {
		t.Fatalf("EmbedBatch = %#v, want one 2-dimensional vector", got)
	}
	if receivedTokens[0] != 515 || receivedTokens[1] > 512 {
		t.Fatalf("provider token counts = %v, want first 515 and retry <= 512", receivedTokens)
	}
}

func TestEmbedBatchBoundsProviderTokenOverflowRetries(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"Embedding input has 515 tokens, exceeding the model maximum of 512."}}`))
	}))
	defer server.Close()

	e := NewEmbedder(server.URL, "", "test-embed", 512)
	e.Client = server.Client()
	_, err := e.EmbedBatch(context.Background(), []string{strings.Repeat("abc", 512)})
	if err == nil || !strings.Contains(err.Error(), "515 tokens") {
		t.Fatalf("EmbedBatch error = %v, want final provider overflow", err)
	}
	if requests != 1+embeddingOverflowMaxRetries {
		t.Fatalf("requests = %d, want initial request plus %d bounded retries", requests, embeddingOverflowMaxRetries)
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

			e := NewEmbedder(server.URL, "", "model", 0)
			e.Client = server.Client()
			_, err := e.EmbedBatch(context.Background(), []string{"hello"})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("EmbedBatch error = %v, want substring %q", err, tt.want)
			}
		})
	}
}
