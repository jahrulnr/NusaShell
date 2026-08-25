package videogen

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nusashell/application"
)

// videoAPIMock returns a test server that handles the full submit/poll/
// download flow. It captures the submit body for assertions.
func videoAPIMock(t *testing.T, captured *submitRequest) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(body, captured); err != nil {
				t.Fatal(err)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "job-1"})
		case http.MethodGet:
			if strings.Contains(r.URL.Path, "/content") {
				w.Header().Set("Content-Type", "video/mp4")
				_, _ = w.Write([]byte{0x00, 0x00, 0x00, 0x14, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":            "job-1",
				"status":        "completed",
				"unsigned_urls": []string{srv.URL + "/videos/job-1/content"},
			})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	return srv
}

func TestSubmitSendsFrameImagesForI2V(t *testing.T) {
	var captured submitRequest
	srv := videoAPIMock(t, &captured)
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, APIKey: "sk-test", HTTP: srv.Client()}
	_, err := client.Generate(context.Background(), application.VideoGenRequest{
		Model:      "google/veo-3.1",
		Prompt:     "pan right",
		References: []application.ImageReference{{MediaType: "image/png", Data: []byte{0x89, 'P', 'N', 'G'}}},
	})
	if err != nil {
		t.Fatalf("Generate error: %v", err)
	}
	if len(captured.FrameImages) != 1 {
		t.Fatalf("FrameImages = %d, want 1", len(captured.FrameImages))
	}
	if captured.FrameImages[0].FrameType != "first_frame" {
		t.Errorf("frame_type = %q, want first_frame", captured.FrameImages[0].FrameType)
	}
	if !strings.HasPrefix(captured.FrameImages[0].ImageURL.URL, "data:image/png;base64,") {
		t.Errorf("image url = %q, want data URL", captured.FrameImages[0].ImageURL.URL)
	}
}

func TestSubmitSendsInputReferencesForMultipleRefs(t *testing.T) {
	var captured submitRequest
	srv := videoAPIMock(t, &captured)
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, APIKey: "sk-test", HTTP: srv.Client()}
	_, err := client.Generate(context.Background(), application.VideoGenRequest{
		Model:  "google/veo-3.1",
		Prompt: "style match",
		References: []application.ImageReference{
			{MediaType: "image/png", Data: []byte{0x89, 'P', 'N', 'G'}},
			{MediaType: "image/jpeg", Data: []byte{0xFF, 0xD8, 0xFF}},
			{MediaType: "image/webp", Data: []byte{'R', 'I', 'F', 'F'}},
		},
	})
	if err != nil {
		t.Fatalf("Generate error: %v", err)
	}
	if len(captured.FrameImages) != 1 {
		t.Fatalf("FrameImages = %d, want 1 (first ref only)", len(captured.FrameImages))
	}
	if len(captured.InputRefs) != 2 {
		t.Fatalf("InputRefs = %d, want 2", len(captured.InputRefs))
	}
}

func TestSubmitNoFrameImagesForT2V(t *testing.T) {
	var captured submitRequest
	srv := videoAPIMock(t, &captured)
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, APIKey: "sk-test", HTTP: srv.Client()}
	_, err := client.Generate(context.Background(), application.VideoGenRequest{
		Model:  "x-ai/grok-imagine-video",
		Prompt: "a sunset",
	})
	if err != nil {
		t.Fatalf("Generate error: %v", err)
	}
	if len(captured.FrameImages) != 0 {
		t.Fatalf("FrameImages = %d, want 0 for t2v", len(captured.FrameImages))
	}
	if len(captured.InputRefs) != 0 {
		t.Fatalf("InputRefs = %d, want 0 for t2v", len(captured.InputRefs))
	}
}
