package coremedia

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
)

func provider(t *testing.T, endpoint string) *aggregate.Provider {
	t.Helper()
	p, err := aggregate.NewProvider("p", "", endpoint, enum.StyleCompletion)
	if err != nil {
		t.Fatal(err)
	}
	p.SetApiKey("secret")
	return &p
}

func TestURL(t *testing.T) {
	p := provider(t, "https://openrouter.ai/api/v1/")
	for in, want := range map[string]string{
		"images":                         "https://openrouter.ai/api/v1/images",
		"/api/v1/videos/j/content?i=0":   "https://openrouter.ai/api/v1/videos/j/content?i=0",
		"https://cdn.example/x.mp4?sig=": "https://cdn.example/x.mp4?sig=",
	} {
		if got := URL(p, in); got != want {
			t.Errorf("URL(%q) = %q, want %q", in, got, want)
		}
	}
}

// A pre-signed link on another host must not receive the provider key.
func TestDownloadKeepsKeyOnProviderHost(t *testing.T) {
	var auth string
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("data"))
	}))
	defer foreign.Close()
	c := New(Options{})
	// The provider lives on "localhost"; the server answers on 127.0.0.1.
	p := provider(t, strings.Replace(foreign.URL, "127.0.0.1", "localhost", 1)+"/v1")
	got, err := c.Download(context.Background(), p, enum.StyleCompletion, foreign.URL+"/x", 1<<10)
	if err != nil || string(got.Body) != "data" {
		t.Fatalf("got %q, %v", got.Body, err)
	}
	if auth != "" {
		t.Fatalf("foreign host received %q", auth)
	}
	if _, err := c.Download(context.Background(), p, enum.StyleCompletion, foreign.URL+"/x", 2); err == nil {
		t.Fatal("download past the limit accepted")
	}
	if _, err := c.Download(context.Background(), p, enum.StyleCompletion, "file:///etc/passwd", 1<<10); err == nil {
		t.Fatal("non-http URL accepted")
	}
}

func TestEnvelopeErrorAndMediaType(t *testing.T) {
	if err := EnvelopeError([]byte(`{"data":[]}`)); err != nil {
		t.Fatalf("clean body flagged: %v", err)
	}
	if err := EnvelopeError([]byte(`{"error":{"message":"quota","code":402}}`)); err == nil || err.Error() != "quota" {
		t.Fatalf("err = %v", err)
	}
	if err := EnvelopeError([]byte(`{"error":"bad model"}`)); err == nil || err.Error() != "bad model" {
		t.Fatalf("err = %v", err)
	}
	if got := MediaType("", []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"/>`)); got != "image/svg+xml" {
		t.Fatalf("svg sniffed as %s", got)
	}
	if got := MediaType("application/octet-stream", []byte("\x89PNG\r\n\x1a\n")); got != "image/png" {
		t.Fatalf("png sniffed as %s", got)
	}
}
