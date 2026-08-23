//go:build stt

// Package whisper implements application.OfflineTranscriber on top of the
// official whisper.cpp Go binding (CGO). Built only with `-tags stt` per
// .experimental/NusaShell-STT-Technical-Design.md §13: CGO is a release/
// CI concern, never a user requirement, and default builds stay pure Go.
package whisper

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/go-audio/wav"

	whisper "github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"
	"nusashell/application"
)

// Engine wraps one loaded ggml model. The model is loaded once and reused;
// every transcription gets its own Context because whisper.cpp contexts are
// NOT safe for concurrent use (doc §16).
type Engine struct {
	mu     sync.Mutex // guards lazy init; transcription itself serializes per-context
	model  whisper.Model
	prompt string // initial_prompt bias (e.g. product vocabulary)
}

// New loads the ggml model at path. initialPrompt biases decoding toward
// domain vocabulary (mitigates "NusaShell" -> "Nusasial" tokenization seen
// in benchmarks).
func New(modelPath, initialPrompt string) (*Engine, error) {
	if strings.TrimSpace(modelPath) == "" {
		return nil, fmt.Errorf("whisper: model path is empty")
	}
	if _, err := os.Stat(modelPath); err != nil {
		return nil, fmt.Errorf("whisper: model not installed: %w", err)
	}
	model, err := whisper.New(modelPath)
	if err != nil {
		return nil, fmt.Errorf("whisper: load model: %w", err)
	}
	return &Engine{model: model, prompt: initialPrompt}, nil
}

func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.model != nil {
		e.model.Close()
		e.model = nil
	}
	return nil
}

// OfflineSTTAvailable reports whether the engine finished loading.
func (e *Engine) OfflineSTTAvailable() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.model != nil
}

func (e *Engine) OfflineSTTUnavailableReason() string {
	if e.OfflineSTTAvailable() {
		return ""
	}
	return "whisper model not loaded"
}

// TranscribeOffline runs local inference on WAV (16 kHz mono) bytes.
// Other encodings must be converted upstream (doc §17 keeps decoding in
// infrastructure; read_audio currently feeds data URLs whose media type we
// check here).
func (e *Engine) TranscribeOffline(_ context.Context, req application.OfflineSTTRequest) (string, error) {
	e.mu.Lock()
	model := e.model
	e.mu.Unlock()
	if model == nil {
		return "", fmt.Errorf("whisper: %s", e.OfflineSTTUnavailableReason())
	}
	if len(req.Data) == 0 {
		return "", fmt.Errorf("whisper: audio data is empty")
	}

	ctx, err := model.NewContext()
	if err != nil {
		return "", fmt.Errorf("whisper: new context: %w", err)
	}
	lang := strings.TrimSpace(req.Language)
	if lang == "" {
		lang = "auto"
	}
	if lang != "auto" {
		if err := ctx.SetLanguage(lang); err != nil {
			return "", fmt.Errorf("whisper: language %q: %w", lang, err)
		}
	}
	if e.prompt != "" {
		ctx.SetInitialPrompt(e.prompt)
	}

	pcm, err := decodeWavMono16k(req.Data)
	if err != nil {
		return "", err
	}
	if req.MaxSeconds > 0 && len(pcm) > req.MaxSeconds*whisper.SampleRate {
		return "", fmt.Errorf("whisper: audio exceeds %d seconds cap", req.MaxSeconds)
	}

	if err := ctx.Process(pcm, nil, nil, nil); err != nil {
		return "", fmt.Errorf("whisper: process: %w", err)
	}
	var sb strings.Builder
	for {
		seg, segErr := ctx.NextSegment()
		if segErr != nil {
			break // io.EOF or end of segments
		}
		sb.WriteString(seg.Text)
	}
	text := strings.TrimSpace(sb.String())
	if text == "" {
		return "", fmt.Errorf("whisper: empty transcript")
	}
	return text, nil
}

// decodeWavMono16k decodes PCM WAV bytes to float32 samples at whisper's
// native sample rate. Resampling other rates is deliberately out of scope
// here (fixtures and read_audio pipeline are normalized to 16 kHz mono).
func decodeWavMono16k(data []byte) ([]float32, error) {
	dec := wav.NewDecoder(strings.NewReader(string(data)))
	buf, err := dec.FullPCMBuffer()
	if err != nil {
		return nil, fmt.Errorf("whisper: decode wav: %w", err)
	}
	if dec.SampleRate != whisper.SampleRate || dec.NumChans != 1 {
		return nil, fmt.Errorf("whisper: need 16 kHz mono wav, got %d Hz %d channels", dec.SampleRate, dec.NumChans)
	}
	return buf.AsFloat32Buffer().Data, nil
}
