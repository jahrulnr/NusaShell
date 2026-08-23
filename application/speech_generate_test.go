package application

import (
	"context"
	"strings"
	"testing"

	"nusashell/domain"
)

// --- fakes ---

type fakeOnlineTTS struct {
	got    TTSRequest
	result *TTSResult
	err    error
}

func (f *fakeOnlineTTS) Synthesize(_ context.Context, req TTSRequest) (*TTSResult, error) {
	f.got = req
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

type fakeOfflineTTS struct {
	available bool
	got       TTSRequest
	result    *TTSResult
	err       error
}

func (f *fakeOfflineTTS) Available() bool           { return f.available }
func (f *fakeOfflineTTS) UnavailableReason() string { return "offline tts not built" }
func (f *fakeOfflineTTS) Synthesize(req TTSRequest) (*TTSResult, error) {
	f.got = req
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

type fakeSettingsTTS struct{ settings domain.Settings }

func (f *fakeSettingsTTS) Get() domain.Settings        { return f.settings }
func (f *fakeSettingsTTS) Set(s domain.Settings) error { f.settings = s; return nil }

func mp3Bytes() []byte { return []byte("ID3 fake mp3 audio") }

// wavMagic returns minimal bytes carrying a real RIFF/WAVE signature so the
// shared generated-media persistence path (magic-number sniffing) accepts it.
func wavMagic() []byte {
	return []byte{'R', 'I', 'F', 'F', 0x10, 0, 0, 0, 'W', 'A', 'V', 'E', 'f', 'm', 't', ' ', 16, 0, 0, 0}
}

func ttsApp(t *testing.T, online *fakeOnlineTTS, offline *fakeOfflineTTS) *App {
	t.Helper()
	app := &App{
		Providers: &fakeProviderStore{items: map[string]*domain.Provider{
			"ttsprov": {ID: "ttsprov", Kind: domain.ProviderChat, Enabled: true, BaseURL: "https://api.example.com/v1"},
		}},
		Credentials: &memCreds{m: map[string]string{"ttsprov": "sk-tts"}},
		Attachments: &memAttachmentStore{root: t.TempDir()},
		Settings:    &fakeSettingsTTS{},
		Logs:        &fakeLogStore{},
	}
	if online != nil {
		app.SpeechSynthesizerFactory = func(*domain.Provider, string) (SpeechSynthesizer, error) { return online, nil }
	}
	if offline != nil {
		app.OfflineSynthesizer = offline
	}
	return app
}

func ttsRun(t *testing.T) (*TurnRun, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	return run, cancel
}

// --- tests ---

func TestGenerateSpeechOnlineWins(t *testing.T) {
	online := &fakeOnlineTTS{result: &TTSResult{Audio: mp3Bytes(), MediaType: "audio/mpeg", Ext: "mp3", Provider: "openai", Model: "tts-1", Voice: "alloy"}}
	offline := &fakeOfflineTTS{available: true}
	app := ttsApp(t, online, offline)
	app.Settings = &fakeSettingsTTS{settings: domain.Settings{TTSProviderID: "ttsprov", TTSModelID: "tts-1"}}
	run, cancel := ttsRun(t)
	defer cancel()

	out, atts, err := app.executeGenerateSpeech(run, domain.ToolCall{ID: "tc1", Args: `{"text":"halo dunia"}`}, app.Settings.Get())
	if err != nil {
		t.Fatalf("online route failed: %v", err)
	}
	if online.got.Text != "halo dunia" {
		t.Errorf("online should receive text, got %q", online.got.Text)
	}
	if offline.got.Text != "" {
		t.Error("offline must not be called when online succeeds")
	}
	if len(atts) != 1 || atts[0].Type != "audio" || !strings.HasPrefix(atts[0].DataURL, "data:audio/mpeg;base64,") {
		t.Fatalf("expected one mp3 attachment, got %+v", atts)
	}
	if !strings.Contains(out, "status: completed") {
		t.Errorf("yaml meta missing, got %q", out)
	}
}

func TestGenerateSpeechFallsBackToOfflineWhenCloudFails(t *testing.T) {
	online := &fakeOnlineTTS{err: errTTS("HTTP 503 down")}
	offline := &fakeOfflineTTS{available: true, result: &TTSResult{Audio: wavMagic(), MediaType: "audio/wav", Ext: "wav", Provider: "piper"}}
	app := ttsApp(t, online, offline)
	app.Settings = &fakeSettingsTTS{settings: domain.Settings{TTSProviderID: "ttsprov", TTSModelID: "tts-1"}}
	run, cancel := ttsRun(t)
	defer cancel()

	out, atts, err := app.executeGenerateSpeech(run, domain.ToolCall{ID: "tc1", Args: `{"text":"halo","format":"wav"}`}, app.Settings.Get())
	if err != nil {
		t.Fatalf("offline should serve after cloud failure: %v", err)
	}
	if offline.got.Text != "halo" {
		t.Errorf("offline should be called with the text, got %q", offline.got.Text)
	}
	if len(atts) != 1 || atts[0].MediaType != "audio/wav" {
		t.Errorf("expected wav attachment from piper, got %+v", atts)
	}
	_ = out
}

func TestGenerateSpeechZeroConfigOffline(t *testing.T) {
	offline := &fakeOfflineTTS{available: true, result: &TTSResult{Audio: wavMagic(), MediaType: "audio/wav", Ext: "wav", Provider: "piper"}}
	app := ttsApp(t, nil, offline) // no online factory at all
	run, cancel := ttsRun(t)
	defer cancel()

	_, atts, err := app.executeGenerateSpeech(run, domain.ToolCall{ID: "tc1", Args: `{"text":"halo"}`}, domain.Settings{})
	if err != nil {
		t.Fatalf("zero-config offline should serve: %v", err)
	}
	if len(atts) != 1 {
		t.Fatalf("expected attachment, got %d", len(atts))
	}
}

func TestGenerateSpeechNothingConfigured(t *testing.T) {
	app := ttsApp(t, nil, nil)
	run, cancel := ttsRun(t)
	defer cancel()

	out, _, err := app.executeGenerateSpeech(run, domain.ToolCall{ID: "tc1", Args: `{"text":"halo"}`}, domain.Settings{})
	if err == nil {
		t.Fatal("expected error when nothing is configured")
	}
	if !strings.Contains(out, "No speech generation model is configured") {
		t.Errorf("guidance message missing, got %q", out)
	}
}

func TestGenerateSpeechValidatesInput(t *testing.T) {
	app := ttsApp(t, nil, nil)
	run, cancel := ttsRun(t)
	defer cancel()

	if _, _, err := app.executeGenerateSpeech(run, domain.ToolCall{ID: "tc", Args: `{}`}, domain.Settings{}); err == nil {
		t.Error("empty text must fail")
	}
	if _, _, err := app.executeGenerateSpeech(run, domain.ToolCall{ID: "tc", Args: `{"text":"hi","format":"flac"}`}, domain.Settings{}); err == nil {
		t.Error("unknown format must fail")
	}
}
