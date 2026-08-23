package tts

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nusashell/application"
)

func TestClientSynthesizesViaSpeechEndpoint(t *testing.T) {
	var gotPath, gotAuth, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		b := make([]byte, 2048)
		n, _ := r.Body.Read(b)
		gotBody = string(b[:n])
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Write([]byte("ID3FAKE"))
	}))
	defer server.Close()

	c := &Client{BaseURL: server.URL + "/v1", APIKey: "sk-test"}
	res, err := c.Synthesize(context.Background(), application.TTSRequest{
		Model: "tts-1", Text: "halo dunia", Voice: "alloy", Format: "mp3",
	})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if gotPath != "/v1/audio/speech" || gotAuth != "Bearer sk-test" {
		t.Errorf("path=%q auth=%q", gotPath, gotAuth)
	}
	for _, want := range []string{"\"model\":\"tts-1\"", "\"input\":\"halo dunia\"", "\"voice\":\"alloy\"", "\"response_format\":\"mp3\""} {
		if !strings.Contains(gotBody, want) {
			errfmt := "body missing %s: %s"
			t.Errorf(errfmt, want, gotBody)
		}
	}
	if string(res.Audio) != "ID3FAKE" || res.MediaType != "audio/mpeg" || res.Ext != "mp3" {
		t.Errorf("result = %+v", res)
	}
}

func TestClientSurfacesProviderError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"bad voice"}}`))
	}))
	defer server.Close()

	c := &Client{BaseURL: server.URL + "/v1", APIKey: "k"}
	_, err := c.Synthesize(context.Background(), application.TTSRequest{Model: "m", Text: "x"})
	if err == nil || !strings.Contains(err.Error(), "HTTP 400") || !strings.Contains(err.Error(), "bad voice") {
		t.Errorf("expected provider error surfaced, got %v", err)
	}
}
