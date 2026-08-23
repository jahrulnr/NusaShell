//go:build stt

package whisper

import (
	"context"
	"os"
	"strings"
	"testing"

	"nusashell/application"
)

// Integration tests require the real ggml model and the benchmark WAV
// fixtures produced by .experimental/stt-bench. They are skipped unless:
//
//	NUSASHELL_STT_MODEL=/tmp/stt-bench/whisper.cpp/models/ggml-base.bin
//	NUSASHELL_STT_FIXTURE_ID=/tmp/stt-bench-id.wav
//	NUSASHELL_STT_FIXTURE_EN=/tmp/stt-bench-en.wav
func integrationEnv() (model, idWav, enWav string, ok bool) {
	model = os.Getenv("NUSASHELL_STT_MODEL")
	idWav = os.Getenv("NUSASHELL_STT_FIXTURE_ID")
	if model == "" || idWav == "" {
		return "", "", "", false
	}
	if _, err := os.Stat(model); err != nil {
		return "", "", "", false
	}
	enWav = os.Getenv("NUSASHELL_STT_FIXTURE_EN")
	return model, idWav, enWav, true
}

func loadFixture(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return data
}

func TestIntegrationIndonesian(t *testing.T) {
	model, idWav, _, ok := integrationEnv()
	if !ok {
		t.Skip("integration env not set")
	}
	eng, err := New(model, "NusaShell")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer eng.Close()

	text, err := eng.TranscribeOffline(context.Background(), application.OfflineSTTRequest{
		Data:     loadFixture(t, idWav),
		Language: "id",
	})
	if err != nil {
		t.Fatalf("transcribe id: %v", err)
	}
	lower := strings.ToLower(text)
	for _, marker := range []string{"kecerdasan buatan", "transkripsi offline", "indonesia"} {
		if !strings.Contains(lower, marker) {
			t.Errorf("id transcript missing %q: %q", marker, text)
		}
	}
	t.Logf("id transcript: %q", text)
}

func TestIntegrationEnglish(t *testing.T) {
	model, _, enWav, ok := integrationEnv()
	if !ok || enWav == "" {
		t.Skip("integration env not set")
	}
	eng, err := New(model, "NusaShell")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer eng.Close()

	text, err := eng.TranscribeOffline(context.Background(), application.OfflineSTTRequest{
		Data:     loadFixture(t, enWav),
		Language: "en",
	})
	if err != nil {
		t.Fatalf("transcribe en: %v", err)
	}
	lower := strings.ToLower(text)
	for _, marker := range []string{"local ai shell", "offline transcription test"} {
		if !strings.Contains(lower, marker) {
			t.Errorf("en transcript missing %q: %q", marker, text)
		}
	}
	t.Logf("en transcript: %q", text)
}

func TestRejectsNon16kOrStereoAndEmptyInput(t *testing.T) {
	eng := &Engine{}
	if _, err := eng.TranscribeOffline(context.Background(), application.OfflineSTTRequest{}); err == nil {
		t.Error("empty audio should fail")
	}
	if _, err := eng.TranscribeOffline(context.Background(), application.OfflineSTTRequest{
		Data: []byte("not a wav file at all"),
	}); err == nil {
		t.Error("garbage audio should fail")
	}
}
