package application

import (
	"strings"
	"testing"

	"nusashell/contracts"
	"nusashell/domain"
)

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
