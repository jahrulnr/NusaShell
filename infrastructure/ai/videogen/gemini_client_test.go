package videogen

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"nusashell/application"
	"nusashell/domain"
)

// geminiMockBuilder builds a Veo mock server. Poll responses are settable
// after creation via the returned *[]map[string]any so the download URI
// can reference the server's own URL.
func geminiMockBuilder(t *testing.T, captured *geminiSubmitRequest) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	pollResponses := []map[string]any{}
	var srv *httptest.Server
	pollIdx := 0
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			if !strings.Contains(r.URL.Path, ":predictLongRunning") {
				t.Fatalf("unexpected POST path: %s", r.URL.Path)
			}
			if r.Header.Get("x-goog-api-key") == "" {
				t.Errorf("missing x-goog-api-key header")
			}
			if r.Header.Get("Authorization") != "" {
				t.Errorf("Gemini must not use Authorization; got %q", r.Header.Get("Authorization"))
			}
			body, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(body, captured); err != nil {
				t.Fatal(err)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"name": "operations/veo-123"})
		case http.MethodGet:
			if strings.Contains(r.URL.Path, "operations/") {
				if pollIdx >= len(pollResponses) {
					pollIdx = len(pollResponses) - 1
				}
				if pollIdx < 0 {
					t.Fatal("no poll responses configured")
				}
				resp := pollResponses[pollIdx]
				pollIdx++
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(resp)
				return
			}
			// Download endpoint — return a small MP4
			if r.Header.Get("x-goog-api-key") == "" {
				t.Errorf("download missing x-goog-api-key header")
			}
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write([]byte{0x00, 0x00, 0x00, 0x14, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	return srv, &pollResponses
}

func geminiDoneResponse(videoURI string) map[string]any {
	return map[string]any{
		"name": "operations/veo-123",
		"done": true,
		"response": map[string]any{
			"generateVideoResponse": map[string]any{
				"generatedSamples": []map[string]any{
					{"video": map[string]any{"uri": videoURI}},
				},
			},
		},
	}
}

func TestGeminiSubmitTextToVideoBody(t *testing.T) {
	var captured geminiSubmitRequest
	srv, polls := geminiMockBuilder(t, &captured)
	defer srv.Close()
	*polls = []map[string]any{geminiDoneResponse(srv.URL + "/download")}

	client := &GeminiClient{
		BaseURL: srv.URL + "/v1beta", APIKey: "gem-key",
		HTTP: srv.Client(), PollInterval: 1 * time.Millisecond,
	}
	_, err := client.Generate(context.Background(), application.VideoGenRequest{
		Model:       "veo-3.1-generate-preview",
		Prompt:      "a lion in the savannah",
		DurationSec: 8,
		Resolution:  "1080p",
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(captured.Instances) != 1 {
		t.Fatalf("instances = %d, want 1", len(captured.Instances))
	}
	if captured.Instances[0].Prompt != "a lion in the savannah" {
		t.Errorf("prompt = %q", captured.Instances[0].Prompt)
	}
	if captured.Instances[0].Image != nil {
		t.Errorf("image should be nil for t2v")
	}
	if captured.Parameters == nil {
		t.Fatal("parameters missing")
	}
	if captured.Parameters.DurationSeconds != "8" {
		t.Errorf("durationSeconds = %q, want 8", captured.Parameters.DurationSeconds)
	}
	if captured.Parameters.Resolution != "1080p" {
		t.Errorf("resolution = %q, want 1080p", captured.Parameters.Resolution)
	}
}

func TestGeminiSubmitImageToVideoBody(t *testing.T) {
	var captured geminiSubmitRequest
	srv, polls := geminiMockBuilder(t, &captured)
	defer srv.Close()
	*polls = []map[string]any{geminiDoneResponse(srv.URL + "/download")}

	client := &GeminiClient{
		BaseURL: srv.URL + "/v1beta", APIKey: "gem-key",
		HTTP: srv.Client(), PollInterval: 1 * time.Millisecond,
	}
	pngData := []byte{0x89, 'P', 'N', 'G'}
	_, err := client.Generate(context.Background(), application.VideoGenRequest{
		Model:  "veo-3.1-generate-preview",
		Prompt: "pan right",
		References: []application.ImageReference{
			{MediaType: "image/png", Data: pngData},
			{MediaType: "image/jpeg", Data: []byte{0xFF, 0xD8, 0xFF}},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if captured.Instances[0].Image == nil {
		t.Fatal("image should be set for i2v")
	}
	if captured.Instances[0].Image.InlineData.MimeType != "image/png" {
		t.Errorf("image mimeType = %q", captured.Instances[0].Image.InlineData.MimeType)
	}
	decoded, err := base64.StdEncoding.DecodeString(captured.Instances[0].Image.InlineData.Data)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != string(pngData) {
		t.Errorf("image data mismatch")
	}
	if len(captured.Instances[0].ReferenceImages) != 1 {
		t.Fatalf("referenceImages = %d, want 1", len(captured.Instances[0].ReferenceImages))
	}
	if captured.Instances[0].ReferenceImages[0].ReferenceType != "asset" {
		t.Errorf("referenceType = %q, want asset", captured.Instances[0].ReferenceImages[0].ReferenceType)
	}
}

func TestGeminiPollInProgressThenDone(t *testing.T) {
	var captured geminiSubmitRequest
	srv, polls := geminiMockBuilder(t, &captured)
	defer srv.Close()
	*polls = []map[string]any{
		{"name": "operations/veo-123", "done": false},
		{"name": "operations/veo-123", "done": false},
		geminiDoneResponse(srv.URL + "/download"),
	}

	client := &GeminiClient{
		BaseURL: srv.URL + "/v1beta", APIKey: "gem-key",
		HTTP: srv.Client(), PollInterval: 1 * time.Millisecond,
	}
	res, err := client.Generate(context.Background(), application.VideoGenRequest{
		Model: "veo-3.1-generate-preview", Prompt: "test",
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(res.Video) == 0 {
		t.Fatal("no video data")
	}
	if res.MediaType != "video/mp4" {
		t.Errorf("mediaType = %q", res.MediaType)
	}
	if res.Provider != "gemini-veo" {
		t.Errorf("provider = %q", res.Provider)
	}
	if res.JobID != "operations/veo-123" {
		t.Errorf("jobID = %q", res.JobID)
	}
}

func TestGeminiOperationErrorBlock(t *testing.T) {
	var captured geminiSubmitRequest
	srv, polls := geminiMockBuilder(t, &captured)
	defer srv.Close()
	*polls = []map[string]any{
		{
			"name": "operations/veo-123",
			"done": true,
			"error": map[string]any{
				"code":    400,
				"message": "durationSeconds must be 8 for 1080p",
				"status":  "INVALID_ARGUMENT",
			},
		},
	}

	client := &GeminiClient{
		BaseURL: srv.URL + "/v1beta", APIKey: "gem-key",
		HTTP: srv.Client(), PollInterval: 1 * time.Millisecond,
	}
	_, err := client.Generate(context.Background(), application.VideoGenRequest{
		Model: "veo-3.1-generate-preview", Prompt: "test", Resolution: "1080p",
	})
	if err == nil {
		t.Fatal("expected error from operation error block")
	}
	if !strings.Contains(err.Error(), "durationSeconds must be 8 for 1080p") {
		t.Errorf("error should surface provider message verbatim, got: %v", err)
	}
}

func TestGeminiDownloadSizeCap(t *testing.T) {
	var captured geminiSubmitRequest
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &captured)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"name": "operations/veo-123"})
		case http.MethodGet:
			if strings.Contains(r.URL.Path, "operations/") {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(geminiDoneResponse(srv.URL + "/download"))
				return
			}
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write(make([]byte, MaxVideoBytes+1))
		}
	}))
	defer srv.Close()

	client := &GeminiClient{
		BaseURL: srv.URL + "/v1beta", APIKey: "gem-key",
		HTTP: srv.Client(), PollInterval: 1 * time.Millisecond,
	}
	_, err := client.Generate(context.Background(), application.VideoGenRequest{
		Model: "veo-3.1-generate-preview", Prompt: "test",
	})
	if err == nil {
		t.Fatal("expected error for oversized download")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("error should mention size cap, got: %v", err)
	}
}

func TestGeminiResolution480pOmitted(t *testing.T) {
	var captured geminiSubmitRequest
	srv, polls := geminiMockBuilder(t, &captured)
	defer srv.Close()
	*polls = []map[string]any{geminiDoneResponse(srv.URL + "/download")}

	client := &GeminiClient{
		BaseURL: srv.URL + "/v1beta", APIKey: "gem-key",
		HTTP: srv.Client(), PollInterval: 1 * time.Millisecond,
	}
	_, err := client.Generate(context.Background(), application.VideoGenRequest{
		Model: "veo-3.1-generate-preview", Prompt: "test", Resolution: "480p",
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// 480p has no Veo equivalent — resolution must be omitted so Veo
	// defaults to 720p, not rejected.
	if captured.Parameters != nil && captured.Parameters.Resolution != "" {
		t.Errorf("resolution = %q, want empty (480p omitted → Veo default 720p)", captured.Parameters.Resolution)
	}
}

func TestGeminiSubmitErrorMapsToProviderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":400,"message":"invalid model","status":"INVALID_ARGUMENT"}}`))
	}))
	defer srv.Close()

	client := &GeminiClient{
		BaseURL: srv.URL + "/v1beta", APIKey: "gem-key",
		HTTP: srv.Client(), PollInterval: 1 * time.Millisecond,
	}
	_, err := client.Generate(context.Background(), application.VideoGenRequest{
		Model: "bad-model", Prompt: "test",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	var perr *domain.ProviderError
	if !errors.As(err, &perr) {
		t.Fatalf("err = %T, want *domain.ProviderError", err)
	}
	if perr.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", perr.StatusCode)
	}
	if !strings.Contains(perr.Error(), "invalid model") {
		t.Fatalf("err = %v", perr)
	}
}

func TestGeminiContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"name": "operations/veo-123"})
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"name": "operations/veo-123", "done": false})
		}
	}))
	defer srv.Close()

	client := &GeminiClient{
		BaseURL: srv.URL + "/v1beta", APIKey: "gem-key",
		HTTP: srv.Client(), PollInterval: 1 * time.Millisecond,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := client.Generate(ctx, application.VideoGenRequest{
		Model: "veo-3.1-generate-preview", Prompt: "test",
	})
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if !strings.Contains(err.Error(), "cancel") && !strings.Contains(err.Error(), "deadline") {
		t.Errorf("error should mention cancellation, got: %v", err)
	}
}

func TestGeminiNilModelRejected(t *testing.T) {
	client := &GeminiClient{BaseURL: "https://example.test/v1beta", APIKey: "k"}
	_, err := client.Generate(context.Background(), application.VideoGenRequest{Prompt: "test"})
	if err == nil || !strings.Contains(err.Error(), "model") {
		t.Fatalf("err = %v, want model-required error", err)
	}
}

// TestGeminiLiveVeoGeneration is a key-gated, opt-in live smoke test.
// It runs ONLY when both GEMINI_API_KEY is set AND
// NUSASHELL_LIVE_VEO_SMOKE=1 is explicitly set — video generation costs
// real money and takes minutes. Never prints the credential. A green
// test always means a real video was produced: quota/permission
// rejections (429, 403) are t.Skip with an explicit reason so they
// never masquerade as a pass.
func TestGeminiLiveVeoGeneration(t *testing.T) {
	key := strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
	if key == "" {
		t.Skip("GEMINI_API_KEY not set; skipping live Veo smoke test")
	}
	if os.Getenv("NUSASHELL_LIVE_VEO_SMOKE") != "1" {
		t.Skip("NUSASHELL_LIVE_VEO_SMOKE=1 not set; skipping live Veo smoke test (video generation costs real money)")
	}
	const liveModel = "veo-3.1-generate-preview"
	client := &GeminiClient{
		BaseURL: "https://generativelanguage.googleapis.com/v1beta",
		APIKey:  key,
	}
	res, err := client.Generate(context.Background(), application.VideoGenRequest{
		Model:       liveModel,
		Prompt:      "A cinematic shot of a majestic lion in the savannah.",
		DurationSec: 8,
		Resolution:  "720p",
	})
	if err != nil {
		var perr *domain.ProviderError
		if errors.As(err, &perr) && (perr.StatusCode == 429 || perr.StatusCode == 403) {
			t.Skipf("quota/permission exhausted for this key/tier on %s (HTTP %d): %s", liveModel, perr.StatusCode, perr.Error())
		}
		t.Fatalf("live Veo generation failed: %v", err)
	}
	if len(res.Video) == 0 {
		t.Fatal("live Veo generation returned no video")
	}
	if len(res.Video) < 100 {
		t.Fatalf("generated video too small: %d bytes", len(res.Video))
	}
	if res.MediaType != "video/mp4" {
		t.Fatalf("mediaType = %q, want video/mp4", res.MediaType)
	}
	t.Logf("live proof OK: generated %d-byte %s video (job %s)", len(res.Video), res.MediaType, res.JobID)
}
