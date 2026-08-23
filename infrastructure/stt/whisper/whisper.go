//go:build stt

package whisper

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	whisper "github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"
	"github.com/go-audio/wav"

	"nusashell/application"
)

// Engine wraps one loaded ggml model. The model is loaded once and reused;
// every transcription gets its own Context because whisper.cpp contexts are
// NOT safe for concurrent use (doc §16).
type Engine struct {
	mu     sync.Mutex // guards model lifetime; transcription itself is per-context
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

// TranscribeOffline runs local inference. Input may be WAV (decoded in
// process) or any container ffmpeg supports (mp3, ogg, m4a...), converted
// to 16 kHz mono PCM via a piped ffmpeg process (doc §17 keeps decoding in
// infrastructure; the application layer never sees sample rates).
func (e *Engine) TranscribeOffline(ctx context.Context, req application.OfflineSTTRequest) (string, error) {
	e.mu.Lock()
	model := e.model
	e.mu.Unlock()
	if model == nil {
		return "", fmt.Errorf("whisper: %s", e.OfflineSTTUnavailableReason())
	}
	if len(req.Data) == 0 {
		return "", fmt.Errorf("whisper: audio data is empty")
	}

	pcm, err := decodeTo16kMono(ctx, req.Data)
	if err != nil {
		return "", err
	}
	if len(pcm) == 0 {
		return "", fmt.Errorf("whisper: decoded audio is empty")
	}
	if req.MaxSeconds > 0 && len(pcm) > req.MaxSeconds*int(whisper.SampleRate) {
		return "", fmt.Errorf("whisper: audio exceeds %d seconds cap", req.MaxSeconds)
	}

	wctx, err := model.NewContext()
	if err != nil {
		return "", fmt.Errorf("whisper: new context: %w", err)
	}
	lang := strings.TrimSpace(req.Language)
	if lang == "" {
		lang = "auto"
	}
	if lang != "auto" {
		if err := wctx.SetLanguage(lang); err != nil {
			return "", fmt.Errorf("whisper: language %q: %w", lang, err)
		}
	}
	if e.prompt != "" {
		wctx.SetInitialPrompt(e.prompt)
	}

	start := time.Now()
	if err := wctx.Process(pcm, nil, nil, nil); err != nil {
		return "", fmt.Errorf("whisper: process: %w", err)
	}
	var sb strings.Builder
	for {
		seg, segErr := wctx.NextSegment()
		if segErr != nil {
			break
		}
		sb.WriteString(seg.Text)
	}
	text := strings.TrimSpace(sb.String())
	if text == "" {
		return "", fmt.Errorf("whisper: empty transcript after %s of inference", time.Since(start))
	}
	return text, nil
}

// decodeTo16kMono converts arbitrary audio bytes to 16 kHz mono float32
// samples. WAV goes through the in-process parser; everything else through
// `ffmpeg -i - -f f32le -ar 16000 -ac 1 -` (piped, no temp files).
func decodeTo16kMono(ctx context.Context, data []byte) ([]float32, error) {
	if samples, wavErr := decodeWavInProcess(data); wavErr == nil {
		return samples, nil
	}
	return decodeViaFFmpeg(ctx, data)
}

func decodeWavInProcess(data []byte) ([]float32, error) {
	dec := wav.NewDecoder(bytes.NewReader(data))
	buf, err := dec.FullPCMBuffer()
	if err != nil {
		return nil, err
	}
	if dec.SampleRate != uint32(whisper.SampleRate) || dec.NumChans != 1 {
		return nil, fmt.Errorf("want 16 kHz mono wav, got %d Hz %d channels", dec.SampleRate, dec.NumChans)
	}
	return buf.AsFloat32Buffer().Data, nil
}

const ffmpegTimeout = 2 * time.Minute

func decodeViaFFmpeg(ctx context.Context, data []byte) ([]float32, error) {
	ffmpegPath, lookErr := exec.LookPath("ffmpeg")
	if lookErr != nil {
		return nil, fmt.Errorf("audio is not a WAV and ffmpeg is not installed; install ffmpeg or provide 16 kHz mono WAV")
	}
	fctx, cancel := context.WithTimeout(ctx, ffmpegTimeout)
	defer cancel()

	cmd := exec.CommandContext(fctx, ffmpegPath,
		"-hide_banner", "-loglevel", "error",
		"-i", "pipe:0",
		"-f", "f32le", "-ar", "16000", "-ac", "1", "pipe:1",
	)
	cmd.Stdin = bytes.NewReader(data)
	var out bytes.Buffer
	cmd.Stdout = &out
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("ffmpeg conversion failed: %s", msg)
	}
	if out.Len() == 0 || out.Len()%4 != 0 {
		return nil, fmt.Errorf("ffmpeg produced invalid PCM (%d bytes)", out.Len())
	}
	pcm := make([]float32, out.Len()/4)
	for i := range pcm {
		bits := uint32(out.Bytes()[i*4]) | uint32(out.Bytes()[i*4+1])<<8 | uint32(out.Bytes()[i*4+2])<<16 | uint32(out.Bytes()[i*4+3])<<24
		pcm[i] = math.Float32frombits(bits)
	}
	return pcm, nil
}
