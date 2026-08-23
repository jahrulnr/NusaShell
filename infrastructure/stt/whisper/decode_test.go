//go:build stt

package whisper

import (
	"context"
	"math"
	"strings"
	"testing"
)

func float32FromBits(b uint32) float32 { return math.Float32frombits(b) }

// minimal 16 kHz mono PCM WAV header + one sample
func tinyWav() []byte {
	h := []byte("RIFF")
	h = append(h, 36, 0, 0, 0) // riff size (min)
	h = append(h, "WAVE"...)
	h = append(h, "fmt "...)
	h = append(h, 16, 0, 0, 0)      // fmt chunk size
	h = append(h, 1, 0)             // PCM
	h = append(h, 1, 0)             // mono
	h = append(h, 0x80, 0x3e, 0, 0) // 16000
	h = append(h, 0, 0x7d, 0, 0)    // byte rate 32000
	h = append(h, 2, 0)             // block align
	h = append(h, 16, 0)            // bits
	h = append(h, "data"...)
	h = append(h, 2, 0, 0, 0) // data size
	h = append(h, 0, 0)       // one int16 sample
	return h
}

func TestDecodeWavInProcessAccepts16kMono(t *testing.T) {
	samples, err := decodeWavInProcess(tinyWav())
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(samples) != 1 {
		t.Errorf("samples = %d, want 1", len(samples))
	}
}

func TestDecodeWavInProcessRejectsStereo(t *testing.T) {
	w := tinyWav()
	w[22] = 2 // channels = 2
	if _, err := decodeWavInProcess(w); err == nil || !strings.Contains(err.Error(), "channels") {
		t.Errorf("expected channel error, got %v", err)
	}
}

func TestDecodeTo16kMonoFallsThroughToFFmpeg(t *testing.T) {
	// Garbage bytes are neither WAV nor decodable by ffmpeg; the error must
	// mention ffmpeg when present (or its absence explicitly).
	_, err := decodeTo16kMono(context.Background(), []byte("definitely not audio"))
	if err == nil {
		t.Fatal("expected error for garbage input")
	}
	if !strings.Contains(err.Error(), "ffmpeg") {
		t.Errorf("error should reference ffmpeg path, got %v", err)
	}
}
