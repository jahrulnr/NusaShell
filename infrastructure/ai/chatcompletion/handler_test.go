package chatcompletion

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nusashell/application"
	"nusashell/domain"
)

func TestChatCompletionAdapterEncodesAttachments(t *testing.T) {
	req := application.ChatRequest{
		Model: "test-model",
		Messages: []application.ChatMessage{{
			Role:    "user",
			Content: "Review these files",
			Attachments: []domain.Attachment{
				{Type: "text", Name: "notes.txt", MediaType: "text/plain", Content: "Local notes"},
				{Type: "image", Name: "pixel.png", MediaType: "image/png", DataURL: "data:image/png;base64,iVBORw0KGgo="},
				{Type: "file", Name: "brief.pdf", MediaType: "application/pdf", DataURL: "data:application/pdf;base64,JVBERi0="},
			},
		}},
	}

	body := marshalRequest(t, buildRequest(req, false))
	if !containsAll(body, "[Attached text file: notes.txt - full content included below]\\n\\nLocal notes", "image_url", "data:image/png;base64,iVBORw0KGgo=", "[Attached document: brief.pdf (application/pdf)]") {
		t.Fatalf("chat attachment mapping = %s", body)
	}
	if strings.Contains(body, "file_data") {
		t.Fatalf("chat completions must use the Electron-compatible document marker: %s", body)
	}
}

func TestOpenAIListModelsParsesOpenRouterFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{
					"id":             "openai/gpt-4o",
					"context_length": 128000,
					"max_tokens":     16384,
					"description":    "GPT-4o is a multimodal model.",
					"pricing": map[string]any{
						"prompt":     "0.000005",
						"completion": "0.000015",
					},
				},
				{
					"id":             "anthropic/claude-3.5-sonnet",
					"context_length": 200000,
					"max_tokens":     8192,
					"description":    "Claude 3.5 Sonnet.",
					"pricing": map[string]any{
						"prompt": "0.000003",
					},
				},
			},
		})
	}))
	defer server.Close()

	adapter := &Adapter{BaseURL: server.URL + "/v1", Client: server.Client()}
	models, err := adapter.ListModels(context.Background(), "test-key")
	if err != nil {
		t.Fatalf("ListModels failed: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}

	m1 := models[0]
	if m1.ID != "openai/gpt-4o" {
		t.Fatalf("model 0 id = %q, want openai/gpt-4o", m1.ID)
	}
	if m1.Context != 128000 {
		t.Fatalf("model 0 context = %d, want 128000", m1.Context)
	}
	if m1.MaxOutput != 16384 {
		t.Fatalf("model 0 max_output = %d, want 16384", m1.MaxOutput)
	}
	if m1.Description != "GPT-4o is a multimodal model." {
		t.Fatalf("model 0 description = %q, want GPT-4o is a multimodal model.", m1.Description)
	}
	if m1.InputCost != 5 {
		t.Fatalf("model 0 input_cost = %v, want 5 (USD per 1M = 0.000005 * 1e6)", m1.InputCost)
	}

	m2 := models[1]
	if m2.Context != 200000 || m2.MaxOutput != 8192 {
		t.Fatalf("model 1: context=%d max_output=%d, want 200000/8192", m2.Context, m2.MaxOutput)
	}
}

// TestCompleteParsesCachedTokens is a regression test for the telemetry gap:
// OpenRouter reports Luna's prompt-cache hit rate at 92% while NusaShell
// showed ~48% because chatcompletion never parsed prompt_tokens_details.
// cached_tokens (CacheRead stayed 0 for DeepSeek and other chat-completion
// models). The non-streaming Complete path must populate CacheRead so the
// hit rate is computed, not zeroed.
//
// Option A normalization: OpenAI-style providers report prompt_tokens as the
// TOTAL (uncached + cached). The adapter must subtract cached_tokens so
// InputTokens is consistently the UNCACHED input across all providers, matching
// the Anthropic convention where input_tokens excludes cache fields. This
// keeps downstream telemetry (cost, charts, ContextTokens) consistent without
// per-provider branches.
func TestCompleteParsesCachedTokens(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message":       map[string]any{"content": "hello"},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     1000,
				"completion_tokens": 50,
				"prompt_tokens_details": map[string]any{
					"cached_tokens": 920,
				},
			},
		})
	}))
	defer server.Close()

	adapter := &Adapter{BaseURL: server.URL + "/v1", Client: server.Client()}
	resp, err := adapter.Complete(context.Background(), application.ChatRequest{
		Model: "test-model",
		Messages: []application.ChatMessage{{
			Role:    "user",
			Content: "Hi",
		}},
	})
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	// InputTokens is the UNCACHED prompt: total (1000) - cached (920) = 80.
	if resp.Usage.InputTokens != 80 {
		t.Errorf("InputTokens = %d, want 80 (uncached = prompt_tokens - cached_tokens)", resp.Usage.InputTokens)
	}
	if resp.Usage.CacheRead != 920 {
		t.Errorf("CacheRead = %d, want 920 (from prompt_tokens_details.cached_tokens)", resp.Usage.CacheRead)
	}
	// ContextTokens must still equal the full prompt + output: 80 + 920 + 50 = 1050.
	if got := resp.Usage.ContextTokens(); got != 1050 {
		t.Errorf("ContextTokens = %d, want 1050 (uncached + cached + output)", got)
	}
}

// TestCompleteNoCachedTokensKeepsFullPrompt verifies that when the provider
// reports no cached tokens, InputTokens equals the full prompt_tokens (no
// subtraction). This is the common case for the first turn of a conversation.
func TestCompleteNoCachedTokensKeepsFullPrompt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": "hi"}, "finish_reason": "stop"},
			},
			"usage": map[string]any{
				"prompt_tokens":     500,
				"completion_tokens": 20,
			},
		})
	}))
	defer server.Close()

	adapter := &Adapter{BaseURL: server.URL + "/v1", Client: server.Client()}
	resp, err := adapter.Complete(context.Background(), application.ChatRequest{
		Model:    "test-model",
		Messages: []application.ChatMessage{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if resp.Usage.InputTokens != 500 {
		t.Errorf("InputTokens = %d, want 500 (no cache, full prompt)", resp.Usage.InputTokens)
	}
	if resp.Usage.CacheRead != 0 {
		t.Errorf("CacheRead = %d, want 0", resp.Usage.CacheRead)
	}
}

func TestCompleteExtractsReasoningContent(t *testing.T) {
	// Reasoning models (DeepSeek, dots-3-note, Qwen) put their output in
	// "reasoning_content" instead of "content" in non-streaming responses.
	// The Complete path must extract it so vision fallback and other
	// non-streaming callers don't get an empty response.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"content":           nil,
						"reasoning_content": "The image shows a cat sitting on a windowsill.",
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     100,
				"completion_tokens": 50,
			},
		})
	}))
	defer server.Close()

	adapter := &Adapter{BaseURL: server.URL + "/v1", Client: server.Client()}
	resp, err := adapter.Complete(context.Background(), application.ChatRequest{
		Model: "test-model",
		Messages: []application.ChatMessage{{
			Role:    "user",
			Content: "Describe this image",
		}},
	})
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if resp.Content != "" {
		t.Errorf("Content = %q, want empty (model puts output in reasoning)", resp.Content)
	}
	if resp.Reasoning != "The image shows a cat sitting on a windowsill." {
		t.Errorf("Reasoning = %q, want the description", resp.Reasoning)
	}
}

func containsAll(value string, want ...string) bool {
	for _, item := range want {
		if !strings.Contains(value, item) {
			return false
		}
	}
	return true
}

func marshalRequest(t *testing.T, value any) string {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// TestToOpenAIMessagesReasoningReplay proves that when ReasoningReplay is
// true, assistant messages carry reasoning_content on the wire so
// thinking-mode upstreams (DeepSeek V4, GLM, Kimi, ox-alpha) can
// reconstruct the thinking state across turns. When false, the field is
// omitted — providers that ignore it (OpenAI, Anthropic) are unaffected.
func TestToOpenAIMessagesReasoningReplay(t *testing.T) {
	msgs := []application.ChatMessage{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there", Reasoning: "I should greet the user."},
		{Role: "user", Content: "What is 2+2?"},
	}

	// Without replay: no reasoning_content on the wire
	got := toOpenAIMessages(application.ChatRequest{Messages: msgs})
	for i, m := range got {
		if m.Role == "assistant" && m.ReasoningContent != "" {
			t.Errorf("message %d (no replay): ReasoningContent = %q, want empty", i, m.ReasoningContent)
		}
	}

	// With replay: assistant message carries the persisted reasoning
	got = toOpenAIMessages(application.ChatRequest{Messages: msgs, ReasoningReplay: true})
	found := false
	for _, m := range got {
		if m.Role == "assistant" {
			if m.ReasoningContent != "I should greet the user." {
				t.Errorf("with replay: ReasoningContent = %q, want persisted reasoning", m.ReasoningContent)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("no assistant message in output")
	}
}

// TestToOpenAIMessagesReasoningReplayPlaceholder proves that when
// ReasoningReplay is true but the assistant message has no persisted
// reasoning, a non-empty placeholder is injected. Some providers (MiMo)
// reject an absent field; others (DeepSeek) accept absent but reject
// empty-string.
func TestToOpenAIMessagesReasoningReplayPlaceholder(t *testing.T) {
	msgs := []application.ChatMessage{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there", Reasoning: ""},
	}
	got := toOpenAIMessages(application.ChatRequest{Messages: msgs, ReasoningReplay: true})
	for _, m := range got {
		if m.Role == "assistant" {
			if m.ReasoningContent != domain.ReasoningPlaceholder {
				t.Errorf("placeholder: ReasoningContent = %q, want %q", m.ReasoningContent, domain.ReasoningPlaceholder)
			}
		}
	}
}

// TestToOpenAIMessagesReasoningReplayStripsPlaceholder proves that a
// previously-injected placeholder is not re-replayed as real reasoning
// (prevents echo loop — OmniRoute #9573).
func TestToOpenAIMessagesReasoningReplayStripsPlaceholder(t *testing.T) {
	msgs := []application.ChatMessage{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there", Reasoning: domain.ReasoningPlaceholder},
	}
	got := toOpenAIMessages(application.ChatRequest{Messages: msgs, ReasoningReplay: true})
	for _, m := range got {
		if m.Role == "assistant" {
			if m.ReasoningContent != domain.ReasoningPlaceholder {
				t.Errorf("strip placeholder: ReasoningContent = %q, want fresh placeholder", m.ReasoningContent)
			}
		}
	}
}

// TestBuildRequestStripParams proves that learned strip params cause the
// corresponding fields to be omitted from the wire request. A model that
// 400'd with "Unsupported parameter: temperature" must not have
// temperature sent on subsequent requests.
func TestBuildRequestStripParams(t *testing.T) {
	temp := 0.7
	topP := 0.9
	req := application.ChatRequest{
		Model:       "glm-5.2",
		Effort:      "high",
		Temperature: &temp,
		TopP:        &topP,
		StripParams: []string{"temperature", "reasoning_effort"},
	}
	r := buildRequest(req, false)
	if r.Temperature != nil {
		t.Errorf("temperature should be stripped, got %v", r.Temperature)
	}
	if r.ReasoningEffort != "" {
		t.Errorf("reasoning_effort should be stripped, got %q", r.ReasoningEffort)
	}
	// top_p is NOT in strip list, must survive
	if r.TopP == nil || *r.TopP != 0.9 {
		t.Errorf("top_p should survive, got %v", r.TopP)
	}
}

// TestBuildRequestStripParamsCaseInsensitive proves the strip list match
// is case-insensitive (learned entries are lowercased on record).
func TestBuildRequestStripParamsCaseInsensitive(t *testing.T) {
	temp := 0.7
	req := application.ChatRequest{
		Model:       "m",
		Temperature: &temp,
		StripParams: []string{"TEMPERATURE"},
	}
	r := buildRequest(req, false)
	if r.Temperature != nil {
		t.Errorf("case-insensitive strip failed: temperature = %v", r.Temperature)
	}
}

// TestStreamCompletedEmptyFallsBackToComplete verifies that a streaming
// response which terminates cleanly ([DONE]) but carries no content,
// reasoning, or tool calls falls back to a non-streaming Complete request
// instead of failing the turn with "provider returned empty content".
// Unstable upstream gateways (e.g. proxies returning a 200 with an empty
// SSE body) often serve non-streaming fine, so the fallback lets the turn
// continue. This mirrors the existing incomplete-stream fallback path.
func TestStreamCompletedEmptyFallsBackToComplete(t *testing.T) {
	var streamCalls, completeCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Stream bool `json:"stream"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Stream {
			streamCalls++
			w.Header().Set("Content-Type", "text/event-stream")
			// Completed but empty: only the terminator, no content chunks.
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
			return
		}
		completeCalls++
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": "Hello from complete"}, "finish_reason": "stop"},
			},
		})
	}))
	defer server.Close()

	adapter := &Adapter{BaseURL: server.URL + "/v1", Client: server.Client()}
	var got string
	resp, err := adapter.Stream(context.Background(), application.ChatRequest{
		Model:    "test-model",
		Messages: []application.ChatMessage{{Role: "user", Content: "hi"}},
	}, func(delta string) { got += delta }, nil)
	if err != nil {
		t.Fatalf("Stream returned error on completed-empty stream, want fallback to Complete: %v", err)
	}
	if streamCalls != 1 {
		t.Errorf("stream request count = %d, want 1", streamCalls)
	}
	if completeCalls != 1 {
		t.Errorf("non-streaming fallback count = %d, want 1", completeCalls)
	}
	if resp.Content != "Hello from complete" {
		t.Errorf("Content = %q, want %q", resp.Content, "Hello from complete")
	}
	if got != "Hello from complete" {
		t.Errorf("onDelta delivered %q, want %q", got, "Hello from complete")
	}
}

// TestStreamCompletedEmptyFallbackAlsoEmptyReturnsRetryable verifies that
// when both the streaming response and the non-streaming fallback return
// empty content, the adapter surfaces a retryable UpstreamError so the
// agent retry loop can re-request rather than silently ending the turn.
func TestStreamCompletedEmptyFallbackAlsoEmptyReturnsRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Stream bool `json:"stream"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": ""}, "finish_reason": "stop"},
			},
		})
	}))
	defer server.Close()

	adapter := &Adapter{BaseURL: server.URL + "/v1", Client: server.Client()}
	_, err := adapter.Stream(context.Background(), application.ChatRequest{
		Model:    "test-model",
		Messages: []application.ChatMessage{{Role: "user", Content: "hi"}},
	}, func(string) {}, nil)
	if err == nil {
		t.Fatalf("Stream with empty stream + empty fallback should return a retryable error, got nil")
	}
	var upstream *application.UpstreamError
	if !errors.As(err, &upstream) {
		t.Fatalf("error must be *application.UpstreamError, got %T: %v", err, err)
	}
	if !upstream.Temporary {
		t.Errorf("UpstreamError.Temporary = false, want true (retryable)")
	}
}

// TestBuildRequestNoStripParams proves that without strip params, all
// fields are sent as normal.
func TestBuildRequestNoStripParams(t *testing.T) {
	temp := 0.7
	req := application.ChatRequest{
		Model:       "m",
		Effort:      "high",
		Temperature: &temp,
	}
	r := buildRequest(req, false)
	if r.Temperature == nil || *r.Temperature != 0.7 {
		t.Errorf("temperature should be present, got %v", r.Temperature)
	}
	if r.ReasoningEffort != "high" {
		t.Errorf("reasoning_effort should be present, got %q", r.ReasoningEffort)
	}
}
