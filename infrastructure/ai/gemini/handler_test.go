package gemini

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nusashell/application"
	"nusashell/domain"
)

const png1x1B64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAAC0lEQVR42mNk+M8AAAMBAQDJ/pLvAAAAAElFTkSuQmCC"

func TestAdapterKind(t *testing.T) {
	a := &Adapter{}
	if got := a.Kind(); got != domain.ProviderGemini {
		t.Errorf("Kind() = %s, want %s", got, domain.ProviderGemini)
	}
}

func TestGenerateContentURL(t *testing.T) {
	a := &Adapter{BaseURL: "https://generativelanguage.googleapis.com/v1beta"}
	got := a.generateContentURL("gemini-3.1-flash-image-preview")
	want := "https://generativelanguage.googleapis.com/v1beta/models/gemini-3.1-flash-image-preview:generateContent"
	if got != want {
		t.Errorf("generateContentURL = %s, want %s", got, want)
	}
}

func TestStreamGenerateContentURL(t *testing.T) {
	a := &Adapter{BaseURL: "https://generativelanguage.googleapis.com/v1beta"}
	got := a.streamGenerateContentURL("gemini-2.5-flash")
	want := "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:streamGenerateContent?alt=sse"
	if got != want {
		t.Errorf("streamGenerateContentURL = %s, want %s", got, want)
	}
}

func TestHeadersIncludeAPIKey(t *testing.T) {
	a := &Adapter{APIKey: "test-key-123"}
	h := a.headers()
	if h["x-goog-api-key"] != "test-key-123" {
		t.Errorf("x-goog-api-key = %s, want test-key-123", h["x-goog-api-key"])
	}
}

func TestCompleteParsesTextResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") != "tok" {
			t.Errorf("api key header = %s", r.Header.Get("x-goog-api-key"))
		}
		if !strings.HasSuffix(r.URL.Path, ":generateContent") {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [{
				"content": {"parts": [{"text": "Hello from Gemini"}]},
				"finishReason": "STOP"
			}],
			"usageMetadata": {"promptTokenCount": 10, "candidatesTokenCount": 5, "totalTokenCount": 15}
		}`))
	}))
	defer srv.Close()

	a := &Adapter{BaseURL: srv.URL, APIKey: "tok", Client: srv.Client()}
	resp, err := a.Complete(context.Background(), application.ChatRequest{
		Model: "gemini-2.5-flash",
		Messages: []application.ChatMessage{
			{Role: "user", Content: "hi"},
		},
	})
	if err != nil {
		t.Fatalf("Complete error: %v", err)
	}
	if resp.Content != "Hello from Gemini" {
		t.Errorf("Content = %q", resp.Content)
	}
	if resp.Usage.InputTokens != 10 {
		t.Errorf("InputTokens = %d", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 5 {
		t.Errorf("OutputTokens = %d", resp.Usage.OutputTokens)
	}
	if resp.StopReason != "stop" {
		t.Errorf("StopReason = %q", resp.StopReason)
	}
}

func TestCompleteParsesToolCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [{
				"content": {"parts": [{"functionCall": {"id": "call-123", "name": "get_weather", "args": {"city": "Jakarta"}}}]},
				"finishReason": "STOP"
			}]
		}`))
	}))
	defer srv.Close()

	a := &Adapter{BaseURL: srv.URL, APIKey: "tok", Client: srv.Client()}
	resp, err := a.Complete(context.Background(), application.ChatRequest{
		Model:    "gemini-2.5-flash",
		Messages: []application.ChatMessage{{Role: "user", Content: "weather"}},
	})
	if err != nil {
		t.Fatalf("Complete error: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("ToolCalls len = %d", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "call-123" {
		t.Errorf("tool ID = %s, want call-123", tc.ID)
	}
	if tc.Name != "get_weather" {
		t.Errorf("tool name = %s", tc.Name)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(tc.Args), &args); err != nil {
		t.Fatalf("unmarshal args: %v", err)
	}
	if args["city"] != "Jakarta" {
		t.Errorf("city arg = %v", args["city"])
	}
}

func TestCompleteParsesToolCallFallbackID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [{
				"content": {"parts": [{"functionCall": {"name": "get_weather", "args": {}}}]},
				"finishReason": "STOP"
			}]
		}`))
	}))
	defer srv.Close()

	a := &Adapter{BaseURL: srv.URL, APIKey: "tok", Client: srv.Client()}
	resp, err := a.Complete(context.Background(), application.ChatRequest{
		Model:    "gemini-2.5-flash",
		Messages: []application.ChatMessage{{Role: "user", Content: "weather"}},
	})
	if err != nil {
		t.Fatalf("Complete error: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("ToolCalls len = %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].ID != "get_weather" {
		t.Errorf("fallback ID = %s, want get_weather", resp.ToolCalls[0].ID)
	}
}

func TestCompleteParsesThoughtSignature(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [{
				"content": {"parts": [{"functionCall": {"id": "call-1", "name": "get_weather", "args": {}, "thoughtSignature": "sig-abc123"}}]},
				"finishReason": "STOP"
			}]
		}`))
	}))
	defer srv.Close()

	a := &Adapter{BaseURL: srv.URL, APIKey: "tok", Client: srv.Client()}
	resp, err := a.Complete(context.Background(), application.ChatRequest{
		Model:    "gemini-2.5-flash",
		Messages: []application.ChatMessage{{Role: "user", Content: "weather"}},
	})
	if err != nil {
		t.Fatalf("Complete error: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("ToolCalls len = %d", len(resp.ToolCalls))
	}
	sig, ok := resp.ToolCalls[0].Opaque["thought_signature"].(string)
	if !ok {
		t.Fatalf("Opaque[thought_signature] not found or wrong type: %v", resp.ToolCalls[0].Opaque)
	}
	if sig != "sig-abc123" {
		t.Errorf("thought_signature = %s, want sig-abc123", sig)
	}
}

func TestBuildContentsEchoesThoughtSignature(t *testing.T) {
	msgs := []application.ChatMessage{{
		Role: "assistant",
		ToolCalls: []domain.ToolCall{{
			ID:     "call-abc",
			Name:   "get_weather",
			Args:   `{"city":"Jakarta"}`,
			Opaque: map[string]any{"thought_signature": "sig-xyz"},
		}},
	}}
	contents := buildContents(msgs)
	if len(contents) != 1 {
		t.Fatalf("contents len = %d", len(contents))
	}
	fc := contents[0].Parts[0].FunctionCall
	if fc == nil {
		t.Fatal("expected FunctionCall part")
	}
	if fc.ThoughtSignature != "sig-xyz" {
		t.Errorf("ThoughtSignature = %s, want sig-xyz", fc.ThoughtSignature)
	}
}

func TestCompleteParsesThinking(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [{
				"content": {"parts": [
					{"text": "Let me think about this", "thought": true},
					{"text": "The answer is 42"}
				]},
				"finishReason": "STOP"
			}]
		}`))
	}))
	defer srv.Close()

	a := &Adapter{BaseURL: srv.URL, APIKey: "tok", Client: srv.Client()}
	resp, err := a.Complete(context.Background(), application.ChatRequest{
		Model:    "gemini-2.5-flash",
		Messages: []application.ChatMessage{{Role: "user", Content: "answer"}},
	})
	if err != nil {
		t.Fatalf("Complete error: %v", err)
	}
	if resp.Reasoning != "Let me think about this" {
		t.Errorf("Reasoning = %q", resp.Reasoning)
	}
	if resp.Content != "The answer is 42" {
		t.Errorf("Content = %q", resp.Content)
	}
}

func TestBuildContentsAssistantToolCallEchoesID(t *testing.T) {
	msgs := []application.ChatMessage{{
		Role: "assistant",
		ToolCalls: []domain.ToolCall{{
			ID:   "call-abc",
			Name: "get_weather",
			Args: `{"city":"Jakarta"}`,
		}},
	}}
	contents := buildContents(msgs)
	if len(contents) != 1 {
		t.Fatalf("contents len = %d", len(contents))
	}
	if len(contents[0].Parts) != 1 {
		t.Fatalf("parts len = %d", len(contents[0].Parts))
	}
	fc := contents[0].Parts[0].FunctionCall
	if fc == nil {
		t.Fatal("expected FunctionCall part")
	}
	if fc.ID != "call-abc" {
		t.Errorf("FunctionCall.ID = %s, want call-abc", fc.ID)
	}
	if fc.Name != "get_weather" {
		t.Errorf("FunctionCall.Name = %s", fc.Name)
	}
}

func TestBuildContentsToolResultEchoesID(t *testing.T) {
	msgs := []application.ChatMessage{{
		Role: "tool",
		ToolResult: &application.ToolResult{
			ToolCallID: "call-abc",
			Name:       "get_weather",
			Content:    "sunny, 32C",
		},
	}}
	contents := buildContents(msgs)
	if len(contents) != 1 {
		t.Fatalf("contents len = %d", len(contents))
	}
	fr := contents[0].Parts[0].FunctionResponse
	if fr == nil {
		t.Fatal("expected FunctionResponse part")
	}
	if fr.ID != "call-abc" {
		t.Errorf("FunctionResponse.ID = %s, want call-abc", fr.ID)
	}
}

func TestStreamParsesDeltas(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		// Chunk 1: text delta
		_, _ = w.Write([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"Hello\"}]}}]}\n\n"))
		flusher.Flush()
		// Chunk 2: text delta + finish
		_, _ = w.Write([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\" world\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":5,\"candidatesTokenCount\":2,\"totalTokenCount\":7}}\n\n"))
		flusher.Flush()
	}))
	defer srv.Close()

	a := &Adapter{BaseURL: srv.URL, APIKey: "tok", Client: srv.Client()}
	var deltas string
	resp, err := a.Stream(context.Background(), application.ChatRequest{
		Model:    "gemini-2.5-flash",
		Messages: []application.ChatMessage{{Role: "user", Content: "hi"}},
	}, func(text string) { deltas += text }, nil)
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}
	if resp.Content != "Hello world" {
		t.Errorf("Content = %q, want 'Hello world'", resp.Content)
	}
	if deltas != "Hello world" {
		t.Errorf("deltas = %q", deltas)
	}
	if resp.Usage.ContextTokens() == 0 {
		t.Errorf("Usage empty")
	}
}

func TestBuildContentsUserWithImage(t *testing.T) {
	msgs := []application.ChatMessage{{
		Role:    "user",
		Content: "describe this",
		Attachments: []domain.Attachment{{
			Type:    "image",
			DataURL: "data:image/png;base64," + png1x1B64,
		}},
	}}
	contents := buildContents(msgs)
	if len(contents) != 1 {
		t.Fatalf("contents len = %d", len(contents))
	}
	if contents[0].Role != "user" {
		t.Errorf("role = %s", contents[0].Role)
	}
	// Should have text part + inlineData part
	if len(contents[0].Parts) != 2 {
		t.Fatalf("parts len = %d, want 2", len(contents[0].Parts))
	}
	if contents[0].Parts[0].Text != "describe this" {
		t.Errorf("part[0].Text = %q", contents[0].Parts[0].Text)
	}
	if contents[0].Parts[1].InlineData == nil {
		t.Fatal("part[1].InlineData is nil")
	}
	if contents[0].Parts[1].InlineData.MimeType != "image/png" {
		t.Errorf("mimeType = %s", contents[0].Parts[1].InlineData.MimeType)
	}
}

func TestBuildContentsToolResult(t *testing.T) {
	msgs := []application.ChatMessage{{
		Role: "tool",
		ToolResult: &application.ToolResult{
			ToolCallID: "call-1",
			Name:       "get_weather",
			Content:    "sunny, 32C",
		},
	}}
	contents := buildContents(msgs)
	if len(contents) != 1 {
		t.Fatalf("contents len = %d", len(contents))
	}
	if contents[0].Role != "user" {
		t.Errorf("role = %s, want user (Gemini has no function role)", contents[0].Role)
	}
	if len(contents[0].Parts) != 1 || contents[0].Parts[0].FunctionResponse == nil {
		t.Fatal("expected functionResponse part")
	}
	fr := contents[0].Parts[0].FunctionResponse
	if fr.Name != "get_weather" {
		t.Errorf("name = %s", fr.Name)
	}
	if fr.Response["response"] != "sunny, 32C" {
		t.Errorf("response = %v", fr.Response["response"])
	}
}

func TestImagesClientGeneratesImage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") != "img-key" {
			t.Errorf("api key = %s", r.Header.Get("x-goog-api-key"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [{
				"content": {"parts": [{"inlineData": {"mimeType": "image/png", "data": "` + png1x1B64 + `"}}]}
			}],
			"usageMetadata": {"promptTokenCount": 5, "candidatesTokenCount": 10, "totalTokenCount": 15}
		}`))
	}))
	defer srv.Close()

	c := &ImagesClient{BaseURL: srv.URL, APIKey: "img-key", HTTP: srv.Client()}
	result, err := c.Generate(context.Background(), application.ImageGenRequest{
		Model:  "gemini-3.1-flash-image-preview",
		Prompt: "a banana",
	})
	if err != nil {
		t.Fatalf("Generate error: %v", err)
	}
	if result.Provider != "gemini" {
		t.Errorf("Provider = %s", result.Provider)
	}
	if len(result.Images) != 1 {
		t.Fatalf("Images len = %d", len(result.Images))
	}
	if result.Images[0].MediaType != "image/png" {
		t.Errorf("MediaType = %s", result.Images[0].MediaType)
	}
	if len(result.Images[0].Bytes) == 0 {
		t.Error("image bytes empty")
	}
	if result.UsageTokens != 15 {
		t.Errorf("UsageTokens = %d", result.UsageTokens)
	}
}

func TestImagesClientWithReferenceImage(t *testing.T) {
	var sentBody generateContentRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&sentBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"` + png1x1B64 + `"}}]}}]}`))
	}))
	defer srv.Close()

	refData := []byte{0x89, 0x50, 0x4E, 0x47}
	c := &ImagesClient{BaseURL: srv.URL, APIKey: "k", HTTP: srv.Client()}
	_, err := c.Generate(context.Background(), application.ImageGenRequest{
		Model:      "gemini-3.1-flash-image-preview",
		Prompt:     "add sunglasses",
		References: []application.ImageReference{{MediaType: "image/png", Data: refData}},
	})
	if err != nil {
		t.Fatalf("Generate error: %v", err)
	}
	if len(sentBody.Contents) != 1 {
		t.Fatalf("contents len = %d", len(sentBody.Contents))
	}
	parts := sentBody.Contents[0].Parts
	// Should have: text prompt + 1 reference image
	if len(parts) != 2 {
		t.Fatalf("parts len = %d, want 2", len(parts))
	}
	if parts[0].Text != "add sunglasses" {
		t.Errorf("part[0].Text = %q", parts[0].Text)
	}
	if parts[1].InlineData == nil {
		t.Fatal("part[1].InlineData is nil")
	}
	if parts[1].InlineData.MimeType != "image/png" {
		t.Errorf("ref mimeType = %s", parts[1].InlineData.MimeType)
	}
}

func TestImagesClientNoImageInResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"I cannot generate that"}]}}]}`))
	}))
	defer srv.Close()

	c := &ImagesClient{BaseURL: srv.URL, APIKey: "k", HTTP: srv.Client()}
	_, err := c.Generate(context.Background(), application.ImageGenRequest{
		Model:  "gemini-3.1-flash-image-preview",
		Prompt: "test",
	})
	if err == nil {
		t.Fatal("expected error for no image in response")
	}
	if !strings.Contains(err.Error(), "no image data") {
		t.Errorf("error = %v", err)
	}
}
