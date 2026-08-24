package application

import (
	"context"
	"testing"

	"nusashell/domain"
)

// Shared harness for the offline-fallback UX tests. The cloud side uses the
// real STT client pointed at an httptest server; the offline side is a stub
// OfflineTranscriber.
type sttHarness struct {
	app      *App
	run      *TurnRun
	toolCall domain.ToolCall
	cloud    *scriptedCloudSTT
	offline  *stubOfflineTranscriber
}

func newSttHarness(t *testing.T) *sttHarness {
	t.Helper()
	cloud := &scriptedCloudSTT{}
	offline := &stubOfflineTranscriber{available: true, text: "lokal"}

	audioPath := writeTestFile(t, t.TempDir(), "clip.mp3")
	app := &App{
		Providers: &fakeProviderStore{items: map[string]*domain.Provider{
			"p1": {ID: "p1", Kind: domain.ProviderChat, Enabled: true,
				BaseURL: "https://api.example.com/v1",
				Models:  []domain.Model{{ID: "m", Kind: domain.ModelKindSTT}}},
		}},
		Credentials:               &memCreds{m: map[string]string{"p1": "sk-test"}},
		SpeechTranscriberFactory:  func(*domain.Provider, string) (SpeechTranscriber, error) { return cloud, nil },
		OfflineTranscriberFactory: func() (OfflineTranscriber, error) { return offline, nil },
		Settings:                  &fakeSettingsStore{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	return &sttHarness{
		app: app, run: run, cloud: cloud, offline: offline,
		toolCall: domain.ToolCall{ID: "tc1", Name: "read_media", Args: filePathArgs(audioPath, "")},
	}
}

// scriptedCloudSTT is a SpeechTranscriber with programmable outcome.
type scriptedCloudSTT struct {
	text string
	err  error
}

func (s *scriptedCloudSTT) Transcribe(_ context.Context, _ STTRequest) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return s.text, nil
}

// stubOfflineTranscriber is the local-engine stand-in.
type stubOfflineTranscriber struct {
	available bool
	reason    string
	text      string
	err       error
}

func (s *stubOfflineTranscriber) TranscribeOffline(_ context.Context, _ OfflineSTTRequest) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return s.text, nil
}

func (s *stubOfflineTranscriber) OfflineSTTAvailable() bool { return s.available }

func (s *stubOfflineTranscriber) OfflineSTTUnavailableReason() string {
	if s.reason == "" {
		return "offline stt not available in this build"
	}
	return s.reason
}
