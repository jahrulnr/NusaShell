package application

import (
	"context"
	"encoding/json"
	"errors"
	"nusashell/application/service/ttserr"
	"nusashell/contracts"
	"nusashell/domain"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- from tts_install_test.go ---

type fakeTTSInstaller struct {
	mu       sync.Mutex
	status   contracts.TTSInstallStatusResult
	started  []string
	failWith error
	block    chan struct{}
}

// startedCount reads the started slice under the mutex (race-safe).
func (f *fakeTTSInstaller) startedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.started)
}

func (f *fakeTTSInstaller) Status() contracts.TTSInstallStatusResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}

func (f *fakeTTSInstaller) Install(ctx context.Context, voiceID string, report func(contracts.TTSInstallProgressDTO)) error {
	f.mu.Lock()
	f.started = append(f.started, voiceID)
	block := f.block
	f.mu.Unlock()
	if block != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-block:
		}
	}
	report(contracts.TTSInstallProgressDTO{VoiceID: voiceID, Phase: "binary"})
	f.mu.Lock()
	fail := f.failWith
	f.mu.Unlock()
	if fail != nil {
		return fail
	}
	return nil
}

func ttsInstallApp(inst *fakeTTSInstaller) *App {
	app := &App{TTSInstaller: inst, Logs: &fakeLogStore{}}
	app.Bus = NewBus()
	return app
}

func waitForCondition(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	for i := 0; i < 500; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(msg)
}

func TestTTSInstallStatusReturnsSnapshot(t *testing.T) {
	inst := &fakeTTSInstaller{status: contracts.TTSInstallStatusResult{
		BinaryInstalled: true,
		Voices:          []contracts.TTSVoiceDTO{{ID: "id_ID-news_tts-medium", Installed: true}},
	}}
	out, rpcErr := ttsInstallApp(inst).handleTTSSettingsInstallStatus()
	if rpcErr != nil {
		t.Fatalf("rpc error: %v", rpcErr)
	}
	res := out.(contracts.TTSInstallStatusResult)
	if !res.BinaryInstalled || !res.Voices[0].Installed || res.Running {
		t.Errorf("unexpected status %+v", res)
	}
}

func TestTTSInstallStartRejectsUnknownVoiceImmediately(t *testing.T) {
	inst := &fakeTTSInstaller{}
	app := ttsInstallApp(inst)
	_, rpcErr := app.handleTTSSettingsInstallStart(contracts.TTSInstallStartRequest{VoiceID: "nope"})
	if rpcErr == nil || !strings.Contains(rpcErr.Message, "unknown voice") {
		t.Fatalf("expected unknown-voice validation error, got %v", rpcErr)
	}
	if len(inst.started) != 0 {
		t.Error("installer must not be invoked for unknown voice")
	}
}

func collectEvents(t *testing.T, app *App) chan contracts.Event {
	t.Helper()
	ch := make(chan contracts.Event, 16)
	_, sub, stop := app.Bus.Subscribe()
	go func() {
		defer close(ch)
		for ev := range sub {
			ch <- ev
		}
	}()
	t.Cleanup(stop)
	return ch
}

func waitForEvent(t *testing.T, events chan contracts.Event, typ string) contracts.Event {
	t.Helper()
	for {
		select {
		case ev := <-events:
			if ev.Type == typ {
				return ev
			} // skip interleaved progress events
		case <-time.After(2 * time.Second):
			t.Fatalf("%s event not emitted", typ)
		}
	}
}

func TestTTSInstallDoneEventCarriesVoiceID(t *testing.T) {
	inst := &fakeTTSInstaller{}
	app := ttsInstallApp(inst)
	events := collectEvents(t, app)

	if _, rpcErr := app.handleTTSSettingsInstallStart(contracts.TTSInstallStartRequest{VoiceID: "id_ID-news_tts-medium"}); rpcErr != nil {
		t.Fatalf("start: %v", rpcErr)
	}
	var done contracts.Event
	done = waitForEvent(t, events, contracts.EventTTSInstallDone)
	if done.Type != contracts.EventTTSInstallDone {
		t.Fatalf("unexpected event %q", done.Type)
	}
	payload, _ := json.Marshal(done.Payload)
	var prog contracts.TTSInstallProgressDTO
	if err := json.Unmarshal(payload, &prog); err != nil || prog.VoiceID != "id_ID-news_tts-medium" {
		t.Fatalf("done payload missing voice id: %s (%v)", payload, err)
	}
	waitForCondition(t, func() bool { return !app.ttsInstallRunning() }, "run flag never cleared")
}

func TestTTSInstallErrorSurfacesOnBus(t *testing.T) {
	inst := &fakeTTSInstaller{failWith: errors.New("disk full")}
	app := ttsInstallApp(inst)
	events := collectEvents(t, app)

	if _, rpcErr := app.handleTTSSettingsInstallStart(contracts.TTSInstallStartRequest{VoiceID: "id_ID-news_tts-medium"}); rpcErr != nil {
		t.Fatalf("start: %v", rpcErr)
	}
	ev := waitForEvent(t, events, contracts.EventTTSInstallError)
	if ev.Type != contracts.EventTTSInstallError {
		t.Fatalf("unexpected event %q", ev.Type)
	}
}

func TestTTSInstallSingleFlight(t *testing.T) {
	inst := &fakeTTSInstaller{block: make(chan struct{})}
	app := ttsInstallApp(inst)

	first, _ := app.handleTTSSettingsInstallStart(contracts.TTSInstallStartRequest{VoiceID: "en_US-lessac-high"})
	if !(first.(contracts.TTSInstallStartResult)).Started {
		t.Fatal("first start must begin the install")
	}
	waitForCondition(t, func() bool { return inst.startedCount() == 1 }, "first install never started")

	second, _ := app.handleTTSSettingsInstallStart(contracts.TTSInstallStartRequest{VoiceID: "en_US-lessac-high"})
	res := second.(contracts.TTSInstallStartResult)
	if res.Started || !res.Running {
		t.Errorf("second concurrent start must report running=true started=false, got %+v", res)
	}
	close(inst.block)
	waitForCondition(t, func() bool { return !app.ttsInstallRunning() }, "install never finished")
}

// --- from stt_install_test.go ---

type fakeSTTInstaller struct {
	mu       sync.Mutex
	status   contracts.STTInstallStatusResult
	started  []string
	failWith error
	block    chan struct{}
}

func (f *fakeSTTInstaller) Status() contracts.STTInstallStatusResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}

func (f *fakeSTTInstaller) Install(ctx context.Context, modelID string, report func(contracts.STTInstallProgressDTO)) error {
	f.mu.Lock()
	f.started = append(f.started, modelID)
	block := f.block
	f.mu.Unlock()
	if block != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-block:
		}
	}
	report(contracts.STTInstallProgressDTO{Phase: PhaseSTTBinary})
	f.mu.Lock()
	fail := f.failWith
	f.mu.Unlock()
	if fail != nil {
		return fail
	}
	return nil
}

func (f *fakeSTTInstaller) startedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.started)
}

func sttInstallApp(inst *fakeSTTInstaller) *App {
	app := &App{STTInstaller: inst, Logs: &fakeLogStore{}}
	app.Bus = NewBus()
	return app
}

// STT phase aliases live in the installer package; the application layer
// only sees opaque strings over the wire.
const PhaseSTTBinary = "binary"

func TestSTTInstallStatusReturnsSnapshot(t *testing.T) {
	inst := &fakeSTTInstaller{status: contracts.STTInstallStatusResult{
		Supported:       true,
		EngineInstalled: true,
		Models:          []contracts.STTModelDTO{{ID: "ggml-small", Installed: true, Default: true}},
	}}
	out, rpcErr := sttInstallApp(inst).handleSTTSettingsInstallStatus()
	if rpcErr != nil {
		t.Fatalf("rpc error: %v", rpcErr)
	}
	res := out.(contracts.STTInstallStatusResult)
	if !res.EngineInstalled || !res.Models[0].Installed || res.Running {
		t.Errorf("unexpected status %+v", res)
	}
}

func TestSTTInstallStatusNilInstallerFails(t *testing.T) {
	_, rpcErr := (&App{Logs: &fakeLogStore{}}).handleSTTSettingsInstallStatus()
	if rpcErr == nil || !strings.Contains(rpcErr.Message, "unavailable") {
		t.Fatalf("expected unavailable error, got %v", rpcErr)
	}
}

func TestSTTInstallStartValidatesModel(t *testing.T) {
	inst := &fakeSTTInstaller{}
	app := sttInstallApp(inst)

	if _, rpcErr := app.handleSTTSettingsInstallStart(contracts.STTInstallStartRequest{}); rpcErr == nil ||
		!strings.Contains(rpcErr.Message, "model_id is required") {
		t.Fatalf("expected empty-model validation, got %v", rpcErr)
	}
	if _, rpcErr := app.handleSTTSettingsInstallStart(contracts.STTInstallStartRequest{ModelID: "ggml-nope"}); rpcErr == nil ||
		!strings.Contains(rpcErr.Message, "unknown STT model") {
		t.Fatalf("expected unknown-model validation, got %v", rpcErr)
	}
	if inst.startedCount() != 0 {
		t.Error("installer must not be invoked for invalid requests")
	}
}

func TestSTTInstallDoneEventCarriesModelID(t *testing.T) {
	inst := &fakeSTTInstaller{}
	app := sttInstallApp(inst)
	events := collectEvents(t, app)

	if _, rpcErr := app.handleSTTSettingsInstallStart(contracts.STTInstallStartRequest{ModelID: "ggml-small"}); rpcErr != nil {
		t.Fatalf("start: %v", rpcErr)
	}
	done := waitForEvent(t, events, contracts.EventSTTInstallDone)
	raw, _ := json.Marshal(done.Payload)
	var prog contracts.STTInstallProgressDTO
	if err := json.Unmarshal(raw, &prog); err != nil || prog.ModelID != "ggml-small" {
		t.Fatalf("done payload missing model id: %s (%v)", raw, err)
	}
	waitForCondition(t, func() bool { return !app.sttInstallRunning() }, "install slot must release")
}

func TestSTTInstallErrorEventOnFailure(t *testing.T) {
	inst := &fakeSTTInstaller{failWith: errors.New("disk full")}
	app := sttInstallApp(inst)
	events := collectEvents(t, app)

	if _, rpcErr := app.handleSTTSettingsInstallStart(contracts.STTInstallStartRequest{ModelID: "ggml-tiny"}); rpcErr != nil {
		t.Fatalf("start: %v", rpcErr)
	}
	errEv := waitForEvent(t, events, contracts.EventSTTInstallError)
	rawErr, _ := json.Marshal(errEv.Payload)
	var prog contracts.STTInstallProgressDTO
	if err := json.Unmarshal(rawErr, &prog); err != nil {
		t.Fatalf("error payload decode: %v (%s)", err, rawErr)
	}
	if !strings.Contains(prog.Message, "disk full") {
		t.Errorf("error payload message = %q", prog.Message)
	}
	waitForCondition(t, func() bool { return !app.sttInstallRunning() }, "failed install must release the slot")
}

func TestSTTInstallSingleFlight(t *testing.T) {
	inst := &fakeSTTInstaller{block: make(chan struct{})}
	app := sttInstallApp(inst)

	res, rpcErr := app.handleSTTSettingsInstallStart(contracts.STTInstallStartRequest{ModelID: "ggml-small"})
	if rpcErr != nil || !res.(contracts.STTInstallStartResult).Started {
		t.Fatalf("first start: %v %v", res, rpcErr)
	}
	res2, _ := app.handleSTTSettingsInstallStart(contracts.STTInstallStartRequest{ModelID: "ggml-tiny"})
	r2 := res2.(contracts.STTInstallStartResult)
	if r2.Started || !r2.Running {
		t.Errorf("second start during flight = %+v, want Started:false Running:true", r2)
	}
	close(inst.block)
	waitForCondition(t, func() bool { return !app.sttInstallRunning() }, "slot release after unblock")
}

func TestSTTInstallCancelStopsAndReleases(t *testing.T) {
	inst := &fakeSTTInstaller{block: make(chan struct{})}
	app := sttInstallApp(inst)

	if _, rpcErr := app.handleSTTSettingsInstallStart(contracts.STTInstallStartRequest{ModelID: "ggml-small"}); rpcErr != nil {
		t.Fatalf("start: %v", rpcErr)
	}
	out, rpcErr := app.handleSTTSettingsInstallCancel()
	if rpcErr != nil {
		t.Fatalf("cancel: %v", rpcErr)
	}
	if res := out.(contracts.STTInstallStartResult); res.Running {
		t.Errorf("cancel result = %+v, want Running:false", res)
	}
	waitForCondition(t, func() bool { return !app.sttInstallRunning() }, "cancel must release the slot")

	// Slot is free again: a fresh install can start and complete.
	inst.block = nil
	res, rpcErr := app.handleSTTSettingsInstallStart(contracts.STTInstallStartRequest{ModelID: "ggml-base"})
	if rpcErr != nil || !res.(contracts.STTInstallStartResult).Started {
		t.Fatalf("restart after cancel: %v %v", res, rpcErr)
	}
	waitForCondition(t, func() bool { return !app.sttInstallRunning() }, "second install must finish")
}

func TestSTTInstallCancelWithoutRunIsNoop(t *testing.T) {
	app := sttInstallApp(&fakeSTTInstaller{})
	out, rpcErr := app.handleSTTSettingsInstallCancel()
	if rpcErr != nil {
		t.Fatalf("cancel noop: %v", rpcErr)
	}
	if out.(contracts.STTInstallStartResult).Running {
		t.Error("noop cancel must not report running")
	}
}

// compile-time interface check mirrors the TTS port contract.
var _ STTInstaller = (*fakeSTTInstaller)(nil)

// --- from speech_generate_test.go ---

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
	online := &fakeOnlineTTS{err: ttserr.ErrTTS("HTTP 503 down")}
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

// --- from offline_tts_models_test.go ---

// TestHandleModelsListIncludesInstalledOfflineVoices verifies that piper
// voices installed via the one-click installer surface in the Settings
// model picker (provider "piper", tts=true) — the user story: after the
// download dialog finishes, the voice appears in the picker.
func TestHandleModelsListIncludesInstalledOfflineVoices(t *testing.T) {
	inst := &fakeTTSInstaller{status: contracts.TTSInstallStatusResult{
		BinaryInstalled: true,
		Voices: []contracts.TTSVoiceDTO{
			{ID: "id_ID-news_tts-medium", Label: "Bahasa Indonesia — news_tts (medium)", Installed: true},
			{ID: "en_US-lessac-high", Label: "English (US) — lessac (high)", Installed: false},
		},
		Ready: true,
	}}
	app := &App{Providers: &fakeProviderStore{items: map[string]*domain.Provider{}}, TTSInstaller: inst, Logs: &fakeLogStore{}}

	res, rpcErr := app.handleModelsList()
	if rpcErr != nil {
		t.Fatalf("handleModelsList: %v", rpcErr.Message)
	}
	var found *contracts.ModelDTO
	for i := range res.(contracts.ModelsListResult).Models {
		m := res.(contracts.ModelsListResult).Models[i]
		if m.ID == "id_ID-news_tts-medium" {
			found = &m
		}
		if m.ID == "en_US-lessac-high" {
			t.Error("uninstalled voice must not appear in the picker")
		}
	}
	if found == nil {
		t.Fatal("installed offline voice missing from models list")
	}
	if found.ProviderID != OfflineTTSProviderID {
		t.Errorf("provider_id = %q, want %q", found.ProviderID, OfflineTTSProviderID)
	}
	if !found.TTS || found.Kind != string(domain.ModelKindTTS) {
		t.Errorf("voice must be tagged as a TTS model: %+v", found)
	}
	if found.DisplayName == "" {
		t.Error("voice must carry its human-readable label")
	}
}

// TestHandleModelsListWithoutInstaller verifies a nil installer (not wired
// in this build) never breaks the models list.
func TestHandleModelsListWithoutInstaller(t *testing.T) {
	app := &App{Providers: &fakeProviderStore{items: map[string]*domain.Provider{}}, Logs: &fakeLogStore{}}
	res, rpcErr := app.handleModelsList()
	if rpcErr != nil {
		t.Fatalf("handleModelsList: %v", rpcErr.Message)
	}
	if got := len(res.(contracts.ModelsListResult).Models); got != 0 {
		t.Fatalf("expected 0 models, got %d", got)
	}
}

// TestGenerateSpeechExplicitOfflineVoice verifies that selecting an
// installed piper voice in Settings (provider "piper") routes
// generate_speech straight to the local engine with the chosen voice —
// the online route is not consulted.
func TestGenerateSpeechExplicitOfflineVoice(t *testing.T) {
	offline := &fakeOfflineTTS{available: true, result: &TTSResult{Audio: wavMagic(), MediaType: "audio/wav", Ext: "wav", Provider: "piper", Model: "id_ID-news_tts-medium"}}
	app := ttsApp(t, nil, offline)
	app.Settings = &fakeSettingsTTS{settings: domain.Settings{TTSProviderID: OfflineTTSProviderID, TTSModelID: "id_ID-news_tts-medium"}}
	run, cancel := ttsRun(t)
	defer cancel()

	out, atts, err := app.executeGenerateSpeech(run, domain.ToolCall{ID: "tc1", Args: `{"text":"halo"}`}, app.Settings.Get())
	if err != nil {
		t.Fatalf("executeGenerateSpeech: %v", err)
	}
	if len(atts) == 0 || !strings.Contains(out, "Speech generated") {
		t.Fatalf("expected generated speech, got %q", out)
	}
	if offline.got.Voice != "id_ID-news_tts-medium" {
		t.Errorf("offline voice = %q, want the settings voice", offline.got.Voice)
	}
}

// TestGenerateSpeechExplicitOfflineVoiceUnavailable verifies a clear error
// (and no online attempt) when the picked offline voice is gone.
func TestGenerateSpeechExplicitOfflineVoiceUnavailable(t *testing.T) {
	app := ttsApp(t, nil, nil) // no offline engine wired
	app.Settings = &fakeSettingsTTS{settings: domain.Settings{TTSProviderID: OfflineTTSProviderID, TTSModelID: "id_ID-news_tts-medium"}}
	run, cancel := ttsRun(t)
	defer cancel()

	out, _, err := app.executeGenerateSpeech(run, domain.ToolCall{ID: "tc1", Args: `{"text":"halo"}`}, app.Settings.Get())
	if err == nil {
		t.Fatal("expected error when the picked offline voice is unavailable")
	}
	if !strings.Contains(out, "offline piper voice is not available") {
		t.Errorf("error should point at the missing voice: %q", out)
	}
}
