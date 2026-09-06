package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"nusashell/domain"
	"path/filepath"
	"strings"
	"testing"
)

// --- from audio_stt_test.go ---

// TestExecuteReadAudioRoutesSTTKindToTranscriptions verifies end to end:
// when the configured audio fallback model is kind "stt", read_media must
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
	toolCall := domain.ToolCall{ID: "tc1", Name: "read_media", Args: filePathArgs(audioPath, "")}

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
	toolCall := domain.ToolCall{ID: "tc1", Name: "read_media", Args: filePathArgs(audioPath, "")}

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
	body.WriteString("--x\r\nContent-Disposition: form-data; name=\"model\"\r\n\r\n")
	body.WriteString(req.Model)
	body.WriteString("\r\n")
	body.WriteString("--x\r\nContent-Disposition: form-data; name=\"file\"; filename=\"")
	body.WriteString(req.Filename)
	body.WriteString("\"\r\n\r\n")
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

// --- from audio_read_audio_test.go ---

func TestExecuteReadAudioNative(t *testing.T) {
	dir := t.TempDir()
	audioPath := writeTestFile(t, dir, "recording.mp3")
	app := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	toolCall := domain.ToolCall{
		ID:   "tc1",
		Name: "read_media",
		Args: filePathArgs(audioPath, "what is being said?"),
	}

	output, atts, err := app.executeReadAudio(run, toolCall, ModelCapabilities{Audio: true}, domain.Settings{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(output, "Audio loaded") {
		t.Errorf("output should mention audio loaded, got: %q", output)
	}
	// Question echo removed from native audio output (model already knows).
	if strings.Contains(output, "what is being said") {
		t.Errorf("audio output should NOT echo the question, got: %q", output)
	}
	if len(atts) != 1 {
		t.Fatalf("expected 1 attachment (audio), got %d", len(atts))
	}
	if atts[0].Type != "audio" || atts[0].Name != "recording.mp3" {
		t.Errorf("attachment should be the audio, got %q %q", atts[0].Type, atts[0].Name)
	}
	if atts[0].MediaType != "audio/mpeg" || atts[0].DataURL == "" {
		t.Errorf("attachment should be inline mpeg audio, got %q url=%v", atts[0].MediaType, atts[0].DataURL != "")
	}
}

func TestExecuteReadAudioNonAudioNoFallback(t *testing.T) {
	dir := t.TempDir()
	audioPath := writeTestFile(t, dir, "recording.mp3")
	app := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	toolCall := domain.ToolCall{
		ID:   "tc1",
		Name: "read_media",
		Args: filePathArgs(audioPath, ""),
	}

	output, atts, err := app.executeReadAudio(run, toolCall, ModelCapabilities{}, domain.Settings{})
	if err != nil {
		t.Fatalf("expected graceful error message, not Go error: %v", err)
	}
	if !strings.Contains(output, "does not support audio input") {
		t.Errorf("output should explain no audio support, got: %q", output)
	}
	if len(atts) != 0 {
		t.Errorf("expected 0 attachments, got %d", len(atts))
	}
}

func TestExecuteReadAudioAudioNotFound(t *testing.T) {
	dir := t.TempDir()
	app := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	toolCall := domain.ToolCall{
		ID:   "tc1",
		Name: "read_media",
		Args: filePathArgs(testAbsPath(dir, "nonexistent.mp3"), ""),
	}

	output, _, err := app.executeReadAudio(run, toolCall, ModelCapabilities{Audio: true}, domain.Settings{})
	if err == nil {
		t.Error("expected error for nonexistent audio")
	}
	if !strings.Contains(output, "not found") {
		t.Errorf("output should mention not found, got: %q", output)
	}
}

func TestExecuteReadAudioMissingArgs(t *testing.T) {
	app := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	toolCall := domain.ToolCall{
		ID:   "tc1",
		Name: "read_media",
		Args: `{}`,
	}

	output, _, err := app.executeReadAudio(run, toolCall, ModelCapabilities{Audio: true}, domain.Settings{})
	if err == nil {
		t.Error("expected error for missing file_path")
	}
	if !strings.Contains(output, "file_path is required") {
		t.Errorf("output should mention missing arg, got: %q", output)
	}
}

func TestExecuteReadAudioRejectsRelativePath(t *testing.T) {
	app := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	toolCall := domain.ToolCall{
		ID:   "tc1",
		Name: "read_media",
		Args: `{"file_path":"recording.mp3"}`,
	}

	output, _, err := app.executeReadAudio(run, toolCall, ModelCapabilities{Audio: true}, domain.Settings{})
	if err == nil {
		t.Error("expected error for relative path")
	}
	if !strings.Contains(output, "absolute") {
		t.Errorf("output should mention absolute path required, got: %q", output)
	}
}

func TestDescribeAudiosWithFallbackSkipsExistingTranscript(t *testing.T) {
	adapter := &fakeVisionAdapter{description: "should not run"}
	var factoryCalls int
	app := visionFallbackTestApp(adapter, &factoryCalls)
	settings := domain.Settings{AudioProviderID: "vision-prov", AudioModelID: "gpt-4o"}
	atts := []domain.Attachment{
		{Type: "audio", Name: "clip.mp3", MediaType: "audio/mpeg", DataURL: "data:audio/mpeg;base64,AAA="},
		{Type: "text", Name: "audio:clip.mp3", MediaType: "text/plain", Content: "[Audio transcript for clip.mp3]\nexisting"},
	}
	out := app.describeAudiosWithFallback(context.Background(), settings, atts)
	if len(out) != 2 {
		t.Fatalf("expected unchanged attachments, got %d", len(out))
	}
	if factoryCalls != 0 || adapter.calls != 0 {
		t.Fatalf("audio fallback called factory=%d chat=%d, want 0/0", factoryCalls, adapter.calls)
	}
}

func TestUndescribedMediaIndexes(t *testing.T) {
	atts := []domain.Attachment{
		{Type: "image", Name: "a.png"},
		{Type: "text", Name: "vision:a.png"},
		{Type: "image", Name: "b.png"},
		{Type: "audio", Name: "c.mp3"},
	}
	got := domain.UndescribedMediaIndexes(atts, "image", mediaDescPrefixVision)
	if len(got) != 1 || got[0] != 2 {
		t.Fatalf("undescribed images = %v, want [2]", got)
	}
	if n := domain.UndescribedMediaIndexes(atts, "audio", mediaDescPrefixAudio); len(n) != 1 || n[0] != 3 {
		t.Fatalf("undescribed audio = %v, want [3]", n)
	}
	if n := domain.UndescribedMediaIndexes(nil, "image", mediaDescPrefixVision); len(n) != 0 {
		t.Fatalf("empty attachments = %v, want []", n)
	}
}

// --- from audio_offline_fallback_test.go ---

// TestOfflineFillsGap pins the zero-config UX: with NO audio fallback
// configured at all, read_media still succeeds via the local engine (when
// available) instead of dead-ending, and discloses route=offline.
func TestOfflineFillsGap(t *testing.T) {
	s := newSttHarness(t)
	s.app.Settings = &fakeSettingsStore{} // AudioProviderID empty
	s.offline.text = "transkrip lokal"

	out, _, err := s.app.executeReadAudio(s.run, s.toolCall, ModelCapabilities{}, s.app.Settings.Get())
	if err != nil {
		t.Fatalf("offline fallback should serve, got error: %v", err)
	}
	if !strings.Contains(out, "transkrip lokal") {
		t.Errorf("expected offline transcript, got %q", out)
	}
	if !strings.Contains(out, "route: offline") {
		t.Errorf("meta should disclose route=offline, got %q", out)
	}
}

// TestCloudBeatsOffline pins explicit-over-implicit: when the user HAS
// configured an stt-kind audio fallback, it is used even if the local
// engine is also available.
func TestCloudBeatsOffline(t *testing.T) {
	s := newSttHarness(t)
	s.app.Settings = &fakeSettingsStore{settings: domain.Settings{AudioProviderID: "p1", AudioModelID: "m"}}
	s.cloud.text = "transkrip awan"
	s.offline.text = "transkrip lokal"

	out, _, err := s.app.executeReadAudio(s.run, s.toolCall, ModelCapabilities{}, s.app.Settings.Get())
	if err != nil {
		t.Fatalf("cloud route should succeed: %v", err)
	}
	if !strings.Contains(out, "transkrip awan") || strings.Contains(out, "transkrip lokal") {
		t.Errorf("cloud transcript should win, got %q", out)
	}
}

// TestCloudFailureDegradesToOffline: a broken/misconfigured cloud provider
// (403 etc.) must not dead-end the tool — fall through to offline and log.
func TestCloudFailureDegradesToOffline(t *testing.T) {
	s := newSttHarness(t)
	s.app.Settings = &fakeSettingsStore{settings: domain.Settings{AudioProviderID: "p1", AudioModelID: "m"}}
	s.cloud.err = errors.New("HTTP 403: forbidden")
	s.offline.text = "transkrip lokal"

	out, _, err := s.app.executeReadAudio(s.run, s.toolCall, ModelCapabilities{}, s.app.Settings.Get())
	if err != nil {
		t.Fatalf("should degrade to offline, got error: %v", err)
	}
	if !strings.Contains(out, "transkrip lokal") {
		t.Errorf("offline should serve after cloud failure, got %q", out)
	}
}

// TestNoRoutesAtAllKeepsOldMessage: neither cloud nor offline available →
// the original helpful message stays.
func TestNoRoutesAtAllKeepsOldMessage(t *testing.T) {
	s := newSttHarness(t)
	s.app.Settings = &fakeSettingsStore{}
	s.offline.available = false
	s.offline.reason = "not built"

	out, _, err := s.app.executeReadAudio(s.run, s.toolCall, ModelCapabilities{}, s.app.Settings.Get())
	if err != nil {
		t.Fatalf("graceful message expected, got Go error: %v", err)
	}
	if !strings.Contains(out, "does not support audio input") {
		t.Errorf("legacy guidance missing, got %q", out)
	}
}

// --- from audio_offline_fallback_harness_test.go ---

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
