package codex

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"nusashell/application"
)

const png1x1B64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAAC0lEQVR42mNk+M8AAAMBAQDJ/pLvAAAAAElFTkSuQmCC"

func TestImagesClientGenerationsJSONAndHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/generations" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content-type = %s", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("auth = %s", r.Header.Get("Authorization"))
		}
		if r.Header.Get("originator") != DefaultOriginator {
			t.Errorf("originator = %s", r.Header.Get("originator"))
		}
		if r.Header.Get("ChatGPT-Account-ID") != "acc-1" {
			t.Errorf("account = %s", r.Header.Get("ChatGPT-Account-ID"))
		}
		if r.Header.Get(codexImageTurnIDHeader) != "tc_turn" {
			t.Errorf("turn-id = %s", r.Header.Get(codexImageTurnIDHeader))
		}
		if r.Header.Get("x-codex-installation-id") != "inst-9" {
			t.Errorf("installation = %s", r.Header.Get("x-codex-installation-id"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["prompt"] != "a red fox" || body["model"] != DefaultImageModel {
			t.Errorf("body = %+v", body)
		}
		if body["background"] != "auto" || body["quality"] != "auto" || body["size"] != "auto" {
			t.Errorf("defaults = %+v", body)
		}
		if _, ok := body["n"]; ok {
			t.Errorf("n must be omitted when 1, got %v", body["n"])
		}
		if _, ok := body["images"]; ok {
			t.Errorf("generations must not send images, got %v", body["images"])
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"created":       1778832973,
			"background":    "opaque",
			"data":          []map[string]any{{"b64_json": png1x1B64}},
			"output_format": "png",
			"quality":       "medium",
			"size":          "1024x1536",
			"usage":         map[string]any{"total_tokens": 2846, "input_tokens": 1474, "output_tokens": 1372},
		})
	}))
	defer server.Close()

	client := &ImagesClient{
		BaseURL:        server.URL,
		AccessToken:    "tok",
		AccountID:      "acc-1",
		InstallationID: "inst-9",
		HTTP:           server.Client(),
	}
	result, err := client.Generate(context.Background(), application.ImageGenRequest{
		Prompt: "a red fox", N: 1, TurnID: "tc_turn",
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if result.Provider != "codex" || result.Model != DefaultImageModel || result.UsageTokens != 2846 {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Images) != 1 || result.Images[0].MediaType != "image/png" || len(result.Images[0].Bytes) < 8 {
		t.Fatalf("images = %+v", result.Images)
	}
}

func TestImagesClientEditsSendsJSONDataURLs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/edits" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			t.Errorf("content-type = %s (must be JSON, not multipart)", r.Header.Get("Content-Type"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["prompt"] != "add a red hat" || body["model"] != "gpt-image-1.5" {
			t.Errorf("body = %+v", body)
		}
		images, _ := body["images"].([]any)
		if len(images) != 1 {
			t.Fatalf("images = %v", body["images"])
		}
		item, _ := images[0].(map[string]any)
		url, _ := item["image_url"].(string)
		if !strings.HasPrefix(url, "data:image/png;base64,") {
			t.Fatalf("image_url = %q", url)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"b64_json": png1x1B64}},
		})
	}))
	defer server.Close()

	client := &ImagesClient{BaseURL: server.URL, AccessToken: "tok", HTTP: server.Client()}
	_, err := client.Generate(context.Background(), application.ImageGenRequest{
		Model:  "gpt-image-1.5",
		Prompt: "add a red hat",
		N:      1,
		References: []application.ImageReference{
			{MediaType: "image/png", Data: []byte("PNGDATA")},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
}

func TestImagesClientUsageLimit429SetsRetryAfter(t *testing.T) {
	resetsAt := time.Now().Add(90 * time.Minute).Unix()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(codexActiveLimitHeader, codexImageGenLimitID)
		w.Header().Set("x-image-gen-primary-reset-at", strconv.FormatInt(resetsAt, 10))
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"type":      "usage_limit_reached",
				"resets_at": resetsAt,
				"plan_type": "plus",
			},
		})
	}))
	defer server.Close()

	client := &ImagesClient{BaseURL: server.URL, AccessToken: "tok", HTTP: server.Client()}
	_, err := client.Generate(context.Background(), application.ImageGenRequest{Prompt: "x", N: 1})
	if err == nil {
		t.Fatal("expected 429")
	}
	var upstream *application.UpstreamError
	if !errors.As(err, &upstream) || upstream.StatusCode != 429 {
		t.Fatalf("err = %v", err)
	}
	if upstream.RetryAfter < 60*time.Minute {
		t.Fatalf("RetryAfter = %s, want ~90m from resets_at", upstream.RetryAfter)
	}
}

func TestImagesClientDoesNotRetryInternally(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "try again", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client := &ImagesClient{BaseURL: server.URL, HTTP: server.Client()}
	_, err := client.Generate(context.Background(), application.ImageGenRequest{Prompt: "p", N: 1})
	if err == nil {
		t.Fatal("expected 503")
	}
	if hits.Load() != 1 {
		t.Fatalf("hits = %d, client must not retry internally", hits.Load())
	}
}

func TestImagesClientHonorsCancel(t *testing.T) {
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
	client := &ImagesClient{BaseURL: server.URL, HTTP: server.Client()}
	_, err := client.Generate(ctx, application.ImageGenRequest{Prompt: "p", N: 1})
	if err == nil {
		t.Fatal("expected cancel error")
	}
}

func TestImagesClientDefaultBaseURL(t *testing.T) {
	c := &ImagesClient{}
	if c.baseURL() != DefaultBaseURL {
		t.Fatalf("baseURL = %q", c.baseURL())
	}
}

func TestParseCodexImageRetryAfterPrefersBodyResetsAt(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	body, _ := json.Marshal(map[string]any{
		"error": map[string]any{"type": "usage_limit_reached", "resets_at": now.Add(2 * time.Hour).Unix()},
	})
	headers := http.Header{}
	headers.Set("Retry-After", "30")
	headers.Set(codexActiveLimitHeader, "image_gen")
	got := parseCodexImageRetryAfter(headers, body, now)
	if got < 2*time.Hour-time.Second || got > 2*time.Hour+time.Second {
		t.Fatalf("got %s, want 2h", got)
	}
}
