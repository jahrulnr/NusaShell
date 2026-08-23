package piper

import (
	"os"
	"strings"
	"testing"

	"nusashell/application"
)

func TestResolveVoiceDefaultPicksFirstOnnx(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(dir+"/b.onnx", []byte("x"), 0o644)
	os.WriteFile(dir+"/a.onnx", []byte("x"), 0o644)
	eng, err := New("", dir)
	if err != nil {
		t.Fatal(err)
	}
	v, err := eng.resolveVoice("")
	if err != nil || !strings.HasSuffix(v, ".onnx") {
		t.Errorf("resolveVoice(\"\") = %q, %v", v, err)
	}
}

func TestResolveVoiceExplicit(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(dir+"/id_ID-news_tts-medium.onnx", []byte("x"), 0o644)
	eng, _ := New("", dir)
	v, err := eng.resolveVoice("id_ID-news_tts-medium")
	if err != nil || !strings.HasSuffix(v, "id_ID-news_tts-medium.onnx") {
		t.Errorf("resolveVoice = %q, %v", v, err)
	}
	if _, err := eng.resolveVoice("missing-voice"); err == nil {
		t.Error("expected error for missing voice")
	}
}

func TestAvailableReflectsBinaryAndVoices(t *testing.T) {
	dir := t.TempDir()
	eng, _ := New("definitely-not-a-binary-xyz", dir)
	if eng.Available() {
		t.Error("should be unavailable without binary and voices")
	}
	if eng.UnavailableReason() == "" {
		t.Error("unavailable reason should be set")
	}
}

// Live test against a real piper install; skipped unless PIPER_BIN +
// PIPER_VOICES_DIR are set (see .experimental/stt-bench for setup).
func TestLiveSynthesizeIndonesian(t *testing.T) {
	bin := os.Getenv("PIPER_BIN")
	voices := os.Getenv("PIPER_VOICES_DIR")
	if bin == "" || voices == "" {
		t.Skip("live piper env not set")
	}
	eng, err := New(bin, voices)
	if err != nil {
		t.Fatal(err)
	}
	res, err := eng.Synthesize(application.TTSRequest{Text: "Halo tuan, ini uji coba suara offline."})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if len(res.Audio) < 1000 || res.MediaType != "audio/wav" {
		t.Errorf("unexpected result: %d bytes %s", len(res.Audio), res.MediaType)
	}
}
