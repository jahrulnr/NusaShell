package imagegen

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"nusashell/application"
	"nusashell/domain"
)

const png1x1B64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAAC0lEQVR42mNk+M8AAAMBAQDJ/pLvAAAAAElFTkSuQmCC"

func TestOpenAIGenerationsDecodesB64AndUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/generations" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("auth = %s", r.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["prompt"] != "a red boat" || body["model"] != "gpt-image-1" {
			t.Errorf("body = %+v", body)
		}
		if _, ok := body["size"]; ok {
			t.Errorf("auto size should be omitted, got %v", body["size"])
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":  []map[string]any{{"b64_json": png1x1B64}},
			"usage": map[string]any{"total_tokens": 4175, "input_tokens": 20, "output_tokens": 4155},
		})
	}))
	defer server.Close()

	client := &Client{Backend: backendOpenAI, BaseURL: server.URL + "/v1", APIKey: "sk-test", HTTP: server.Client()}
	result, err := client.Generate(context.Background(), application.ImageGenRequest{
		Model: "gpt-image-1", Prompt: "a red boat", Size: "auto", N: 1,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if result.Provider != backendOpenAI || result.UsageTokens != 4175 {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Images) != 1 || len(result.Images[0].Bytes) < 8 {
		t.Fatalf("images = %d bytes=%d", len(result.Images), len(result.Images[0].Bytes))
	}
}

func TestOpenRouterSendsInputReferencesAndCost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["size"] != "1024x1024" {
			t.Errorf("size = %v", body["size"])
		}
		refs, _ := body["input_references"].([]any)
		if len(refs) != 1 {
			t.Fatalf("input_references = %v", body["input_references"])
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":  []map[string]any{{"b64_json": png1x1B64, "media_type": "image/png"}},
			"usage": map[string]any{"cost": 0.04, "total_tokens": 100},
		})
	}))
	defer server.Close()

	client := &Client{Backend: backendOpenRouter, BaseURL: server.URL + "/v1", APIKey: "or-key", HTTP: server.Client()}
	result, err := client.Generate(context.Background(), application.ImageGenRequest{
		Model: "openai/gpt-image-2", Prompt: "watercolor", Size: "1024x1024", N: 1,
		References: []application.ImageReference{{MediaType: "image/png", Data: []byte{1, 2, 3}}},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if result.Provider != backendOpenRouter || result.CostUSD != 0.04 {
		t.Fatalf("result = %+v", result)
	}
	if result.Images[0].MediaType != "image/png" {
		t.Fatalf("media_type = %s", result.Images[0].MediaType)
	}
}

func TestOpenAIEditsUsesMultipart(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/edits" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			t.Errorf("content-type = %s", r.Header.Get("Content-Type"))
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		if r.FormValue("prompt") != "make it night" {
			t.Errorf("prompt = %s", r.FormValue("prompt"))
		}
		file, _, err := r.FormFile("image")
		if err != nil {
			t.Fatalf("image part: %v", err)
		}
		defer file.Close()
		got, _ := io.ReadAll(file)
		if string(got) != "PNGDATA" {
			t.Errorf("image bytes = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"b64_json": png1x1B64}},
		})
	}))
	defer server.Close()

	client := &Client{Backend: backendOpenAI, BaseURL: server.URL + "/v1", APIKey: "sk", HTTP: server.Client()}
	_, err := client.Generate(context.Background(), application.ImageGenRequest{
		Model: "gpt-image-1", Prompt: "make it night", N: 1,
		References: []application.ImageReference{{MediaType: "image/png", Data: []byte("PNGDATA")}},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
}

func TestGenerateSurfacesHTTPStatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"overloaded"}}`, http.StatusBadGateway)
	}))
	defer server.Close()
	client := &Client{Backend: backendOpenAI, BaseURL: server.URL + "/v1", APIKey: "sk", HTTP: server.Client()}
	_, err := client.Generate(context.Background(), application.ImageGenRequest{Model: "gpt-image-1", Prompt: "x", N: 1})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "502") {
		// DoJSON keeps the HTTP status in the returned error.
		t.Logf("err = %v", err)
	}
}

func TestGenerateHonorsCancel(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		time.Sleep(2 * time.Second)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()
	client := &Client{Backend: backendOpenAI, BaseURL: server.URL + "/v1", HTTP: server.Client()}
	_, err := client.Generate(ctx, application.ImageGenRequest{Model: "m", Prompt: "p", N: 1})
	if err == nil {
		t.Fatal("expected cancel error")
	}
}

func TestFactoryRejectsUnsupportedKind(t *testing.T) {
	factory := NewFactory()
	for _, kind := range []domain.ProviderKind{domain.ProviderMessages} {
		_, err := factory(context.Background(), &domain.Provider{Kind: kind, BaseURL: "https://example.com"}, "key")
		if err == nil || !strings.Contains(err.Error(), "no image generation API") {
			t.Fatalf("kind %s err = %v", kind, err)
		}
	}
}

func TestFactoryRoutesOpenRouterByHost(t *testing.T) {
	factory := NewFactory()
	gen, err := factory(context.Background(), &domain.Provider{Kind: domain.ProviderChat, BaseURL: "https://openrouter.ai/api/v1"}, "key")
	if err != nil {
		t.Fatal(err)
	}
	client, ok := gen.(*Client)
	if !ok || client.Backend != backendOpenRouter {
		t.Fatalf("backend = %#v", gen)
	}
}

func TestOpenAINeverSendsResponseFormat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if _, ok := body["response_format"]; ok {
			t.Errorf("response_format must never be sent, body = %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"b64_json": png1x1B64}},
		})
	}))
	defer server.Close()
	client := &Client{Backend: backendOpenAI, BaseURL: server.URL + "/v1", HTTP: server.Client()}
	if _, err := client.Generate(context.Background(), application.ImageGenRequest{
		Model: "hidream", Prompt: "a boat", N: 1,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestOpenAIFallsBackToURLDownload(t *testing.T) {
	// image server: returns raw PNG bytes with image/png content-type
	imgBytes := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}
	imgServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(imgBytes)
	}))
	defer imgServer.Close()

	// API server: returns a URL pointing to the image server
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"url": imgServer.URL + "/img.png"}},
		})
	}))
	defer apiServer.Close()

	client := &Client{Backend: backendOpenAI, BaseURL: apiServer.URL + "/v1", HTTP: apiServer.Client()}
	res, err := client.Generate(context.Background(), application.ImageGenRequest{
		Model: "hidream", Prompt: "a boat", N: 1,
	})
	if err != nil {
		t.Fatalf("Generate error: %v", err)
	}
	if len(res.Images) != 1 {
		t.Fatalf("images len = %d", len(res.Images))
	}
	if string(res.Images[0].Bytes) != string(imgBytes) {
		t.Errorf("downloaded bytes mismatch: got %v", res.Images[0].Bytes)
	}
	if res.Images[0].MediaType != "image/png" {
		t.Errorf("media type = %s, want image/png", res.Images[0].MediaType)
	}
}

func TestOpenAIRetriesAreCallerOwned(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "try again", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client := &Client{Backend: backendOpenAI, BaseURL: server.URL + "/v1", HTTP: server.Client()}
	_, err := client.Generate(context.Background(), application.ImageGenRequest{Model: "m", Prompt: "p", N: 1})
	if err == nil {
		t.Fatal("expected 503")
	}
	if hits.Load() != 1 {
		t.Fatalf("hits = %d, client must not retry internally", hits.Load())
	}
}

func TestCodexGenerateSendsDTOBodyAndHeaders(t *testing.T) {
	var gotPath, gotAuth, gotOrigin, gotTurn, gotAccount, gotUA string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotOrigin = r.Header.Get("originator")
		gotTurn = r.Header.Get("x-codex-image-turn-id")
		gotAccount = r.Header.Get("ChatGPT-Account-ID")
		gotUA = r.Header.Get("User-Agent")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"created": 1778832973,
			"data":    []map[string]any{{"b64_json": png1x1B64}},
			"usage":   map[string]any{"total_tokens": 2846},
		})
	}))
	defer server.Close()

	client := &Client{Backend: BackendCodex, BaseURL: server.URL + "/backend-api/codex", APIKey: "tok-1", AccountID: "acc-1", HTTP: server.Client()}
	res, err := client.Generate(context.Background(), application.ImageGenRequest{
		Model: "gpt-image-2", Prompt: "a red boat", N: 3, TurnID: "turn-1",
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if gotPath != "/backend-api/codex/images/generations" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer tok-1" || gotOrigin != "codex_cli_rs" || gotTurn != "turn-1" || gotAccount != "acc-1" {
		t.Fatalf("headers auth=%q origin=%q turn=%q account=%q", gotAuth, gotOrigin, gotTurn, gotAccount)
	}
	if gotUA == "" {
		t.Fatal("user-agent must be set")
	}
	if gotBody["model"] != "gpt-image-2" || gotBody["prompt"] != "a red boat" {
		t.Fatalf("body = %+v", gotBody)
	}
	// DTO defaults: auto for background/quality/size; n always omitted (single image).
	if gotBody["background"] != "auto" || gotBody["quality"] != "auto" || gotBody["size"] != "auto" {
		t.Fatalf("auto defaults = %+v", gotBody)
	}
	if _, ok := gotBody["n"]; ok {
		t.Fatalf("n must be omitted for codex, got %+v", gotBody)
	}
	if res.Provider != BackendCodex || res.UsageTokens != 2846 {
		t.Fatalf("result = %+v", res)
	}
	if len(res.Images) != 1 {
		t.Fatalf("images = %d", len(res.Images))
	}
}

func TestCodexEditSendsImagesDataURL(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/backend-api/codex/images/edits" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"b64_json": png1x1B64}}})
	}))
	defer server.Close()

	client := &Client{Backend: BackendCodex, BaseURL: server.URL + "/backend-api/codex", HTTP: server.Client()}
	_, err := client.Generate(context.Background(), application.ImageGenRequest{
		Model: "gpt-image-2", Prompt: "add a hat",
		References: []application.ImageReference{{MediaType: "image/png", Data: []byte("PNGDATA")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	imgs, _ := gotBody["images"].([]any)
	if len(imgs) != 1 {
		t.Fatalf("images = %v", gotBody["images"])
	}
	ref, _ := imgs[0].(map[string]any)
	url, _ := ref["image_url"].(string)
	want := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("PNGDATA"))
	if url != want {
		t.Fatalf("image_url = %q, want %q", url, want)
	}
}

func TestCodexUsageLimit429IsHardFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"type": "usage_limit_reached", "message": "image limit reached", "resets_at": 1786150800, "plan_type": "plus",
			},
		})
	}))
	defer server.Close()
	client := &Client{Backend: BackendCodex, BaseURL: server.URL, HTTP: server.Client()}
	_, err := client.Generate(context.Background(), application.ImageGenRequest{Model: "gpt-image-2", Prompt: "x", N: 1})
	var perr *domain.ProviderError
	if !errors.As(err, &perr) {
		t.Fatalf("err = %v, want *ProviderError", err)
	}
	if perr.StatusCode != 429 || perr.RetryAfter != 0 {
		t.Fatalf("status = %d retry = %s, want 429 and no retry window", perr.StatusCode, perr.RetryAfter)
	}
	if !strings.Contains(perr.Error(), "usage limit reached") || !strings.Contains(perr.Error(), "resets at") {
		t.Fatalf("err = %v", perr)
	}
	if want := time.Unix(1786150800, 0); !perr.UsageLimitResetAt.Equal(want) {
		t.Fatalf("usage limit reset = %s, want %s", perr.UsageLimitResetAt, want)
	}
}

func TestCodexGeneric429NoRetryWindow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "slow down"}})
	}))
	defer server.Close()
	client := &Client{Backend: BackendCodex, BaseURL: server.URL, HTTP: server.Client()}
	_, err := client.Generate(context.Background(), application.ImageGenRequest{Model: "m", Prompt: "p", N: 1})
	var perr *domain.ProviderError
	if !errors.As(err, &perr) {
		t.Fatalf("err = %v, want *ProviderError", err)
	}
	if perr.StatusCode != 429 || perr.RetryAfter != 0 {
		t.Fatalf("status = %d retry = %s", perr.StatusCode, perr.RetryAfter)
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("err = %v", err)
	}
}

func TestCodex5xxIsRetriable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "overloaded", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client := &Client{Backend: BackendCodex, BaseURL: server.URL, HTTP: server.Client()}
	_, err := client.Generate(context.Background(), application.ImageGenRequest{Model: "m", Prompt: "p", N: 1})
	var perr *domain.ProviderError
	if !errors.As(err, &perr) {
		t.Fatalf("err = %v, want *ProviderError", err)
	}
	if !perr.Temporary || perr.StatusCode != 503 {
		t.Fatalf("perr = %+v, want retriable 503", perr)
	}
}

func TestCodexRejectsOversizeBase64(t *testing.T) {
	huge := strings.Repeat("A", maxCodexImageB64+1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"b64_json": huge}}})
	}))
	defer server.Close()
	client := &Client{Backend: BackendCodex, BaseURL: server.URL, HTTP: server.Client()}
	_, err := client.Generate(context.Background(), application.ImageGenRequest{Model: "m", Prompt: "p", N: 1})
	if err == nil || !strings.Contains(err.Error(), "32 MiB") {
		t.Fatalf("err = %v", err)
	}
}

func TestCodexEmptyDataFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"created": 1})
	}))
	defer server.Close()
	client := &Client{Backend: BackendCodex, BaseURL: server.URL, HTTP: server.Client()}
	_, err := client.Generate(context.Background(), application.ImageGenRequest{Model: "m", Prompt: "p", N: 1})
	if err == nil || !strings.Contains(err.Error(), "no images") {
		t.Fatalf("err = %v", err)
	}
}
