package ai

import (
	"fmt"
	"os"
	"path/filepath"

	"nusashell/application"
	"nusashell/infrastructure/stt/whisper"
)

// NewOfflineTranscriberFactory wires the offline STT engine selected in
// .experimental/NusaShell-STT-Technical-Design.md §3 (whisper.cpp won the
// id+en gate; see .experimental/stt-bench/RESULTS.md).
//
// Model resolution order:
//  1. NUSASHELL_STT_MODEL env var (absolute or relative path)
//  2. <data-dir>/models/stt/ggml-base.bin   (doc §9 runtime layout)
//
// The returned factory never fails at wiring time — it fails per-call with
// a clear reason when the model is missing, so a missing model degrades
// read_audio to cloud STT instead of breaking startup (doc §15).
func NewOfflineTranscriberFactory(dataDir string) application.OfflineTranscriberFactory {
	return func() (application.OfflineTranscriber, error) {
		modelPath := resolveModelPath(dataDir)
		if _, err := os.Stat(modelPath); err != nil {
			return nil, fmt.Errorf("offline STT unavailable: model not installed (%s)", modelPath)
		}
		// initial_prompt biases decoding toward NusaShell vocabulary;
		// benchmark showed "NusaShell" -> "Nusasial" without it.
		return whisper.New(modelPath, "NusaShell")
	}
}

func resolveModelPath(dataDir string) string {
	if env := os.Getenv("NUSASHELL_STT_MODEL"); env != "" {
		return env
	}
	return filepath.Join(dataDir, "models", "stt", "ggml-base.bin")
}
