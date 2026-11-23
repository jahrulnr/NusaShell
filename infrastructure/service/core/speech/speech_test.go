package corespeech

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
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

func TestAudioSpeechAsksForMP3(t *testing.T) {
	mp3 := []byte("ID3\x04\x00\x00\x00\x00\x00\x00audio")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path != "/v1/audio/speech" || body["input"] != "halo" || body["response_format"] != "mp3" || body["speed"] != 1.5 {
			t.Errorf("%s %v", r.URL.Path, body)
		}
		if _, ok := body["voice"]; ok {
			t.Errorf("an unnamed voice was sent: %v", body["voice"])
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write(mp3)
	}))
	defer srv.Close()
	speed := 1.5
	got, err := New(coremedia.New(coremedia.Options{})).Speak(context.Background(), provider(t, srv.URL+"/v1", enum.StyleResponses),
		model("fish-audio/s2"), dservice.SpeechRequest{Text: "halo", Speed: &speed})
	if err != nil {
		t.Fatal(err)
	}
	if got.MediaType != "audio/mpeg" || !bytes.Equal(got.Data, mp3) {
		t.Fatalf("got %s %q", got.MediaType, got.Data)
	}
}

// A PCM answer is wrapped so it plays: Gemini speech is headerless L16.
func TestGeminiSpeechWrapsPCM(t *testing.T) {
	pcm := []byte{1, 2, 3, 4}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(raw), `"voiceName":"Kore"`) || !strings.Contains(string(raw), `"responseModalities":["AUDIO"]`) {
			t.Errorf("body = %s", raw)
		}
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"audio/L16;codec=pcm;rate=24000","data":"`+base64.StdEncoding.EncodeToString(pcm)+`"}}]}}]}`)
	}))
	defer srv.Close()
	got, err := New(coremedia.New(coremedia.Options{})).Speak(context.Background(), provider(t, srv.URL+"/v1beta", enum.StyleGenerateContent),
		model("gemini-2.5-flash-preview-tts"), dservice.SpeechRequest{Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if got.MediaType != "audio/wav" || string(got.Data[:4]) != "RIFF" || string(got.Data[8:12]) != "WAVE" || !bytes.Equal(got.Data[44:], pcm) {
		t.Fatalf("got %s % x", got.MediaType, got.Data)
	}
	if http.DetectContentType(got.Data) != "audio/wave" {
		t.Fatalf("sniffed as %s", http.DetectContentType(got.Data))
	}
}
