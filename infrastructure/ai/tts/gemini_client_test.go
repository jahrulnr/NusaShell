package tts

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"nusashell/application"
	"nusashell/domain"
)

// rawPCM is 4 bytes of 16-bit mono PCM silence at 24kHz (2 samples).
var rawPCM = []byte{0x00, 0x00, 0x00, 0x00}

// wavFromPCM builds a WAV container around pcm for test assertions.
func wavFromPCM(pcm []byte, sampleRate, bits, channels int) []byte {
	return pcmToWAV(pcm, sampleRate, bits, channels)
}

func geminiTTSMock(t *testing.T, captured *map[string]any, respBody map[string]any, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, ":generateContent") {
			t.Errorf("unexpected path %q, want :generateContent suffix", r.URL.Path)
		}
		if r.Header.Get("x-goog-api-key") == "" {
			t.Error("missing x-goog-api-key header")
		}
		if r.Header.Get("Authorization") != "" {
			t.Errorf("Gemini must not use Authorization; got %q", r.Header.Get("Authorization"))
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, captured); err != nil {
			t.Fatal(err)
		}
		if status != 0 {
			w.WriteHeader(status)
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		_ = json.NewEncoder(w).Encode(respBody)
	}))
	return srv
}

func TestGeminiTTSSendsGenerateContentBody(t *testing.T) {
	var gotBody map[string]any
	srv := geminiTTSMock(t, &gotBody, map[string]any{
		"candidates": []map[string]any{{
			"content": map[string]any{
				"parts": []map[string]any{{
					"inlineData": map[string]any{
						"mimeType": "audio/L16;rate=24000",
						"data":     base64.StdEncoding.EncodeToString(rawPCM),
					},
				}},
			},
		}},
	}, 0)
	defer srv.Close()

	c := &GeminiClient{BaseURL: srv.URL + "/v1beta", APIKey: "gem-key", HTTP: srv.Client()}
	res, err := c.Synthesize(context.Background(), application.TTSRequest{
		Model: "gemini-3.1-flash-tts-preview", Text: "halo dunia", Voice: "Kore",
	})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	// Verify request body shape.
	contents, _ := gotBody["contents"].([]any)
	if len(contents) != 1 {
		t.Fatalf("contents = %+v", gotBody["contents"])
	}
	first, _ := contents[0].(map[string]any)
	if first["role"] != "user" {
		t.Errorf("role = %v, want user", first["role"])
	}
	parts, _ := first["parts"].([]any)
	if len(parts) != 1 {
		t.Fatalf("parts = %+v", parts)
	}
	textPart, _ := parts[0].(map[string]any)
	if textPart["text"] != "halo dunia" {
		t.Errorf("text = %v, want halo dunia", textPart["text"])
	}
	genCfg, _ := gotBody["generationConfig"].(map[string]any)
	if genCfg == nil {
		t.Fatal("missing generationConfig")
	}
	mods, _ := genCfg["responseModalities"].([]any)
	if len(mods) != 1 || mods[0] != "AUDIO" {
		t.Fatalf("responseModalities = %+v, want [AUDIO]", mods)
	}
	speechCfg, _ := genCfg["speechConfig"].(map[string]any)
	if speechCfg == nil {
		t.Fatal("missing speechConfig")
	}
	voiceCfg, _ := speechCfg["voiceConfig"].(map[string]any)
	if voiceCfg == nil {
		t.Fatal("missing voiceConfig")
	}
	prebuilt, _ := voiceCfg["prebuiltVoiceConfig"].(map[string]any)
	if prebuilt == nil {
		t.Fatal("missing prebuiltVoiceConfig")
	}
	if prebuilt["voiceName"] != "Kore" {
		t.Errorf("voiceName = %v, want Kore", prebuilt["voiceName"])
	}
	// Verify result.
	if res.Provider != "gemini" || res.Model != "gemini-3.1-flash-tts-preview" {
		t.Errorf("result = %+v", res)
	}
	if res.Ext != "wav" || res.MediaType != "audio/wav" {
		t.Errorf("mediaType/ext = %q/%q, want audio/wav/wav", res.MediaType, res.Ext)
	}
	// PCM must be wrapped into a WAV container (RIFF header).
	if len(res.Audio) < 44 || string(res.Audio[:4]) != "RIFF" {
		t.Fatalf("audio not wrapped in WAV: % x", res.Audio[:min(8, len(res.Audio))])
	}
}

func TestGeminiTTSDefaultVoiceWhenEmpty(t *testing.T) {
	var gotBody map[string]any
	srv := geminiTTSMock(t, &gotBody, map[string]any{
		"candidates": []map[string]any{{
			"content": map[string]any{
				"parts": []map[string]any{{
					"inlineData": map[string]any{
						"mimeType": "audio/L16;rate=24000",
						"data":     base64.StdEncoding.EncodeToString(rawPCM),
					},
				}},
			},
		}},
	}, 0)
	defer srv.Close()

	c := &GeminiClient{BaseURL: srv.URL + "/v1beta", APIKey: "gem-key", HTTP: srv.Client()}
	res, err := c.Synthesize(context.Background(), application.TTSRequest{
		Model: "gemini-3.1-flash-tts-preview", Text: "hello",
	})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if res.Voice != DefaultGeminiTTSVoice {
		t.Errorf("voice = %q, want %q", res.Voice, DefaultGeminiTTSVoice)
	}
	genCfg, _ := gotBody["generationConfig"].(map[string]any)
	speechCfg, _ := genCfg["speechConfig"].(map[string]any)
	voiceCfg, _ := speechCfg["voiceConfig"].(map[string]any)
	prebuilt, _ := voiceCfg["prebuiltVoiceConfig"].(map[string]any)
	if prebuilt["voiceName"] != DefaultGeminiTTSVoice {
		t.Errorf("voiceName = %v, want %s", prebuilt["voiceName"], DefaultGeminiTTSVoice)
	}
}

func TestGeminiTTSPassesThroughWAV(t *testing.T) {
	wavBytes := wavFromPCM(rawPCM, 24000, 16, 1)
	srv := geminiTTSMock(t, &map[string]any{}, map[string]any{
		"candidates": []map[string]any{{
			"content": map[string]any{
				"parts": []map[string]any{{
					"inlineData": map[string]any{
						"mimeType": "audio/wav",
						"data":     base64.StdEncoding.EncodeToString(wavBytes),
					},
				}},
			},
		}},
	}, 0)
	defer srv.Close()

	c := &GeminiClient{BaseURL: srv.URL + "/v1beta", APIKey: "gem-key", HTTP: srv.Client()}
	res, err := c.Synthesize(context.Background(), application.TTSRequest{
		Model: "gemini-2.5-flash-preview-tts", Text: "test", Voice: "Puck",
	})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if string(res.Audio) != string(wavBytes) {
		t.Fatal("WAV pass-through mismatch")
	}
	if res.MediaType != "audio/wav" || res.Ext != "wav" {
		t.Errorf("mediaType/ext = %q/%q", res.MediaType, res.Ext)
	}
}

func TestGeminiTTSPCMWrappedToWAVWithCorrectHeader(t *testing.T) {
	srv := geminiTTSMock(t, &map[string]any{}, map[string]any{
		"candidates": []map[string]any{{
			"content": map[string]any{
				"parts": []map[string]any{{
					"inlineData": map[string]any{
						"mimeType": "audio/L16;rate=24000",
						"data":     base64.StdEncoding.EncodeToString(rawPCM),
					},
				}},
			},
		}},
	}, 0)
	defer srv.Close()

	c := &GeminiClient{BaseURL: srv.URL + "/v1beta", APIKey: "gem-key", HTTP: srv.Client()}
	res, err := c.Synthesize(context.Background(), application.TTSRequest{
		Model: "gemini-3.1-flash-tts-preview", Text: "test",
	})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	wantWAV := wavFromPCM(rawPCM, 24000, 16, 1)
	if string(res.Audio) != string(wantWAV) {
		t.Errorf("PCM wrap mismatch:\n got % x\n want % x", res.Audio, wantWAV)
	}
	// Verify WAV fmt chunk: PCM(1), mono, 24000Hz, 16-bit.
	if string(res.Audio[8:12]) != "WAVE" {
		t.Fatal("missing WAVE tag")
	}
	if binary.LittleEndian.Uint16(res.Audio[20:22]) != 1 {
		t.Error("not PCM format")
	}
	if binary.LittleEndian.Uint16(res.Audio[22:24]) != 1 {
		t.Error("not mono")
	}
	if binary.LittleEndian.Uint32(res.Audio[24:28]) != 24000 {
		t.Error("sample rate not 24000")
	}
	if binary.LittleEndian.Uint16(res.Audio[34:36]) != 16 {
		t.Error("bits per sample not 16")
	}
}

func TestGeminiTTSRequestedFormatIgnoredReturnsWAV(t *testing.T) {
	// Gemini TTS cannot produce mp3/opus — the artifact is always WAV.
	for _, fmtReq := range []string{"mp3", "wav", "opus"} {
		t.Run(fmtReq, func(t *testing.T) {
			srv := geminiTTSMock(t, &map[string]any{}, map[string]any{
				"candidates": []map[string]any{{
					"content": map[string]any{
						"parts": []map[string]any{{
							"inlineData": map[string]any{
								"mimeType": "audio/L16;rate=24000",
								"data":     base64.StdEncoding.EncodeToString(rawPCM),
							},
						}},
					},
				}},
			}, 0)
			defer srv.Close()

			c := &GeminiClient{BaseURL: srv.URL + "/v1beta", APIKey: "gem-key", HTTP: srv.Client()}
			res, err := c.Synthesize(context.Background(), application.TTSRequest{
				Model: "gemini-3.1-flash-tts-preview", Text: "test", Format: fmtReq,
			})
			if err != nil {
				t.Fatalf("Synthesize(%s): %v", fmtReq, err)
			}
			if res.Ext != "wav" || res.MediaType != "audio/wav" {
				t.Errorf("format %q: got ext=%q mediaType=%q, want wav/audio/wav", fmtReq, res.Ext, res.MediaType)
			}
		})
	}
}

func TestGeminiTTSErrorMapsToProviderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":400,"message":"Invalid value at 'generationConfig.responseModalities[0]'","status":"INVALID_ARGUMENT"}}`))
	}))
	defer srv.Close()

	c := &GeminiClient{BaseURL: srv.URL + "/v1beta", APIKey: "gem-key", HTTP: srv.Client()}
	_, err := c.Synthesize(context.Background(), application.TTSRequest{
		Model: "gemini-2.5-pro-preview-tts", Text: "test",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	var perr *domain.ProviderError
	if !errors.As(err, &perr) {
		t.Fatalf("err = %T, want *domain.ProviderError", err)
	}
	if perr.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", perr.StatusCode)
	}
	if !strings.Contains(perr.Error(), "Invalid value") {
		t.Fatalf("err = %v, want provider message verbatim", perr)
	}
}

func TestGeminiTTS429IsHardFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":429,"message":"quota exceeded","status":"RESOURCE_EXHAUSTED"}}`))
	}))
	defer srv.Close()

	c := &GeminiClient{BaseURL: srv.URL + "/v1beta", APIKey: "gem-key", HTTP: srv.Client()}
	_, err := c.Synthesize(context.Background(), application.TTSRequest{
		Model: "gemini-3.1-flash-tts-preview", Text: "test",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	var perr *domain.ProviderError
	if !errors.As(err, &perr) {
		t.Fatalf("err = %T, want *domain.ProviderError", err)
	}
	if perr.StatusCode != 429 {
		t.Fatalf("status = %d, want 429", perr.StatusCode)
	}
	if perr.Temporary {
		t.Error("429 must be a hard failure, not retriable")
	}
}

func TestGeminiTTS5xxIsRetriable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"code":502,"message":"bad gateway","status":"UNAVAILABLE"}}`))
	}))
	defer srv.Close()

	c := &GeminiClient{BaseURL: srv.URL + "/v1beta", APIKey: "gem-key", HTTP: srv.Client()}
	_, err := c.Synthesize(context.Background(), application.TTSRequest{
		Model: "gemini-3.1-flash-tts-preview", Text: "test",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	var perr *domain.ProviderError
	if !errors.As(err, &perr) {
		t.Fatalf("err = %T, want *domain.ProviderError", err)
	}
	if perr.StatusCode != 502 {
		t.Fatalf("status = %d, want 502", perr.StatusCode)
	}
	if !perr.Temporary {
		t.Error("5xx must be retriable")
	}
}

func TestGeminiTTSNoAudioFails(t *testing.T) {
	srv := geminiTTSMock(t, &map[string]any{}, map[string]any{
		"candidates": []map[string]any{{
			"content": map[string]any{
				"parts": []map[string]any{{"text": "I can only generate text"}},
			},
		}},
	}, 0)
	defer srv.Close()

	c := &GeminiClient{BaseURL: srv.URL + "/v1beta", APIKey: "gem-key", HTTP: srv.Client()}
	_, err := c.Synthesize(context.Background(), application.TTSRequest{
		Model: "gemini-3.1-flash-tts-preview", Text: "test",
	})
	if err == nil || !strings.Contains(err.Error(), "no audio") {
		t.Fatalf("err = %v, want no-audio error", err)
	}
}

func TestGeminiTTSNilModelRejected(t *testing.T) {
	c := &GeminiClient{BaseURL: "https://example.test/v1beta", APIKey: "k"}
	_, err := c.Synthesize(context.Background(), application.TTSRequest{Text: "test"})
	if err == nil || !strings.Contains(err.Error(), "model") {
		t.Fatalf("err = %v, want model-required error", err)
	}
}

func TestGeminiTTSEmptyTextRejected(t *testing.T) {
	c := &GeminiClient{BaseURL: "https://example.test/v1beta", APIKey: "k"}
	_, err := c.Synthesize(context.Background(), application.TTSRequest{Model: "gemini-3.1-flash-tts-preview"})
	if err == nil || !strings.Contains(err.Error(), "text") {
		t.Fatalf("err = %v, want text-required error", err)
	}
}

func TestGeminiTTSModelIDNormalization(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{
					"parts": []map[string]any{{
						"inlineData": map[string]any{
							"mimeType": "audio/L16;rate=24000",
							"data":     base64.StdEncoding.EncodeToString(rawPCM),
						},
					}},
				},
			}},
		})
	}))
	defer srv.Close()

	c := &GeminiClient{BaseURL: srv.URL + "/v1beta", APIKey: "gem-key", HTTP: srv.Client()}
	_, err := c.Synthesize(context.Background(), application.TTSRequest{
		Model: "google/gemini-3.1-flash-tts-preview", Text: "test",
	})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/models/gemini-3.1-flash-tts-preview:generateContent") {
		t.Errorf("path = %q, want bare model id after stripping prefix", gotPath)
	}
}

func TestParsePCMMimeType(t *testing.T) {
	cases := []struct {
		mime                      string
		wantRate, wantBit, wantCh int
	}{
		{"audio/L16;rate=24000", 24000, 0, 0},
		{"audio/L16;rate=24000;bit=16;channels=1", 24000, 16, 1},
		{"audio/L16;rate=48000;channels=2", 48000, 0, 2},
		{"audio/pcm", 0, 0, 0},
	}
	for _, tc := range cases {
		r, b, ch := parsePCMMimeType(tc.mime)
		if r != tc.wantRate || b != tc.wantBit || ch != tc.wantCh {
			t.Errorf("parsePCMMimeType(%q) = (%d,%d,%d), want (%d,%d,%d)", tc.mime, r, b, ch, tc.wantRate, tc.wantBit, tc.wantCh)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestGeminiLiveTTSGeneration is a key-gated live smoke test. It runs ONLY
// when GEMINI_API_KEY is set. Never prints the credential. A green test
// always means real speech audio was produced: quota/permission rejections
// (429, 403) are t.Skip with the model + reason so they never masquerade as
// a pass.
func TestGeminiLiveTTSGeneration(t *testing.T) {
	key := strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
	if key == "" {
		t.Skip("GEMINI_API_KEY not set; skipping live Gemini TTS smoke test")
	}
	const liveModel = "gemini-3.1-flash-tts-preview"
	client := &GeminiClient{
		BaseURL: "https://generativelanguage.googleapis.com/v1beta",
		APIKey:  key,
	}
	res, err := client.Synthesize(context.Background(), application.TTSRequest{
		Model: liveModel, Text: "Hello, this is a live test of Gemini text to speech.", Voice: "Kore",
	})
	if err != nil {
		var perr *domain.ProviderError
		if errors.As(err, &perr) && (perr.StatusCode == 429 || perr.StatusCode == 403) {
			t.Skipf("quota/permission exhausted for this key/tier on %s (HTTP %d): %s", liveModel, perr.StatusCode, perr.Error())
		}
		t.Fatalf("live Gemini TTS failed: %v", err)
	}
	if len(res.Audio) == 0 {
		t.Fatal("live Gemini TTS returned no audio")
	}
	if len(res.Audio) < 44 {
		t.Fatalf("generated audio too small: %d bytes", len(res.Audio))
	}
	if string(res.Audio[:4]) != "RIFF" {
		t.Fatalf("expected WAV container, got % x", res.Audio[:4])
	}
	if res.MediaType != "audio/wav" || res.Ext != "wav" {
		t.Fatalf("mediaType/ext = %q/%q, want audio/wav/wav", res.MediaType, res.Ext)
	}
	t.Logf("live proof OK: generated %d-byte %s audio (model %s, voice %s)", len(res.Audio), res.MediaType, res.Model, res.Voice)
}
