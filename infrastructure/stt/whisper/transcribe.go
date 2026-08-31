package whisper

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"nusashell/application"
	"nusashell/infrastructure/nusatemp"
	clock "nusashell/pkg/time"
)

// TranscribeOffline runs one local inference: the audio bytes are decoded to
// 16 kHz mono in-process (WAV → re-encode; ffmpeg fallback for mp3/ogg/…),
// written to a temp dir, and handed to whisper-cli. The model selection flow
// (settings → env → first installed) is centralized in the factory; here the
// already-resolved path arrives via req.Model (Configuration → OfflineSTTRequest
// field added in application).
func (e *Engine) TranscribeOffline(ctx context.Context, req application.OfflineSTTRequest) (string, error) {
	if len(req.Data) == 0 {
		return "", fmt.Errorf("whisper: audio data is empty")
	}

	modelPath := strings.TrimSpace(req.Model)
	// Bare name from Settings/env (e.g. "ggml-small.bin") resolves inside
	// modelsDir; an absolute path passes through untouched.
	modelPath, err := resolveModel(e.modelDir, modelPath)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(modelPath); err != nil {
		return "", fmt.Errorf("whisper: model not valid: %w", err)
	}

	bin, err := e.lookupBinary()
	if err != nil {
		return "", err
	}

	// decode → canonical 16 kHz mono int16 PCM
	wav, err := normalizeTo16kMonoWav(ctx, req.Data, req.MaxSeconds)
	if err != nil {
		return "", err
	}

	work, err := nusatemp.MkdirTemp("stt-*")
	if err != nil {
		return "", fmt.Errorf("whisper: temp dir: %w", err)
	}
	defer os.RemoveAll(work)

	in := filepath.Join(work, "in.wav")
	if err := os.WriteFile(in, wav, 0o644); err != nil {
		return "", fmt.Errorf("whisper: write input: %w", err)
	}
	out := filepath.Join(work, "out") // whisper-cli adds ".txt"

	args := []string{"-m", modelPath, "-f", in, "-nt", "-of", out}
	if lang := strings.TrimSpace(req.Language); lang != "" && lang != "auto" {
		args = append(args, "-l", lang)
	}
	if req.Translate {
		args = append(args, "-tr")
	}
	if e.prompt != "" {
		args = append(args, "--prompt", e.prompt)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, inferTimeout)
	defer cancel()
	cmd := exec.CommandContext(timeoutCtx, bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	start := clock.NewTime().Time()
	err = cmd.Run()
	elapsed := clock.NewTime().Since(start)

	if timeoutCtx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("whisper: timed out after %s of inference", elapsed.Round(time.Millisecond))
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		msg := lastLine(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("whisper-cli failed (%s): %s", elapsed.Round(time.Millisecond), msg)
	}

	raw, err := os.ReadFile(out + ".txt")
	if err != nil {
		return "", fmt.Errorf("whisper: read transcript: %w", err)
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return "", fmt.Errorf("whisper: empty transcript after %s of inference", elapsed.Round(time.Millisecond))
	}
	return text, nil
}

// lastLine returns the last non-empty line of s (whisper-cli errors land at
// the end of stderr after the model-load banner).
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

// normalizeTo16kMonoWav guarantees whisper-cli's required input format
// (16-bit PCM, mono, 16 kHz). WAV input is decoded in-process and re-encoded
// (whisper-cli rejects f32/other-rate WAVs entirely); everything else goes
// through piped ffmpeg producing int16 PCM, then gets the WAV header.
func normalizeTo16kMonoWav(ctx context.Context, data []byte, maxSeconds int) ([]byte, error) {
	if samples, err := decodeWavInProcess(data); err == nil {
		samples, err = capSamples(samples, maxSeconds)
		if err != nil {
			return nil, err
		}
		return encodeWav16(floatToInt16(samples)), nil
	}
	i16s, err := decodeViaFFmpegInt16(ctx, data)
	if err != nil {
		return nil, err
	}
	i16s, err = capSamplesInt16(i16s, maxSeconds)
	if err != nil {
		return nil, err
	}
	return encodeWav16(i16s), nil
}

// floatToInt16 scales float samples [-1, 1] into int16 with clipping.
func floatToInt16(in []float32) []int16 {
	out := make([]int16, len(in))
	for i, s := range in {
		if s > 1 {
			s = 1
		}
		if s < -1 {
			s = -1
		}
		out[i] = int16(s * 32767)
	}
	return out
}

// capSamples enforces the port's MaxSeconds duration guard on float32 PCM.
func capSamples(samples []float32, maxSeconds int) ([]float32, error) {
	if maxSeconds <= 0 {
		return samples, nil
	}
	if len(samples) > maxSeconds*sampleRate {
		return nil, fmt.Errorf("whisper: audio exceeds %d seconds cap", maxSeconds)
	}
	return samples, nil
}

// capSamplesInt16 is the int16 variant of capSamples.
func capSamplesInt16(samples []int16, maxSeconds int) ([]int16, error) {
	if maxSeconds <= 0 {
		return samples, nil
	}
	if len(samples) > maxSeconds*sampleRate {
		return nil, fmt.Errorf("whisper: audio exceeds %d seconds cap", maxSeconds)
	}
	return samples, nil
}
