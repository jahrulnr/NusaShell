package media

import (
	"context"
	"errors"
	"testing"
)

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

func TestOfflineSTTPortContract(t *testing.T) {
	var port OfflineTranscriber = &stubOffline{text: "Paragraf pertama", available: true}
	got, err := port.TranscribeOffline(context.Background(), OfflineSTTRequest{
		Data: []byte("wav-bytes"), Language: "id", MaxSeconds: 120,
	})
	if err != nil || got != "Paragraf pertama" {
		t.Fatalf("transcribe = %q, %v", got, err)
	}
}

func TestOfflineSTTUnavailableIsNotFatal(t *testing.T) {
	status := OfflineTranscriberStatus(&stubOffline{available: false, reason: "model not installed"})
	if status.OfflineSTTAvailable() {
		t.Fatal("engine should report unavailable")
	}
	if status.OfflineSTTUnavailableReason() == "" {
		t.Error("unavailable engine should explain why")
	}
}

func TestOfflineSTTErrorPassthrough(t *testing.T) {
	port := OfflineTranscriber(&stubOffline{err: errors.New("native init failed")})
	_, err := port.TranscribeOffline(context.Background(), OfflineSTTRequest{})
	if err == nil || err.Error() != "native init failed" {
		t.Errorf("underlying error should pass through, got %v", err)
	}
}
