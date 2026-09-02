package aiutil

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nusashell/domain"
)

func TestInputAudioFormat(t *testing.T) {
	tests := map[string]string{
		"audio/wav":      "wav",
		"audio/x-wav":    "wav",
		"audio/wave":     "wav",
		"audio/vnd.wave": "wav",
		"audio/ogg":      "ogg",
		"audio/webm":     "webm",
		"audio/flac":     "flac",
		" AUDIO/WAV ":    "wav",
		"audio/unknown":  "mp3",
		"":               "mp3",
	}
	for mediaType, want := range tests {
		t.Run(mediaType, func(t *testing.T) {
			if got := InputAudioFormat(mediaType); got != want {
				t.Fatalf("InputAudioFormat(%q) = %q, want %q", mediaType, got, want)
			}
		})
	}
}

func TestAttachmentBlocksEncodeExpectedProviderShape(t *testing.T) {
	attachment := domain.Attachment{
		DataURL:   "data:audio/wav;base64,QUJD",
		MediaType: "audio/wav",
	}
	audio := InputAudioBlock(attachment)
	if audio["type"] != "input_audio" {
		t.Fatalf("audio type = %v, want input_audio", audio["type"])
	}
	audioPayload, ok := audio["input_audio"].(map[string]any)
	if !ok {
		t.Fatalf("input_audio = %T, want map[string]any", audio["input_audio"])
	}
	if audioPayload["data"] != "QUJD" || audioPayload["format"] != "wav" {
		t.Fatalf("input_audio = %#v, want data=QUJD format=wav", audioPayload)
	}

	video := VideoURLBlock(domain.Attachment{DataURL: "data:video/mp4;base64,AAAA"})
	if video["type"] != "video_url" {
		t.Fatalf("video type = %v, want video_url", video["type"])
	}
	videoPayload, ok := video["video_url"].(map[string]any)
	if !ok || videoPayload["url"] != "data:video/mp4;base64,AAAA" {
		t.Fatalf("video_url = %#v, want original data URL", video["video_url"])
	}
}

func TestNormalizeEffortAndEfforts(t *testing.T) {
	tests := map[string]string{
		"":          "auto",
		" DEFAULT ": "auto",
		"off":       "none",
		"min":       "minimal",
		"med":       "medium",
		"x-high":    "xhigh",
		"maximum":   "max",
		"invalid":   "auto",
	}
	for input, want := range tests {
		t.Run(input, func(t *testing.T) {
			if got := NormalizeEffort(input); got != want {
				t.Fatalf("NormalizeEffort(%q) = %q, want %q", input, got, want)
			}
		})
	}

	got := NormalizeEfforts([]string{"low", "MED", "medium", "auto", "invalid", "off", "low"})
	want := []string{"low", "medium", "none"}
	if !equalStrings(got, want) {
		t.Fatalf("NormalizeEfforts() = %#v, want %#v", got, want)
	}
	if got := NormalizeEfforts(nil); got != nil {
		t.Fatalf("NormalizeEfforts(nil) = %#v, want nil", got)
	}
}

func TestUtilityHelpers(t *testing.T) {
	if got := string(MustJSON(map[string]string{"key": "value"})); got != `{"key":"value"}` {
		t.Fatalf("MustJSON() = %q", got)
	}
	if got := string(MustJSON(func() {})); got != "[]" {
		t.Fatalf("MustJSON(unmarshalable) = %q, want []", got)
	}
	if got, err := ParseFloat("3.25"); err != nil || got != 3.25 {
		t.Fatalf("ParseFloat(3.25) = %v, %v", got, err)
	}
	if _, err := ParseFloat(""); err == nil {
		t.Fatal("ParseFloat(empty) returned nil error")
	}
	value := "value"
	if Deref(&value) != value || Deref(nil) != "" {
		t.Fatal("Deref did not handle pointer and nil correctly")
	}
	if got := string(StrJSON(`quoted`)); got != `"quoted"` {
		t.Fatalf("StrJSON() = %q", got)
	}
	if got := DataURLBase64("data:image/png;base64,AAAA"); got != "AAAA" {
		t.Fatalf("DataURLBase64() = %q, want AAAA", got)
	}
	if got := DataURLBase64("not-a-data-url"); got != "" {
		t.Fatalf("DataURLBase64(invalid) = %q, want empty", got)
	}
	attachment := domain.Attachment{Name: "notes.txt", Content: "hello", MediaType: "text/plain"}
	if !strings.Contains(TextAttachmentContent(attachment), "notes.txt") {
		t.Fatal("TextAttachmentContent omitted attachment name")
	}
	if got := DocumentAttachmentContent(attachment); got != "[Attached document: notes.txt (text/plain)]" {
		t.Fatalf("DocumentAttachmentContent() = %q", got)
	}
}

func TestFetchModelHelpersDeduplicateAndIgnoreEmptyIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" && r.URL.Query().Get("output_modalities") != "speech" {
			t.Errorf("speech request missing output_modalities=speech: %s", r.URL.String())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"known"},{"id":"new"},{"id":""},{"id":"new"}]}`))
	}))
	defer server.Close()

	tests := []struct {
		name string
		call func(map[string]bool) []string
	}{
		{name: "embedding", call: func(seen map[string]bool) []string {
			return FetchEmbeddingModels(context.Background(), server.Client(), server.URL+"/v1", nil, seen)
		}},
		{name: "image", call: func(seen map[string]bool) []string {
			return FetchImageModels(context.Background(), server.Client(), server.URL+"/v1", nil, seen)
		}},
		{name: "speech", call: func(seen map[string]bool) []string {
			return FetchSpeechModels(context.Background(), server.Client(), server.URL+"/v1", nil, seen)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seen := map[string]bool{"known": true}
			got := tt.call(seen)
			if !equalStrings(got, []string{"new"}) {
				t.Fatalf("%s models = %#v, want [new]", tt.name, got)
			}
			if !seen["new"] {
				t.Fatal("new model was not added to seen")
			}
		})
	}
}

func TestFetchModelHelpersReturnNilOnHTTPError(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	seen := map[string]bool{}
	if got := FetchEmbeddingModels(context.Background(), server.Client(), server.URL, nil, seen); got != nil {
		t.Fatalf("FetchEmbeddingModels on 404 = %#v, want nil", got)
	}
	if got := FetchImageModels(context.Background(), server.Client(), server.URL, nil, seen); got != nil {
		t.Fatalf("FetchImageModels on 404 = %#v, want nil", got)
	}
	if got := FetchSpeechModels(context.Background(), server.Client(), server.URL, nil, seen); got != nil {
		t.Fatalf("FetchSpeechModels on 404 = %#v, want nil", got)
	}
}

func TestHTTPHelpers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/answer" {
			t.Errorf("request = %s %s, want POST /answer", r.Method, r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Accept") != "application/json" {
			t.Errorf("headers = %#v, want JSON content and accept", r.Header)
		}
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	var result struct {
		OK bool `json:"ok"`
	}
	if err := DoJSON(context.Background(), server.Client(), http.MethodPost, server.URL+"/answer", nil, map[string]string{"q": "hello"}, &result); err != nil {
		t.Fatalf("DoJSON failed: %v", err)
	}
	if !result.OK {
		t.Fatal("DoJSON did not decode response")
	}

	sseServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "text/event-stream, application/json" {
			t.Errorf("SSE Accept = %q", r.Header.Get("Accept"))
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer sseServer.Close()
	resp, err := OpenSSE(context.Background(), sseServer.Client(), sseServer.URL, nil, nil)
	if err != nil {
		t.Fatalf("OpenSSE success status failed: %v", err)
	}
	resp.Body.Close()

	errorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("busy"))
	}))
	defer errorServer.Close()
	_, err = OpenSSE(context.Background(), errorServer.Client(), errorServer.URL, nil, nil)
	var upstream *domain.ProviderError
	if !errors.As(err, &upstream) || upstream.StatusCode != http.StatusTooManyRequests || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("OpenSSE error = %v, want 429 upstream error containing busy", err)
	}
}

func TestSSEAndURLHelpers(t *testing.T) {
	var decoded struct {
		Message string `json:"message"`
	}
	if err := DecodeData(Event{Data: `{"message":"hello"}`}, &decoded); err != nil || decoded.Message != "hello" {
		t.Fatalf("DecodeData = %#v, %v", decoded, err)
	}
	if err := DecodeData(Event{}, &decoded); err == nil {
		t.Fatal("DecodeData(empty) returned nil error")
	}
	if got := JoinEndpoint("https://example.test/v1///", "/models"); got != "https://example.test/v1/models" {
		t.Fatalf("JoinEndpoint = %q", got)
	}
	if got := JoinEndpoint("https://example.test/v1/models", "/models"); got != "https://example.test/v1/models" {
		t.Fatalf("JoinEndpoint duplicated operation path: %q", got)
	}
	if IsOpenAIDirectURL("https://openrouter.ai/api/v1") || !IsOpenAIDirectURL("https://api.openai.com/v1") {
		t.Fatal("IsOpenAIDirectURL classified hosts incorrectly")
	}

	incomplete := IncompleteSSEError()
	if !errors.Is(incomplete, io.ErrUnexpectedEOF) {
		t.Fatalf("IncompleteSSEError = %v, want UnexpectedEOF wrapper", incomplete)
	}
	var upstream *domain.ProviderError
	if !errors.As(incomplete, &upstream) {
		t.Fatalf("IncompleteSSEError = %T, want domain.ProviderError", incomplete)
	}
	if got := RetryableSSEReadError(nil); got != nil {
		t.Fatalf("RetryableSSEReadError(nil) = %v, want nil", got)
	}
	if got := RetryableSSEReadError(io.EOF); got != io.EOF {
		t.Fatalf("RetryableSSEReadError(EOF) = %v, want EOF", got)
	}
	if got := RetryableSSEReadError(ErrResponseTooLarge); got == nil || !strings.Contains(got.Error(), "size limit") {
		t.Fatalf("RetryableSSEReadError(size) = %v", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
