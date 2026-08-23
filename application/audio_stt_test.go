package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"nusashell/domain"
)

// TestAudioFallbackRoute pins the by-kind routing rule: catalog models of
// kind "stt" are served ONLY by the dedicated /audio/transcriptions
// endpoint (probe-verified: chat input_audio and Responses both reject
// them), while every other kind keeps the multimodal chat fallback.
func TestAudioFallbackRoute(t *testing.T) {
	sttProvider := &domain.Provider{Models: []domain.Model{{ID: "whisper-x", Kind: domain.ModelKindSTT}}}
	if got := audioFallbackRoute(sttProvider, "whisper-x"); got != audioRouteTranscriptions {
		t.Errorf("stt kind should route to transcriptions, got %q", got)
	}

	chatProvider := &domain.Provider{Models: []domain.Model{{ID: "gem", Kind: domain.ModelKindChat}}}
	if got := audioFallbackRoute(chatProvider, "gem"); got != audioRouteChat {
		t.Errorf("chat kind should route to chat, got %q", got)
	}

	if got := audioFallbackRoute(&domain.Provider{}, "unknown-model"); got != audioRouteChat {
		t.Errorf("unknown model should default to chat route, got %q", got)
	}
}

// TestExecuteReadAudioRoutesSTTKindToTranscriptions verifies end to end:
// when the configured audio fallback model is kind "stt", read_audio must
// send a multipart POST to <base>/audio/transcriptions carrying the model
// name and audio file, then return the transcript text.
func TestExecuteReadAudioRoutesSTTKindToTranscriptions(t *testing.T) {
	var gotPath, gotAuth, gotModel string
	var gotFile []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			t.Errorf("multipart parse: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		gotModel = r.FormValue("model")
		f, _, err := r.FormFile("file")
		if err != nil {
			t.Errorf("file field missing: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer f.Close()
		data, _ := io.ReadAll(f)
		gotFile = data
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"text": "Paragraf pertama. Hari ini kita membedah dua proyek agen sumber terbuka.",
		})
	}))
	defer server.Close()

	dir := t.TempDir()
	audioPath := writeTestFile(t, dir, "recording.mp3")

	app := &App{
		Providers: &fakeProviderStore{items: map[string]*domain.Provider{
			"sttprov": {
				ID: "sttprov", Kind: domain.ProviderChat, Enabled: true,
				BaseURL: server.URL + "/v1",
				Models:  []domain.Model{{ID: "whisper-test", Kind: domain.ModelKindSTT}},
			},
		}},
		Credentials: &memCreds{m: map[string]string{"sttprov": "sk-stt-key"}},
		SpeechTranscriberFactory: func(p *domain.Provider, apiKey string) (SpeechTranscriber, error) {
			return &fakeSpeechTranscriber{base: p.BaseURL, key: apiKey}, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	toolCall := domain.ToolCall{ID: "tc1", Name: "read_audio", Args: filePathArgs(audioPath, "")}

	output, atts, err := app.executeReadAudio(run, toolCall, ModelCapabilities{}, domain.Settings{
		AudioProviderID: "sttprov", AudioModelID: "whisper-test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotPath != "/v1/audio/transcriptions" {
		t.Errorf("should POST to /v1/audio/transcriptions, got %q", gotPath)
	}
	if gotAuth != "Bearer sk-stt-key" {
		t.Errorf("authorization = %q", gotAuth)
	}
	if gotModel != "whisper-test" {
		t.Errorf("model form value = %q", gotModel)
	}
	if !bytes.HasPrefix(gotFile, []byte("ID3")) {
		t.Error("uploaded file should carry the audio magic bytes (ID3)")
	}
	if !strings.Contains(output, "[Audio transcript for recording.mp3]") || !strings.Contains(output, "Paragraf pertama") {
		t.Errorf("output should contain transcript, got: %q", output)
	}
	if len(atts) != 0 {
		t.Errorf("STT route returns text only, got %d attachments", len(atts))
	}
}

// TestExecuteReadAudioSTTWithoutFactory verifies a clear error when the
// binary was built without the STT wiring but the user picked an stt model.
func TestExecuteReadAudioSTTWithoutFactory(t *testing.T) {
	dir := t.TempDir()
	audioPath := writeTestFile(t, dir, "recording.mp3")
	app := &App{
		Providers: &fakeProviderStore{items: map[string]*domain.Provider{
			"sttprov": {
				ID: "sttprov", Kind: domain.ProviderChat, Enabled: true,
				BaseURL: "https://api.example.com/v1",
				Models:  []domain.Model{{ID: "whisper-test", Kind: domain.ModelKindSTT}},
			},
		}},
		Credentials: &memCreds{m: map[string]string{"sttprov": "sk"}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	toolCall := domain.ToolCall{ID: "tc1", Name: "read_audio", Args: filePathArgs(audioPath, "")}

	output, _, err := app.executeReadAudio(run, toolCall, ModelCapabilities{}, domain.Settings{
		AudioProviderID: "sttprov", AudioModelID: "whisper-test",
	})
	if err == nil {
		t.Fatal("expected error when STT factory is unavailable")
	}
	if !strings.Contains(output, "speech-transcription-only") || !strings.Contains(output, "audio-capable chat model") {
		t.Errorf("error should explain STT unavailability, got: %q", output)
	}
	_ = filepath.Base
}

// fakeSpeechTranscriber is a minimal SpeechTranscriber backed by the
// httptest server above; production builds get infrastructure/ai/stt.Client
// via NewSpeechTranscriberFactory.
type fakeSpeechTranscriber struct {
	base string
	key  string
}

func (f *fakeSpeechTranscriber) Transcribe(ctx context.Context, req STTRequest) (string, error) {
	body := &strings.Builder{}
	body.WriteString("--x\r\nContent-Disposition: form-data; name=\"model\"\r\n\r\n" + req.Model + "\r\n")
	body.WriteString("--x\r\nContent-Disposition: form-data; name=\"file\"; filename=\"" + req.Filename + "\"\r\n\r\n")
	body.Write(req.Data)
	body.WriteString("\r\n--x--\r\n")
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, f.base+"/audio/transcriptions", strings.NewReader(body.String()))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Authorization", "Bearer "+f.key)
	httpReq.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.Text, nil
}
