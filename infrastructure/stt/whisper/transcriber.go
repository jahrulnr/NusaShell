package whisper

import (
	"context"
	"path/filepath"

	"nusashell/application"
)

// Transcriber implements application.OfflineTranscriber (+Status) on top of
// the external whisper-cli binary. It is built once at wiring time and holds
// zero native state — every call re-resolves binary + model from disk, which
// delivers the degradation contracts (§3.b): never fails at construction,
// fails per-call with an actionable reason when files disappear, and sees
// newly installed models immediately without a restart.
type Transcriber struct {
	modelFile func() string // lazy settings probe: current model preference
	language  func() string
	dataDir   string
}

// NewTranscriber builds the runtime wrapper. dataDir is the NusaShell data
// directory (<data>/models/stt holds GGML models; <data>/whisper/<platform>/
// may hold the one-click-installed engine). modelFile/language are evaluated
// per call so settings edits take effect live.
func NewTranscriber(dataDir string, modelFile, language func() string) *Transcriber {
	if modelFile == nil {
		modelFile = func() string { return "" }
	}
	if language == nil {
		language = func() string { return "" }
	}
	return &Transcriber{modelFile: modelFile, language: language, dataDir: dataDir}
}

func (t *Transcriber) modelsDir() string { return modelsDir(t.dataDir) }
func (t *Transcriber) engine() *Engine   { return New("", t.modelsDir()) }
func (t *Transcriber) request(req *application.OfflineSTTRequest) application.OfflineSTTRequest {
	if req.Model == "" {
		req.Model = t.modelFile()
	}
	if req.Language == "" {
		req.Language = t.language()
	}
	return *req
}

// modelsDir returns <data>/models/stt (doc §9 runtime layout).
func modelsDir(dataDir string) string { return filepath.Join(dataDir, "models", "stt") }

// TranscribeOffline runs one whisper-cli invocation. The factory handles
// settings precedence; the engine does everything observable on disk.
func (t *Transcriber) TranscribeOffline(ctx context.Context, req application.OfflineSTTRequest) (string, error) {
	full := t.request(&req)
	return t.engine().TranscribeOffline(ctx, full)
}

// OfflineSTTAvailable mirrors Engine.OfflineSTTAvailable — cheap per call
// (LookPath + glob), no process is spawned.
func (t *Transcriber) OfflineSTTAvailable() bool { return t.engine().OfflineSTTAvailable() }

func (t *Transcriber) OfflineSTTUnavailableReason() string {
	return t.engine().OfflineSTTUnavailableReason()
}
