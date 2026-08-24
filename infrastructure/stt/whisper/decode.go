package whisper

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/go-audio/wav"
)

// sampleRate is whisper.cpp's required input rate (whisper_cli: 16-bit WAV, mono).
const sampleRate = 16000

const ffmpegTimeout = 2 * time.Minute

// decodeWavInProcess decodes a 16 kHz mono WAV to float32 samples without
// any subprocess. The result is re-encoded by the caller because whisper-cli
// only accepts 16-bit PCM input (the original in-process pipeline kept raw f32;
// this pipeline preserves what matters: no external deps on the PCM path).
func decodeWavInProcess(data []byte) ([]float32, error) {
	dec := wav.NewDecoder(bytes.NewReader(data))
	buf, err := dec.FullPCMBuffer()
	if err != nil {
		return nil, err
	}
	if dec.SampleRate != uint32(sampleRate) || dec.NumChans != 1 {
		return nil, fmt.Errorf("want 16 kHz mono wav, got %d Hz %d channels", dec.SampleRate, dec.NumChans)
	}
	return buf.AsFloat32Buffer().Data, nil
}

// decodeViaFFmpegInt16 converts arbitrary audio (mp3, ogg, m4a, webm, …) to
// 16 kHz mono int16 samples via a piped ffmpeg process. FFmpeg is a soft
// dependency: missing ffmpeg surfaces as one actionable error path, never as
// a fatal build-time link.
func decodeViaFFmpegInt16(ctx context.Context, data []byte) ([]int16, error) {
	ffmpegPath, lookErr := exec.LookPath("ffmpeg")
	if lookErr != nil {
		return nil, fmt.Errorf("audio is not 16-bit PCM WAV and ffmpeg is not installed; install ffmpeg or provide a 16 kHz mono WAV")
	}
	fctx, cancel := context.WithTimeout(ctx, ffmpegTimeout)
	defer cancel()

	cmd := exec.CommandContext(fctx, ffmpegPath,
		"-hide_banner", "-loglevel", "error",
		"-i", "pipe:0",
		"-f", "s16le", "-ar", "16000", "-ac", "1", "pipe:1",
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
		return nil, fmt.Errorf("ffmpeg conversion failed: %s", lastLine(msg))
	}
	if out.Len() == 0 || out.Len()%2 != 0 {
		return nil, fmt.Errorf("ffmpeg produced invalid PCM (%d bytes)", out.Len())
	}
	samples := make([]int16, out.Len()/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(out.Bytes()[i*2:]))
	}
	return samples, nil
}
