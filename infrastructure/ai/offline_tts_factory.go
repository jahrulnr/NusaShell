package ai

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

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
// resolved via PIPER_BIN, PATH, or the one-click installer's managed copy
// at <data>/piper/<goos>-<goarch>/piper. Returns nil when piper is clearly
// not installed so the composition root stays clean (application treats nil
// as "offline TTS disabled").
func NewOfflineSynthesizer(dataDir string) application.OfflineSynthesizer {
	bin := os.Getenv("PIPER_BIN")
	if bin != "" {
		// PIPER_BIN takes precedence, but only when it points at an
		// existing binary. A missing path falls through to the next
		// resolution layer instead of wiring an engine that can never run.
		if _, err := os.Stat(bin); err != nil {
			bin = ""
		}
	}
	if bin == "" {
		if _, err := exec.LookPath("piper"); err != nil {
			// Fall back to the managed install from the Settings one-click
			// installer (binary + espeak-ng-data + libs under <data>/piper).
			bin = filepath.Join(dataDir, "piper", runtime.GOOS+"-"+runtime.GOARCH, "piper")
			if runtime.GOOS == "windows" {
				bin += ".exe"
			}
			if _, err := os.Stat(bin); err != nil {
				return nil
			}
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
