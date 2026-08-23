package ai

import (
	"os"
	"os/exec"
	"path/filepath"

	"nusashell/application"
	"nusashell/infrastructure/tts/piper"
)

// OfflineSpeechAvailable reports whether the local piper engine can serve
// (binary found AND at least one voice installed). Used to flip the
// generate_speech tool on without an explicit settings entry.
func OfflineSpeechAvailable(dataDir string) bool {
	off := NewOfflineSynthesizer(dataDir)
	return off != nil && off.Available()
}

// NewOfflineSynthesizer wires the local piper TTS engine. Voice models live
// under <data>/models/tts/<voice>.onnx (+ .onnx.json); the piper binary is
// resolved via PIPER_BIN or PATH. Returns nil when piper is clearly not
// installed so the composition root stays clean (application treats nil as
// "offline TTS disabled").
func NewOfflineSynthesizer(dataDir string) application.OfflineSynthesizer {
	bin := os.Getenv("PIPER_BIN")
	if bin == "" {
		if _, err := exec.LookPath("piper"); err != nil {
			return nil
		}
	}
	voicesDir := os.Getenv("PIPER_VOICES_DIR")
	if voicesDir == "" {
		voicesDir = filepath.Join(dataDir, "models", "tts")
	}
	eng, err := piper.New(bin, voicesDir)
	if err != nil {
		return nil
	}
	return eng
}
