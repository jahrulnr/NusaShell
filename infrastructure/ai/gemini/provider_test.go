package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nusashell/infrastructure/ai/core"
)

func TestNewRequiresAPIKey(t *testing.T) {
	if _, err := New(Config{}); err == nil || !strings.Contains(err.Error(), "api key is required") {
		t.Fatalf("err = %v", err)
	}
	// The gateway variants may skip the key explicitly.
	if _, err := New(Config{APIKeyOptional: true}); err != nil {
		t.Fatalf("APIKeyOptional must build: %v", err)
	}
}

func TestChatEndToEnd(t *testing.T) {
	var gotRequest *http.Request
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRequest = r
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"hello world"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":2,"totalTokenCount":6}}`))
	}))
	defer server.Close()

	provider, err := New(Config{APIKey: "secret-key", BaseURL: server.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := provider.Chat(context.Background(), &core.Request{
		Model:    "gemini-2.5-flash",
		Messages: []core.Message{core.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if gotRequest == nil {
		t.Fatal("no request observed")
	}
	if gotRequest.Header.Get("x-goog-api-key") != "secret-key" {
		t.Fatalf("x-goog-api-key = %q", gotRequest.Header.Get("x-goog-api-key"))
	}
	if gotRequest.URL.Path != "/v1beta/models/gemini-2.5-flash:generateContent" {
		t.Fatalf("path = %q", gotRequest.URL.Path)
	}
	if gotBody["contents"] == nil || gotBody["systemInstruction"] != nil {
		t.Fatalf("body = %+v", gotBody)
	}
	if resp.Text() != "hello world" || resp.FinishReason != core.FinishReasonStop {
		t.Fatalf("response = %+v", resp)
	}
	if resp.Usage.InputTokens != 4 || resp.Usage.OutputTokens != 2 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func TestChatHTTPErrorMapping(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":429,"message":"exhausted","status":"RESOURCE_EXHAUSTED"}}`))
	}))
	defer server.Close()
	provider, err := New(Config{APIKey: "k", BaseURL: server.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = provider.Chat(context.Background(), &core.Request{Model: "gemini-2.5-flash", Messages: []core.Message{core.UserText("hi")}})
	if err == nil {
		t.Fatal("expected error")
	}
	var le *core.LiteLLMError
	if !errors.As(err, &le) || le.StatusCode != http.StatusTooManyRequests || le.Type != core.ErrorTypeRateLimit {
		t.Fatalf("error = %v", err)
	}
}

func TestListModelsFiltersAndPaginates(t *testing.T) {
	page := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("x-goog-api-key") != "k" {
			t.Errorf("x-goog-api-key = %q", r.Header.Get("x-goog-api-key"))
		}
		if !strings.Contains(r.URL.RawQuery, "pageSize=200") {
			t.Errorf("query = %q", r.URL.RawQuery)
		}
		page++
		w.Header().Set("Content-Type", "application/json")
		switch page {
		case 1:
			_, _ = w.Write([]byte(`{"models":[
				{"name":"models/gemini-2.5-flash","displayName":"Gemini 2.5 Flash","description":"fast","inputTokenLimit":1048576,"outputTokenLimit":65536,"supportedGenerationMethods":["generateContent","countTokens"]},
				{"name":"models/gemini-embedding-001","displayName":"Embedding","supportedGenerationMethods":["embedContent"]},
				{"name":"models/gemini-2.5-pro","displayName":"Gemini 2.5 Pro","inputTokenLimit":1048576,"outputTokenLimit":65536,"supportedGenerationMethods":["generateContent"]}
			],"nextPageToken":"tok2"}`))
		case 2:
			_, _ = w.Write([]byte(`{"models":[
				{"name":"models/gemini-3-flash","displayName":"Gemini 3 Flash","inputTokenLimit":1048576,"outputTokenLimit":131072,"supportedGenerationMethods":["generateContent"]}
			]}`))
		default:
			t.Errorf("unexpected page %d", page)
		}
	}))
	defer server.Close()
	provider, err := New(Config{APIKey: "k", BaseURL: server.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	models, err := provider.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 3 {
		t.Fatalf("models = %+v, want 3 (embedding filtered out)", models)
	}
	if models[0].ID != "gemini-2.5-flash" || models[0].InputTokenLimit != 1048576 {
		t.Fatalf("models[0] = %+v", models[0])
	}
	if models[0].Name != "Gemini 2.5 Flash" {
		t.Fatalf("display name = %q", models[0].Name)
	}
	if models[2].ID != "gemini-3-flash" {
		t.Fatalf("models[2] = %+v", models[2])
	}
	if page != 2 {
		t.Fatalf("pages = %d, want 2", page)
	}
}

func TestListModelsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":401,"message":"API key not valid. Please pass a valid API key.","status":"UNAUTHENTICATED"}}`))
	}))
	defer server.Close()
	provider, err := New(Config{APIKey: "bad", BaseURL: server.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = provider.ListModels(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	var le *core.LiteLLMError
	if !errors.As(err, &le) || le.Type != core.ErrorTypeAuth {
		t.Fatalf("error = %v", err)
	}
}

func TestListModelsFiltersImageAndSpecializedModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[
			{"name":"models/gemini-3-flash","displayName":"Gemini 3 Flash","inputTokenLimit":1048576,"outputTokenLimit":65536,"supportedGenerationMethods":["generateContent"]},
			{"name":"models/gemini-3.1-flash-lite-image","displayName":"Gemini 3.1 Flash Lite Image","inputTokenLimit":1048576,"outputTokenLimit":65536,"supportedGenerationMethods":["generateContent"]},
			{"name":"models/gemini-embedding-001","displayName":"Embedding","supportedGenerationMethods":["embedContent"]},
			{"name":"models/gemini-2.5-flash-tts","displayName":"TTS","supportedGenerationMethods":["generateContent"]}
		]}`))
	}))
	defer server.Close()
	provider, err := New(Config{APIKey: "k", BaseURL: server.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	models, err := provider.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("models = %+v, want only gemini-3-flash (image and tts filtered by ID, embedding by method)", models)
	}
	if models[0].ID != "gemini-3-flash" {
		t.Fatalf("models[0] = %+v, want gemini-3-flash", models[0])
	}
}

func TestIsChatModelID(t *testing.T) {
	cases := []struct {
		id   string
		want bool
	}{
		{"gemini-3-flash", true},
		{"gemini-2.5-pro", true},
		{"gemini-3.1-flash-lite-image", false},
		{"gemini-2.5-flash-tts", false},
		{"gemini-embedding-001", false},
		{"models/gemini-3-flash", true},
		{"models/gemini-3.1-flash-lite-image", false},
	}
	for _, tc := range cases {
		if got := isChatModelID(tc.id); got != tc.want {
			t.Fatalf("isChatModelID(%q) = %v, want %v", tc.id, got, tc.want)
		}
	}
}
