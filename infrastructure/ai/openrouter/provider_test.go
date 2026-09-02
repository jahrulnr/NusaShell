package openrouter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"nusashell/infrastructure/ai/compat"
	"nusashell/infrastructure/ai/core"
	"nusashell/infrastructure/ai/internal/testgolden"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestHeadersReasoningAndCache(t *testing.T) {
	var referer, title string
	body := captureBody(t, &referer, &title, &core.Request{
		Model: "anthropic/claude-sonnet-4",
		Messages: []core.Message{core.User(core.TextBlock{
			Text:  "hi",
			Cache: &core.CacheControl{Type: core.CacheTypeEphemeral, TTL: core.CacheTTL1h},
		})},
		Thinking:        &core.Thinking{Mode: core.ThinkingEnabled, Effort: "high"},
		ProviderOptions: core.ProviderOptions{"cache_retention": "1h"},
	})
	if referer == "" || title != "NusaShell" {
		t.Fatalf("headers referer=%q title=%q", referer, title)
	}
	reasoning := body["reasoning"].(map[string]any)
	if reasoning["effort"] != "high" {
		t.Fatalf("reasoning = %#v", reasoning)
	}
	cache := body["cache_control"].(map[string]any)
	if cache["ttl"] != "1h" {
		t.Fatalf("cache_control = %#v", cache)
	}
	content := body["messages"].([]any)[0].(map[string]any)["content"].([]any)
	blockCache := content[0].(map[string]any)["cache_control"].(map[string]any)
	if blockCache["ttl"] != "1h" {
		t.Fatalf("block cache = %#v", blockCache)
	}
	testgolden.AssertJSON(t, "../../testdata/compat/openrouter_request.golden.json", body)
}

func TestThinkingRequiresBudgetOrEffort(t *testing.T) {
	body := captureBody(t, nil, nil, &core.Request{
		Model:    "anthropic/claude-sonnet-4",
		Messages: []core.Message{core.UserText("hi")},
		Thinking: &core.Thinking{Mode: core.ThinkingEnabled},
	})
	reasoning := body["reasoning"].(map[string]any)
	if reasoning["enabled"] != true {
		t.Fatalf("reasoning = %#v", reasoning)
	}
}

func TestThinkingDisabledAndEffortValidation(t *testing.T) {
	body := captureBody(t, nil, nil, &core.Request{
		Model:    "anthropic/claude-sonnet-4",
		Messages: []core.Message{core.UserText("hi")},
		Thinking: &core.Thinking{Mode: core.ThinkingDisabled},
	})
	reasoning := body["reasoning"].(map[string]any)
	if reasoning["effort"] != "none" {
		t.Fatalf("reasoning = %#v", reasoning)
	}

	p, err := New(compat.Config{APIKey: "key", BaseURL: "https://openrouter.test", HTTPClient: roundTripFunc(nil)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = p.Chat(context.Background(), &core.Request{
		Model:    "anthropic/claude-sonnet-4",
		Messages: []core.Message{core.UserText("hi")},
		Thinking: &core.Thinking{Mode: core.ThinkingEnabled, Effort: "extreme"},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported reasoning effort") {
		t.Fatalf("expected effort error, got %v", err)
	}
}

func TestCacheRetentionValidation(t *testing.T) {
	p, err := New(compat.Config{APIKey: "key", BaseURL: "https://openrouter.test", HTTPClient: roundTripFunc(nil)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = p.Chat(context.Background(), &core.Request{
		Model:           "anthropic/claude-sonnet-4",
		Messages:        []core.Message{core.UserText("hi")},
		ProviderOptions: core.ProviderOptions{"cache_retention": "forever"},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported cache_retention") {
		t.Fatalf("expected cache retention error, got %v", err)
	}

	_, err = p.Chat(context.Background(), &core.Request{
		Model:           "openai/gpt-4o-mini",
		Messages:        []core.Message{core.UserText("hi")},
		ProviderOptions: core.ProviderOptions{"cache_retention": "1h"},
	})
	if err == nil || !strings.Contains(err.Error(), "only supported for anthropic models") {
		t.Fatalf("expected non-anthropic cache error, got %v", err)
	}
}

func TestSessionIDProviderOption(t *testing.T) {
	body := captureBody(t, nil, nil, &core.Request{
		Model:    "anthropic/claude-sonnet-4",
		Messages: []core.Message{core.UserText("hi")},
		ProviderOptions: core.ProviderOptions{
			ProviderOptionSessionID:      "agent-session",
			ProviderOptionPromptCacheKey: "nusashell_cv_0123456789012345678",
		},
	})
	if body["session_id"] != "agent-session" {
		t.Fatalf("body = %#v", body)
	}
	if body["prompt_cache_key"] != "nusashell_cv_0123456789012345678" {
		t.Fatalf("prompt_cache_key = %#v", body["prompt_cache_key"])
	}

	p, err := New(compat.Config{APIKey: "key", BaseURL: "https://openrouter.test", HTTPClient: roundTripFunc(nil)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = p.Chat(context.Background(), &core.Request{
		Model:           "anthropic/claude-sonnet-4",
		Messages:        []core.Message{core.UserText("hi")},
		ProviderOptions: core.ProviderOptions{ProviderOptionSessionID: strings.Repeat("x", 257)},
	})
	if err == nil || !strings.Contains(err.Error(), "at most 256") {
		t.Fatalf("expected session_id length error, got %v", err)
	}
}

func TestDelegatedAPIsSendSessionIDAsOpenRouterHeader(t *testing.T) {
	const sessionID = "nusashell_bg_0123456789012345678"
	for _, tc := range []struct {
		name      string
		api       string
		model     string
		maxTokens *int
		response  string
	}{
		{
			name:  "messages",
			api:   APIMessages,
			model: "anthropic/claude-sonnet-4",
			maxTokens: func() *int {
				v := 128
				return &v
			}(),
			response: `{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`,
		},
		{
			name:     "responses",
			api:      APIResponses,
			model:    "openai/gpt-5.6",
			response: `{"model":"openai/gpt-5.6","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotSessionID string
			provider, err := NewForAPI(compat.Config{
				APIKey:  "key",
				BaseURL: "https://openrouter.test",
				HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					gotSessionID = req.Header.Get("x-session-id")
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(tc.response)),
						Header:     make(http.Header),
					}, nil
				}),
			}, tc.api)
			if err != nil {
				t.Fatalf("NewForAPI: %v", err)
			}
			_, err = provider.Chat(context.Background(), &core.Request{
				Model:     tc.model,
				MaxTokens: tc.maxTokens,
				Messages:  []core.Message{core.UserText("hi")},
				ProviderOptions: core.ProviderOptions{
					ProviderOptionSessionID: sessionID,
				},
			})
			if err != nil {
				t.Fatalf("Chat: %v", err)
			}
			if gotSessionID != sessionID {
				t.Fatalf("x-session-id = %q, want %q", gotSessionID, sessionID)
			}
		})
	}
}

func TestBlockCacheValidation(t *testing.T) {
	_, _, _, err := mapBlocks([]core.Block{
		core.TextBlock{
			Text:  "hi",
			Cache: &core.CacheControl{Type: core.CacheTypeEphemeral, TTL: "24h"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported cache ttl") {
		t.Fatalf("expected cache ttl error, got %v", err)
	}

	_, _, _, err = mapBlocks([]core.Block{
		core.TextBlock{
			Text:  "hi",
			Cache: &core.CacheControl{Type: "persistent"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported cache type") {
		t.Fatalf("expected cache type error, got %v", err)
	}
}

func TestResponseReasoningBlocksRoundTrip(t *testing.T) {
	var capturedBody map[string]any
	p, err := New(compat.Config{
		APIKey:  "key",
		BaseURL: "https://openrouter.test",
		HTTPClient: roundTripFunc(func(httpReq *http.Request) (*http.Response, error) {
			if err := json.NewDecoder(httpReq.Body).Decode(&capturedBody); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"model":"anthropic/claude-sonnet-4","choices":[{"message":{"content":"ok","reasoning":"think"}}]}`)), Header: make(http.Header)}, nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := p.Chat(context.Background(), &core.Request{
		Model:    "anthropic/claude-sonnet-4",
		Messages: []core.Message{core.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Reasoning() != "think" {
		t.Fatalf("reasoning = %q", resp.Reasoning())
	}

	_, err = p.Chat(context.Background(), &core.Request{
		Model:    "anthropic/claude-sonnet-4",
		Messages: []core.Message{core.Assistant(resp.Blocks...)},
	})
	if err != nil {
		t.Fatalf("round-trip Chat: %v", err)
	}
	message := capturedBody["messages"].([]any)[0].(map[string]any)
	if message["reasoning"] != "think" {
		t.Fatalf("message reasoning = %#v", message)
	}
}

func TestReasoningDetailsRoundTrip(t *testing.T) {
	var capturedBody map[string]any
	p, err := New(compat.Config{
		APIKey:  "key",
		BaseURL: "https://openrouter.test",
		HTTPClient: roundTripFunc(func(httpReq *http.Request) (*http.Response, error) {
			if err := json.NewDecoder(httpReq.Body).Decode(&capturedBody); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{
				"model":"anthropic/claude-sonnet-4",
				"choices":[{
					"message":{
						"content":"ok",
						"reasoning_details":[
							{"type":"reasoning.summary","summary":"sum"},
							{"type":"reasoning.encrypted","data":"cipher","format":"anthropic-claude-v1"}
						]
					}
				}]
			}`)), Header: make(http.Header)}, nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := p.Chat(context.Background(), &core.Request{
		Model:    "anthropic/claude-sonnet-4",
		Messages: []core.Message{core.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Reasoning() != "sum" {
		t.Fatalf("reasoning = %q", resp.Reasoning())
	}
	_, err = p.Chat(context.Background(), &core.Request{
		Model:    "anthropic/claude-sonnet-4",
		Messages: []core.Message{core.Assistant(resp.Blocks...)},
	})
	if err != nil {
		t.Fatalf("round-trip Chat: %v", err)
	}
	message := capturedBody["messages"].([]any)[0].(map[string]any)
	if _, hasPlain := message["reasoning"]; hasPlain {
		t.Fatalf("message should preserve reasoning_details, got %#v", message)
	}
	details := message["reasoning_details"].([]any)
	if len(details) != 2 || details[1].(map[string]any)["data"] != "cipher" {
		t.Fatalf("reasoning_details = %#v", details)
	}
}

func TestRejectsSignedOrRedactedReasoningBlockHistory(t *testing.T) {
	_, _, _, err := mapBlocks([]core.Block{
		core.ReasoningBlock{Text: "think", Signature: "sig"},
	})
	if err == nil || !strings.Contains(err.Error(), "signed or redacted") {
		t.Fatalf("expected signed reasoning error, got %v", err)
	}

	_, _, _, err = mapBlocks([]core.Block{
		core.ReasoningBlock{Text: "think", Extra: core.MustJSONRaw(map[string]any{"provider": "state"})},
	})
	if err == nil || !strings.Contains(err.Error(), "valid JSON array") {
		t.Fatalf("expected reasoning_details shape error, got %v", err)
	}
}

func TestRejectsEmptyReasoningBlockOnReplay(t *testing.T) {
	_, _, _, err := mapBlocks([]core.Block{
		core.ReasoningBlock{Text: ""},
	})
	if err == nil || !strings.Contains(err.Error(), "empty text") {
		t.Fatalf("expected empty reasoning error, got %v", err)
	}
}

func TestUsageIncludesCacheWriteTokens(t *testing.T) {
	p, err := New(compat.Config{
		APIKey:  "key",
		BaseURL: "https://openrouter.test",
		HTTPClient: roundTripFunc(func(httpReq *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{
				"choices":[{"message":{"content":"ok"}}],
				"usage":{
					"prompt_tokens":10,
					"completion_tokens":1,
					"total_tokens":11,
					"prompt_tokens_details":{"cached_tokens":6,"cache_write_tokens":4}
				}
			}`)), Header: make(http.Header)}, nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := p.Chat(context.Background(), &core.Request{
		Model:    "anthropic/claude-sonnet-4",
		Messages: []core.Message{core.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Usage.CacheReadTokens != 6 || resp.Usage.CacheWriteTokens != 4 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func TestJSONSchemaCleaned(t *testing.T) {
	format, err := core.NewResponseFormatJSONSchema("answer", "", map[string]any{
		"type":       "object",
		"properties": map[string]any{"ok": map[string]any{"type": "boolean"}},
	}, core.StrictEnabled)
	if err != nil {
		t.Fatalf("NewResponseFormatJSONSchema: %v", err)
	}
	body := captureBody(t, nil, nil, &core.Request{
		Model:          "openai/gpt-4o-mini",
		Messages:       []core.Message{core.UserText("hi")},
		ResponseFormat: format,
		ProviderOptions: core.ProviderOptions{ProviderOptionRouting: map[string]any{
			"order":              []any{"OpenAI"},
			"require_parameters": false,
		}},
	})
	schema := body["response_format"].(map[string]any)["json_schema"].(map[string]any)["schema"].(map[string]any)
	if schema["additionalProperties"] != false {
		t.Fatalf("schema should be cleaned: %#v", schema)
	}
	provider := body["provider"].(map[string]any)
	if provider["require_parameters"] != true {
		t.Fatalf("structured output must require compatible upstream parameters: %#v", provider)
	}
	if order := provider["order"].([]any); len(order) != 1 || order[0] != "OpenAI" {
		t.Fatalf("provider routing preferences were not preserved: %#v", provider)
	}
}

func TestNonStrictJSONSchemaPreservesAdditionalProperties(t *testing.T) {
	format, err := core.NewResponseFormatJSONSchema("answer", "", map[string]any{
		"type":                 "object",
		"additionalProperties": true,
		"properties":           map[string]any{"ok": map[string]any{"type": "boolean"}},
	}, core.StrictDisabled)
	if err != nil {
		t.Fatalf("NewResponseFormatJSONSchema: %v", err)
	}
	body := captureBody(t, nil, nil, &core.Request{
		Model:          "openai/gpt-4o-mini",
		Messages:       []core.Message{core.UserText("hi")},
		ResponseFormat: format,
	})
	schema := body["response_format"].(map[string]any)["json_schema"].(map[string]any)["schema"].(map[string]any)
	if schema["additionalProperties"] != true {
		t.Fatalf("non-strict schema was changed: %#v", schema)
	}
}

func TestStrictToolsAreForwarded(t *testing.T) {
	tool, err := core.NewTool("lookup", "Lookup.", map[string]any{"type": "object"})
	if err != nil {
		t.Fatalf("NewTool: %v", err)
	}
	tool.Strict = core.StrictEnabled
	req := &core.Request{
		Model:    "anthropic/claude-sonnet-4.5",
		Messages: []core.Message{core.UserText("hi")},
		Tools:    []core.Tool{tool},
	}
	body := captureBody(t, nil, nil, req)
	fn := body["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)
	if fn["strict"] != true {
		t.Fatalf("strict = %#v, want true", fn["strict"])
	}
	headers := make(http.Header)
	headers.Set("x-anthropic-beta", "interleaved-thinking-2025-05-14")
	mapHeaders(headers, req)
	if got := headers.Get("x-anthropic-beta"); got != "interleaved-thinking-2025-05-14,"+structuredOutputsBeta {
		t.Fatalf("x-anthropic-beta = %q", got)
	}

	p, err := New(compat.Config{APIKey: "key", BaseURL: "https://openrouter.test", HTTPClient: roundTripFunc(nil)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := p.Capabilities("openai/gpt-4o-mini").Tools.StrictSchema; got != core.SupportPartial {
		t.Fatalf("strict tool support = %v, want partial", got)
	}
}

func captureBody(t *testing.T, referer, title *string, req *core.Request) map[string]any {
	t.Helper()
	var body map[string]any
	p, err := New(compat.Config{
		APIKey:  "key",
		BaseURL: "https://openrouter.test",
		HTTPClient: roundTripFunc(func(httpReq *http.Request) (*http.Response, error) {
			if referer != nil {
				*referer = httpReq.Header.Get("HTTP-Referer")
			}
			if title != nil {
				*title = httpReq.Header.Get("X-Title")
			}
			if err := json.NewDecoder(httpReq.Body).Decode(&body); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"ok"}}]}`)), Header: make(http.Header)}, nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := p.Chat(context.Background(), req); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	return body
}

func TestToolResultImageReinjectsAsUserMessage(t *testing.T) {
	body := captureBody(t, nil, nil, &core.Request{
		Model: "openai/gpt-4o",
		Messages: []core.Message{
			core.UserText("read this image"),
			core.Assistant(core.ToolUseBlock{ID: "call_1", Name: "read_media", Arguments: core.MustJSONRaw(map[string]any{"file_path": "/tmp/x.png"})}),
			core.ToolResult("call_1", core.Text("/tmp/x.png"), core.ImageURL("https://example.test/x.png")),
		},
	})
	msgs := body["messages"].([]any)
	// Expect: user, assistant(tool_call), tool(text-only), user(image)
	if len(msgs) != 4 {
		t.Fatalf("got %d messages, want 4 (tool + reinjected user): %+v", len(msgs), msgs)
	}
	toolMsg, _ := msgs[2].(map[string]any)
	if toolMsg["role"] != "tool" {
		t.Fatalf("msgs[2] role = %v, want tool", toolMsg["role"])
	}
	if toolMsg["content"] != "/tmp/x.png" {
		t.Fatalf("tool content = %v, want path-only text", toolMsg["content"])
	}
	reinject, _ := msgs[3].(map[string]any)
	if reinject["role"] != "user" {
		t.Fatalf("msgs[3] role = %v, want user (reinject)", reinject["role"])
	}
	parts := reinject["content"].([]any)
	if len(parts) != 1 {
		t.Fatalf("reinject parts = %d, want 1", len(parts))
	}
	imgPart, _ := parts[0].(map[string]any)
	if imgPart["type"] != "image_url" {
		t.Fatalf("reinject part type = %v, want image_url", imgPart["type"])
	}
}

func TestToolResultsStayContiguousBeforeMediaReinjection(t *testing.T) {
	body := captureBody(t, nil, nil, &core.Request{
		Model: "deepseek/deepseek-v4-flash-vision-exp",
		Messages: []core.Message{
			core.UserText("inspect the screenshot and summarize it"),
			core.Assistant(
				core.ToolUseBlock{ID: "call_image", Name: "read_media", Arguments: core.MustJSONRaw(map[string]any{})},
				core.ToolUseBlock{ID: "call_summary", Name: "memory", Arguments: core.MustJSONRaw(map[string]any{})},
			),
			core.ToolResult("call_image", core.Text("/tmp/screenshot.png"), core.ImageURL("https://example.test/screenshot.png")),
			core.ToolResultText("call_summary", "summary"),
		},
	})

	msgs := body["messages"].([]any)
	if len(msgs) != 5 {
		t.Fatalf("got %d messages, want user + assistant + 2 tools + media user: %+v", len(msgs), msgs)
	}
	if role := msgs[2].(map[string]any)["role"]; role != "tool" {
		t.Fatalf("msgs[2] role = %v, want tool", role)
	}
	if id := msgs[2].(map[string]any)["tool_call_id"]; id != "call_image" {
		t.Fatalf("msgs[2] tool_call_id = %v, want call_image", id)
	}
	if role := msgs[3].(map[string]any)["role"]; role != "tool" {
		t.Fatalf("msgs[3] role = %v, want tool before media reinjection", role)
	}
	if id := msgs[3].(map[string]any)["tool_call_id"]; id != "call_summary" {
		t.Fatalf("msgs[3] tool_call_id = %v, want call_summary", id)
	}
	if role := msgs[4].(map[string]any)["role"]; role != "user" {
		t.Fatalf("msgs[4] role = %v, want user media reinjection", role)
	}
}

func TestToolResultTextOnlyNoReinject(t *testing.T) {
	body := captureBody(t, nil, nil, &core.Request{
		Model: "openai/gpt-4o",
		Messages: []core.Message{
			core.UserText("hi"),
			core.Assistant(core.ToolUseBlock{ID: "call_1", Name: "lookup", Arguments: core.MustJSONRaw(map[string]any{"q": "x"})}),
			core.ToolResultText("call_1", "ok"),
		},
	})
	msgs := body["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3 (no reinject for text-only result): %+v", len(msgs), msgs)
	}
}
