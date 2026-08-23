//go:build !stt

// Package whisper is the no-CGO stub of the offline STT adapter. Default
// builds (no `-tags stt`) never compile whisper.cpp or any CGO — the
// factory reports the engine as unavailable with an actionable reason
// (.experimental/NusaShell-STT-Technical-Design.md §7, §15).
package whisper

import (
	"context"
	"fmt"

	"nusashell/application"
)

const unavailableReason = "offline STT requires a build with -tags stt and a bundled ggml model"

// Engine is a placeholder; New always fails in this build flavor.
type Engine struct{}

func New(_, _ string) (*Engine, error) {
	return nil, fmt.Errorf("whisper: %s", unavailableReason)
}

func (e *Engine) Close() error { return nil }

func (e *Engine) OfflineSTTAvailable() bool { return false }

func (e *Engine) OfflineSTTUnavailableReason() string { return unavailableReason }

func (e *Engine) TranscribeOffline(_ context.Context, _ application.OfflineSTTRequest) (string, error) {
	return "", fmt.Errorf("stt unavailable: %s", unavailableReason)
}
