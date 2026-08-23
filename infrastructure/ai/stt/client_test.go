package stt

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nusashell/application"
)

func TestClientTranscribesViaMultipart(t *testing.T) {
	var gotPath, gotAuth, gotModel, gotPrompt, gotCT string
	var fileBytes []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
		}
		gotModel = r.FormValue("model")
		gotPrompt = r.FormValue("prompt")
		f, _, err := r.FormFile("file")
		if err != nil {
			t.Errorf("file field: %v", err)
			return
		}
		defer f.Close()
		buf := make([]byte, 4096)
		n, _ := f.Read(buf)
		fileBytes = buf[:n]
		_ = json.NewEncoder(w).Encode(map[string]string{"text": "Paragraf pertama. Hari ini kita membedah dua proyek."})
	}))
	defer server.Close()

	c := &Client{BaseURL: server.URL + "/v1", APIKey: "sk-test"}
	text, err := c.Transcribe(context.Background(), application.STTRequest{
		Model: "whisper-1", Data: []byte("RIFF-fake-audio"), Filename: "clip.mp3", Prompt: "hint",
	})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if !strings.Contains(text, "Paragraf pertama") {
		t.Errorf("transcript = %q", text)
	}
	if gotPath != "/v1/audio/transcriptions" {
		t.Errorf("path = %q, want /v1/audio/transcriptions", gotPath)
	}
	if gotAuth != "Bearer sk-test" || !strings.HasPrefix(gotCT, "multipart/form-data") {
		authz, ct := gotAuth, gotCT
		t.Errorf("headers auth=%q ct=%q", authz, ct)
	}
	if gotModel != "whisper-1" || gotPrompt != "hint" {
		t.Errorf("form model=%q prompt=%q", gotModel, gotPrompt)
	}
	if string(fileBytes) != "RIFF-fake-audio" {
		t.Errorf("uploaded bytes = %q", fileBytes)
	}
}

func TestClientSurfacesProviderError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"Audio input is not available."}}`))
	}))
	defer server.Close()

	c := &Client{BaseURL: server.URL + "/v1", APIKey: "k"}
	_, err := c.Transcribe(context.Background(), application.STTRequest{
		Model: "m", Data: []byte("x"), Filename: "a.mp3",
	})
	if err == nil || !strings.Contains(err.Error(), "HTTP 400") || !strings.Contains(err.Error(), "Audio input is not available") {
		t.Errorf("expected provider error to surface, got: %v", err)
	}
}

func TestClientRejectsMissingInput(t *testing.T) {
	c := &Client{BaseURL: "https://x/v1"}
	if _, err := c.Transcribe(context.Background(), application.STTRequest{Data: []byte("x")}); err == nil {
		t.Error("expected error for empty model")
	}
	if _, err := c.Transcribe(context.Background(), application.STTRequest{Model: "m"}); err == nil {
		t.Error("expected error for empty data")
	}
}
