// Package whisper implements application.OfflineTranscriber by shelling out
// to the whisper.cpp whisper-cli binary. There is deliberately NO CGO here —
// neither on the default build path nor behind a build tag — so every
// NusaShell binary stays pure Go; the engine and GGML models are plain
// release artifacts sitting in the data directory (piper TTS pattern).
//
// One whisper-cli process is launched per transcription request. The binary
// and model are re-resolved on every call, so an engine that disappears
// mid-session degrades to a per-call error instead of breaking the app
// (.experimental/offline-stt-assessment.md §3.b contracts).
package whisper

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	// defaultPrompt biases decoding toward NusaShell vocabulary; the
	// benchmark showed "NusaShell" -> "Nusasial" without it
	// (.experimental/stt-bench/RESULTS.md).
	defaultPrompt = "NusaShell"

	// inferTimeout bounds one transcription. Tool audio is short; the
	// "long operation is not an error" principle applies, so this is a
	// runaway guard, not a UX target.
	inferTimeout = 45 * time.Minute
)

// Engine carries only configuration. Binaries and models are plain files;
// nothing is loaded at construction time, so wiring can never fail at
// startup (degradation contract §3.b-1).
type Engine struct {
	bin      string // optional: absolute path or PATH name privileges over env/managed
	modelDir string // directory holding ggml-*.bin models
	prompt   string
}

// New builds the engine. binaryPath may be empty (resolve via
// WHISPER_BIN / managed data-dir copy / PATH). modelsDir holds the
// downloaded GGML models.
func New(binaryPath, modelsDir string) *Engine {
	if strings.TrimSpace(modelsDir) == "" {
		modelsDir = "."
	}
	return &Engine{
		bin:      strings.TrimSpace(binaryPath),
		modelDir: modelsDir,
		prompt:   defaultPrompt,
	}
}

// SetPrompt overrides the initial_prompt bias (tests).
func (e *Engine) SetPrompt(p string) { e.prompt = p }

// OfflineSTTAvailable reports whether the binary and a model are resolvable
// right now. Cheap (LookPath/Stat/Glob only — no process is spawned).
func (e *Engine) OfflineSTTAvailable() bool {
	return e.OfflineSTTUnavailableReason() == ""
}

// OfflineSTTUnavailableReason names the first missing piece so the caller
// sees an actionable message (.experimental/NusaShell-STT-Technical-Design.md
// §15 style): engine first, then model.
func (e *Engine) OfflineSTTUnavailableReason() string {
	if _, err := e.lookupBinary(); err != nil {
		return err.Error()
	}
	if _, err := resolveModel(e.modelDir, ""); err != nil {
		return err.Error()
	}
	return ""
}

// lookupBinary resolves whisper-cli for the current platform with the
// instance override taking priority, then WHISPER_BIN, the managed
// one-click-install copy, and finally $PATH. Every step fails softly:
// an unset env var or a missing managed copy simply falls through.
func (e *Engine) lookupBinary() (string, error) {
	if e.bin != "" {
		return locateBinary(e.bin)
	}
	if env := strings.TrimSpace(os.Getenv("WHISPER_BIN")); env != "" {
		return locateBinary(env)
	}
	if managed := e.managedBinary(); managed != "" {
		return managed, nil
	}
	return locateBinary("whisper-cli")
}

// locateBinary accepts a name resolvable on PATH or an existing path.
func locateBinary(path string) (string, error) {
	if resolved, err := exec.LookPath(path); err == nil {
		return resolved, nil
	}
	if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
		return path, nil
	}
	return "", fmt.Errorf("whisper.cpp engine not found (%q) — set WHISPER_BIN, add whisper-cli to PATH, or install from Settings", path)
}

// managedBinary returns the one-click installer copy
// <data>/whisper/<goos>-<goarch>/whisper-cli[.exe], derived from
// modelDir = <data>/models/stt. When modelDir is not the managed layout
// (a hand-set NUSASHELL_STT_MODEL), the derived path simply doesn't exist
// and the caller falls through to PATH.
func (e *Engine) managedBinary() string {
	data := filepath.Dir(filepath.Dir(e.modelDir))
	name := "whisper-cli"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(data, "whisper", runtime.GOOS+"-"+runtime.GOARCH, name)
	if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
		return path
	}
	return ""
}

// resolveModel returns the ggml model path inside dir: fileName when given
// (with optional .bin suffix), otherwise the first ggml-*.bin.
func resolveModel(dir, fileName string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		dir = "."
	}
	if fileName != "" {
		path := fileName
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, fileName)
		}
		if !strings.HasSuffix(path, ".bin") {
			path += ".bin"
		}
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("whisper: model %q not installed in %s", fileName, dir)
		}
		return path, nil
	}
	matches, err := filepath.Glob(filepath.Join(dir, "ggml-*.bin"))
	if err != nil || len(matches) == 0 {
		return "", fmt.Errorf("whisper: model not installed — no ggml-*.bin in %s (install from Settings)", dir)
	}
	return matches[0], nil
}

// FirstModel returns the path of the first installed ggml model in dir, or
// "" when none.
func FirstModel(dir string) string {
	path, err := resolveModel(dir, "")
	if err != nil {
		return ""
	}
	return path
}
