package application

import (
	"context"
	"errors"
	"testing"
)

// Phase-1 port contract tests: these pin the semantics the doc requires so
// future engine adapters (whisper.cpp / sherpa-onnx / cloud-off switch)
// cannot quietly drift from the application boundary.

type stubOffline struct {
	available bool
	reason    string
	lastReq   OfflineSTTRequest
	text      string
	err       error
}

func (s *stubOffline) TranscribeOffline(_ context.Context, req OfflineSTTRequest) (string, error) {
	s.lastReq = req
	return s.text, s.err
}

func (s *stubOffline) OfflineSTTAvailable() bool           { return s.available }
func (s *stubOffline) OfflineSTTUnavailableReason() string { return s.reason }

// TestOfflineSTTPortContract verifies the interface is satisfiable by a
// plain Go struct with no CGO and that requests flow through unchanged —
// the application layer must stay engine-agnostic (doc §1, §5).
func TestOfflineSTTPortContract(t *testing.T) {
	var port OfflineTranscriber = &stubOffline{text: "Paragraf pertama", available: true}
	got, err := port.TranscribeOffline(context.Background(), OfflineSTTRequest{
		Data: []byte("wav-bytes"), Language: "id", MaxSeconds: 120,
	})
	if err != nil || got != "Paragraf pertama" {
		t.Fatalf("transcribe = %q, %v", got, err)
	}
}

// TestOfflineSTTUnavailableIsNotFatal pins doc §15: an unavailable local
// engine surfaces a reason instead of crashing the app. Callers decide how
// to degrade (e.g. read_audio falls back to cloud STT or chat).
func TestOfflineSTTUnavailableIsNotFatal(t *testing.T) {
	status := OfflineTranscriberStatus(&stubOffline{available: false, reason: "model not installed"})
	if status.OfflineSTTAvailable() {
		t.Fatal("engine should report unavailable")
	}
	if status.OfflineSTTUnavailableReason() == "" {
		t.Error("unavailable engine should explain why")
	}
}

// TestOfflineSTTErrorPassthrough keeps underlying causes intact for logs
// while callers wrap user-facing messages (doc §15).
func TestOfflineSTTErrorPassthrough(t *testing.T) {
	port := OfflineTranscriber(&stubOffline{err: errors.New("native init failed")})
	_, err := port.TranscribeOffline(context.Background(), OfflineSTTRequest{})
	if err == nil || err.Error() != "native init failed" {
		t.Errorf("underlying error should pass through, got %v", err)
	}
}
