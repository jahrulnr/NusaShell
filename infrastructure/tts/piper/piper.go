// Package piper implements application.OfflineSynthesizer by shelling out
// to the rhasspy/piper CLI (no CGO — the binary and voice models are plain
// release artifacts, keeping default NusaShell builds pure Go).
//
// Layout (doc §9 style):
//
//	<data>/models/tts/<voice>.onnx        e.g. id_ID-news_tts-medium.onnx
//	<data>/models/tts/<voice>.onnx.json   matching config
//
// The piper binary is resolved via PIPER_BIN or PATH.
package piper

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"nusashell/application"
)

const synthTimeout = 5 * time.Minute

// Engine runs one piper process per request (piper is single-shot; startup
// cost ~100-200ms is acceptable for tool-driven synthesis).
type Engine struct {
	mu        sync.Mutex // serializes: piper + espeak-ng-data are not concurrency-safe per dir
	bin       string     // path to piper executable
	voicesDir string     // directory holding <voice>.onnx(+json)
}

// New builds an Engine. binPath may be empty (resolved from PATH);
// voicesDir holds the .onnx/.onnx.json voice pairs.
func New(binPath, voicesDir string) (*Engine, error) {
	if strings.TrimSpace(voicesDir) == "" {
		return nil, fmt.Errorf("piper: voices directory is required")
	}
	return &Engine{bin: binPath, voicesDir: voicesDir}, nil
}

func (e *Engine) resolveVoice(voice string) (string, error) {
	v := strings.TrimSpace(voice)
	if v == "" {
		// Default voice: first *.onnx in the voices dir (stable enough for a
		// single-voice install; multi-voice users pass voice explicitly).
		matches, err := filepath.Glob(filepath.Join(e.voicesDir, "*.onnx"))
		if err != nil || len(matches) == 0 {
			return "", fmt.Errorf("piper: no voice model found in %s", e.voicesDir)
		}
		return matches[0], nil
	}
	path := v
	if !filepath.IsAbs(path) {
		path = filepath.Join(e.voicesDir, v)
	}
	if !strings.HasSuffix(path, ".onnx") {
		path += ".onnx"
	}
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("piper: voice %q not installed in %s", voice, e.voicesDir)
	}
	return path, nil
}

func (e *Engine) Available() bool {
	_, binErr := exec.LookPath(e.bin)
	voices, _ := filepath.Glob(filepath.Join(e.voicesDir, "*.onnx"))
	return binErr == nil && len(voices) > 0
}

func (e *Engine) UnavailableReason() string {
	if _, err := exec.LookPath(e.bin); err != nil {
		return "piper binary not found (set PIPER_BIN or install piper on PATH)"
	}
	voices, _ := filepath.Glob(filepath.Join(e.voicesDir, "*.onnx"))
	if len(voices) == 0 {
		return fmt.Sprintf("no piper voice installed in %s", e.voicesDir)
	}
	return ""
}

func (e *Engine) Synthesize(req application.TTSRequest) (*application.TTSResult, error) {
	if strings.TrimSpace(req.Text) == "" {
		return nil, fmt.Errorf("piper: text is required")
	}
	modelPath, err := e.resolveVoice(req.Voice)
	if err != nil {
		return nil, err
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	piperBin := e.bin
	if piperBin == "" {
		piperBin = "piper"
	}
	cmd := exec.Command(piperBin,
		"--model", modelPath,
		"--output_file", "-", // raw wav to stdout
	)
	cmd.Stdin = strings.NewReader(req.Text + "\n")
	cmd.Env = append(os.Environ(), "LD_LIBRARY_PATH="+filepath.Dir(piperBin)+"/lib")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()
	select {
	case <-time.After(synthTimeout):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return nil, fmt.Errorf("piper: timed out after %s", synthTimeout)
	case err := <-done:
		if err != nil {
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = err.Error()
			}
			return nil, fmt.Errorf("piper: %s", msg)
		}
	}
	if stdout.Len() == 0 {
		return nil, fmt.Errorf("piper: empty audio output")
	}
	voiceName := strings.TrimSuffix(filepath.Base(modelPath), ".onnx")
	return &application.TTSResult{
		Audio: bytes.Clone(stdout.Bytes()), MediaType: "audio/wav", Ext: "wav",
		Provider: "piper", Model: voiceName, Voice: voiceName,
	}, nil
}
