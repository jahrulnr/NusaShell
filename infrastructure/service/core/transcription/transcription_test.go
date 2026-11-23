package coretranscription

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	coremedia "github.com/jahrulnr/nusashell/infrastructure/service/core/media"
)

func provider(t *testing.T, endpoint string, style enum.Style) *aggregate.Provider {
	t.Helper()
	p, err := aggregate.NewProvider("p", "", endpoint, style)
	if err != nil {
		t.Fatal(err)
	}
	p.SetApiKey("k")
	return &p
}

func model(id string) entity.Model {
	m, _ := entity.NewModel(id)
	return m
}

func TestTranscriptionsMultipart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		files := r.MultipartForm.File["file"]
		if len(files) != 1 || files[0].Filename != "audio.ogg" || r.FormValue("language") != "id" || r.FormValue("model") != "openai/whisper-large-v3" {
			t.Errorf("form = %v files = %v", r.MultipartForm.Value, files)
		}
		_, _ = io.WriteString(w, `{"text":"  halo dunia "}`)
	}))
	defer srv.Close()
	got, err := New(coremedia.New(coremedia.Options{})).Transcribe(context.Background(), provider(t, srv.URL+"/v1", enum.StyleResponses),
		model("openai/whisper-large-v3"), dservice.TranscriptionRequest{Audio: dservice.Media{MediaType: "audio/ogg; codecs=opus", Data: []byte("OggS")}, Language: "id"})
	if err != nil || got != "halo dunia" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestGeminiTranscription(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(raw), `"mimeType":"audio/mpeg"`) || !strings.Contains(string(raw), "Transcribe") {
			t.Errorf("body = %s", raw)
		}
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"hello there"}]}}]}`)
	}))
	defer srv.Close()
	got, err := New(coremedia.New(coremedia.Options{})).Transcribe(context.Background(), provider(t, srv.URL+"/v1beta", enum.StyleGenerateContent),
		model("gemini-2.5-flash"), dservice.TranscriptionRequest{Audio: dservice.Media{MediaType: "audio/mpeg", Data: []byte("ID3")}})
	if err != nil || got != "hello there" {
		t.Fatalf("got %q, %v", got, err)
	}
}
