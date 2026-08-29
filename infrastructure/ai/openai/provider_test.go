package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"nusashell/infrastructure/ai/core"
	"nusashell/infrastructure/ai/internal/testgolden"
	"nusashell/infrastructure/ai/retry"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestBuildRequestTextImageToolsAndOptions(t *testing.T) {
	provider := mustProvider(t)
	maxTokens := 256
	temp := 0.2
	tool := mustTool(t, "lookup", "Lookup data.", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"q": map[string]any{"type": "string"},
		},
		"required": []string{"q"},
	})
	tool.Strict = core.StrictEnabled

	wire, err := provider.buildRequest(&core.Request{
		Model:       "gpt-4.1",
		MaxTokens:   &maxTokens,
		Temperature: &temp,
		Messages: []core.Message{
			core.System("You are helpful."),
			core.User(
				core.Text("describe"),
				core.ImageURL("https://example.test/image.png"),
			),
			core.Assistant(core.ToolUseBlock{
				ID:        "call_1",
				Name:      "lookup",
				Arguments: core.MustJSONRaw(map[string]any{"q": "x"}),
			}),
			core.ToolResultText("call_1", "result"),
		},
		Tools: []core.Tool{tool},
		ProviderOptions: core.ProviderOptions{
			"frequency_penalty": 0.4,
			"metadata":          map[string]any{"tenant": "acme"},
			"modalities":        []any{"text"},
		},
	}, false)
	if err != nil {
		t.Fatalf("buildRequest returned error: %v", err)
	}
	testgolden.AssertJSON(t, "../../testdata/openai/chat_request_basic.golden.json", wire)

	data, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("marshal wire: %v", err)
	}
	jsonText := string(data)
	for _, want := range []string{
		`"max_tokens":256`,
		`"temperature":0.2`,
		`"type":"image_url"`,
		`"tool_calls"`,
		`"tool_call_id":"call_1"`,
		`"strict":true`,
		`"additionalProperties":false`,
		`"frequency_penalty":0.4`,
		`"metadata":{"tenant":"acme"}`,
	} {
		if !strings.Contains(jsonText, want) {
			t.Fatalf("wire JSON missing %s:\n%s", want, jsonText)
		}
	}
}

func TestStrictSchemaRejectsConst(t *testing.T) {
	_, err := normalizeStrictSchema(map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"kind": map[string]any{"type": "string", "const": "answer"},
		},
		"required": []any{"kind"},
	})
	if err == nil || !strings.Contains(err.Error(), "const is not supported") {
		t.Fatalf("expected const schema error, got %v", err)
	}
}

func TestBuildRequestOpenAIProviderOptions(t *testing.T) {
	provider := mustProvider(t)
	wire, err := provider.buildRequest(&core.Request{
		Model:    "gpt-4o-audio-preview",
		Messages: []core.Message{core.UserText("hi")},
		ProviderOptions: core.ProviderOptions{
			ProviderOptionAudio: map[string]any{
				"format": "mp3",
				"voice":  "alloy",
			},
			ProviderOptionModeration:        map[string]any{"model": "omni-moderation-latest"},
			ProviderOptionModalities:        []any{"text", "audio"},
			ProviderOptionParallelToolCalls: true,
			ProviderOptionSafetyIdentifier:  "safe-user",
			ProviderOptionVerbosity:         "low",
			ProviderOptionWebSearchOptions: map[string]any{
				"search_context_size": "low",
			},
		},
	}, false)
	if err != nil {
		t.Fatalf("buildRequest returned error: %v", err)
	}
	data, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("marshal wire: %v", err)
	}
	jsonText := string(data)
	for _, want := range []string{
		`"audio":{"format":"mp3","voice":"alloy"}`,
		`"moderation":{"model":"omni-moderation-latest"}`,
		`"modalities":["text","audio"]`,
		`"parallel_tool_calls":true`,
		`"safety_identifier":"safe-user"`,
		`"verbosity":"low"`,
		`"web_search_options":{"search_context_size":"low"}`,
	} {
		if !strings.Contains(jsonText, want) {
			t.Fatalf("wire JSON missing %s:\n%s", want, jsonText)
		}
	}
}

func TestBuildRequestRequiresSingleOutput(t *testing.T) {
	provider := mustProvider(t)
	req := &core.Request{
		Model:           "gpt-4.1",
		Messages:        []core.Message{core.UserText("hi")},
		ProviderOptions: core.ProviderOptions{ProviderOptionN: 1},
	}
	wire, err := provider.buildRequest(req, false)
	if err != nil {
		t.Fatalf("buildRequest returned error for n=1: %v", err)
	}
	if wire.N == nil || *wire.N != 1 {
		t.Fatalf("n = %v, want 1", wire.N)
	}

	req.ProviderOptions[ProviderOptionN] = 2
	_, err = provider.buildRequest(req, false)
	if err == nil || !strings.Contains(err.Error(), `provider option "n" must be 1`) {
		t.Fatalf("expected single-output error, got %v", err)
	}
}

func TestBuildRequestRoundTripsTextReasoningBlockHistory(t *testing.T) {
	provider := mustProvider(t)
	wire, err := provider.buildRequest(&core.Request{
		Model: "gpt-4.1",
		Messages: []core.Message{
			core.Assistant(
				core.ReasoningBlock{Text: "hidden state"},
				core.ToolUseBlock{ID: "call_1", Name: "lookup", Arguments: core.MustJSONRaw(map[string]any{})},
			),
		},
	}, false)
	if err != nil {
		t.Fatalf("buildRequest returned error: %v", err)
	}
	if len(wire.Messages) != 1 || wire.Messages[0].ReasoningContent != "hidden state" {
		t.Fatalf("messages = %#v", wire.Messages)
	}
}

func TestBuildRequestRejectsOpaqueReasoningBlockHistory(t *testing.T) {
	provider := mustProvider(t)
	_, err := provider.buildRequest(&core.Request{
		Model: "gpt-4.1",
		Messages: []core.Message{
			core.Assistant(core.ReasoningBlock{Text: "hidden state", Signature: "sig"}),
		},
	}, false)
	if err == nil || !strings.Contains(err.Error(), "does not accept signed") {
		t.Fatalf("expected opaque reasoning block error, got %v", err)
	}
}

func TestBuildRequestRejectsUnknownProviderOption(t *testing.T) {
	provider := mustProvider(t)
	_, err := provider.buildRequest(&core.Request{
		Model:           "gpt-4.1",
		Messages:        []core.Message{core.UserText("hi")},
		ProviderOptions: core.ProviderOptions{"unknown": true},
	}, false)
	if err == nil || !strings.Contains(err.Error(), "unsupported provider option") {
		t.Fatalf("expected provider option error, got %v", err)
	}
}

func TestBuildRequestRejectsInvalidPromptCacheRetention(t *testing.T) {
	provider := mustProvider(t)
	_, err := provider.buildRequest(&core.Request{
		Model:    "gpt-4.1",
		Messages: []core.Message{core.UserText("hi")},
		ProviderOptions: core.ProviderOptions{
			"prompt_cache_retention": "forever",
		},
	}, false)
	if err == nil || !strings.Contains(err.Error(), "prompt_cache_retention") {
		t.Fatalf("expected prompt cache retention error, got %v", err)
	}
}

func TestBuildStreamRequestAcceptsStreamOptions(t *testing.T) {
	provider := mustProvider(t)
	includeObfuscation := false
	wire, err := provider.buildRequest(&core.Request{
		Model:    "gpt-4.1",
		Messages: []core.Message{core.UserText("hi")},
		ProviderOptions: core.ProviderOptions{
			ProviderOptionStreamOptions: map[string]any{
				"include_usage":       true,
				"include_obfuscation": includeObfuscation,
			},
		},
	}, true)
	if err != nil {
		t.Fatalf("buildRequest returned error: %v", err)
	}
	if wire.StreamOptions == nil || !wire.StreamOptions.IncludeUsage || wire.StreamOptions.IncludeObfuscation == nil || *wire.StreamOptions.IncludeObfuscation {
		t.Fatalf("stream_options = %#v", wire.StreamOptions)
	}

	_, err = provider.buildRequest(&core.Request{
		Model:    "gpt-4.1",
		Messages: []core.Message{core.UserText("hi")},
		ProviderOptions: core.ProviderOptions{
			ProviderOptionStreamOptions: map[string]any{"include_obfuscation": false},
		},
	}, false)
	if err == nil || !strings.Contains(err.Error(), "requires stream request") {
		t.Fatalf("expected non-stream stream_options error, got %v", err)
	}
}

func TestChatReturnsStructuredValidationError(t *testing.T) {
	provider := mustProvider(t)
	_, err := provider.Chat(context.Background(), &core.Request{
		Model:           "gpt-4.1",
		Messages:        []core.Message{core.UserText("hi")},
		ProviderOptions: core.ProviderOptions{"unknown": true},
	})
	if err == nil || !core.IsValidationError(err) {
		t.Fatalf("expected structured validation error, got %v", err)
	}
}

func TestBuildRequestReasoningModelConstraints(t *testing.T) {
	provider := mustProvider(t)
	// NusaShell sends effort to any model: gateways (OpenRouter, DeepSeek,
	// GLM) accept reasoning_effort on non-gpt-5 models, so the upstream
	// "thinking only for reasoning chat models" gate is intentionally not
	// ported. The effort must pass through regardless of model name.
	wire0, err := provider.buildRequest(&core.Request{
		Model:    "gpt-4.1",
		Messages: []core.Message{core.UserText("hi")},
		Thinking: &core.Thinking{Mode: core.ThinkingEnabled, Effort: "medium"},
	}, false)
	if err != nil {
		t.Fatalf("non-reasoning model with effort must not error: %v", err)
	}
	if wire0.ReasoningEffort != "medium" {
		t.Fatalf("reasoning_effort = %q, want medium", wire0.ReasoningEffort)
	}

	temp := 1.0
	wire, err := provider.buildRequest(&core.Request{
		Model:       "gpt-5.1",
		Temperature: &temp,
		Messages:    []core.Message{core.UserText("hi")},
	}, false)
	if err != nil || wire.Temperature == nil || *wire.Temperature != temp {
		t.Fatalf("temperature was not preserved: wire=%#v err=%v", wire, err)
	}

	_, err = provider.buildRequest(&core.Request{
		Model:    "gpt-5.1",
		Messages: []core.Message{core.UserText("hi")},
		Thinking: &core.Thinking{Mode: core.ThinkingEnabled, Effort: "minimal"},
	}, false)
	if err == nil || !strings.Contains(err.Error(), `unsupported reasoning_effort "minimal"`) {
		t.Fatalf("expected minimal effort error, got %v", err)
	}

	wire, err = provider.buildRequest(&core.Request{
		Model:    "openai/gpt-5.1",
		Messages: []core.Message{core.UserText("hi")},
		Thinking: &core.Thinking{
			Mode:   core.ThinkingEnabled,
			Effort: "medium",
		},
	}, false)
	if err != nil {
		t.Fatalf("buildRequest reasoning model: %v", err)
	}
	if wire.ReasoningEffort != "medium" {
		t.Fatalf("reasoning_effort = %q, want medium", wire.ReasoningEffort)
	}

	wire, err = provider.buildRequest(&core.Request{
		Model:    "gpt-5.1",
		Messages: []core.Message{core.UserText("hi")},
		Thinking: &core.Thinking{Mode: core.ThinkingDisabled},
	}, false)
	if err != nil {
		t.Fatalf("buildRequest disabled reasoning model: %v", err)
	}
	if wire.ReasoningEffort != "none" {
		t.Fatalf("disabled reasoning_effort = %q, want none", wire.ReasoningEffort)
	}

	topP := 0.9
	wire, err = provider.buildRequest(&core.Request{
		Model:    "gpt-5.6",
		TopP:     &topP,
		Messages: []core.Message{core.UserText("hi")},
		Thinking: &core.Thinking{
			Mode:   core.ThinkingEnabled,
			Effort: "xhigh",
		},
	}, false)
	if err != nil {
		t.Fatalf("buildRequest xhigh reasoning model: %v", err)
	}
	if wire.ReasoningEffort != "xhigh" || wire.TopP == nil || *wire.TopP != topP {
		t.Fatalf("reasoning_effort/top_p = %q/%v", wire.ReasoningEffort, wire.TopP)
	}

	wire, err = provider.buildRequest(&core.Request{
		Model:    "gpt-5.7",
		Messages: []core.Message{core.UserText("hi")},
		Thinking: &core.Thinking{Mode: core.ThinkingDisabled},
	}, false)
	if err != nil || wire.ReasoningEffort != "none" {
		t.Fatalf("future model disabled reasoning = %#v, err %v", wire, err)
	}

	wire, err = provider.buildRequest(&core.Request{
		Model:          "gpt-5.7",
		Messages:       []core.Message{core.UserText("hi")},
		ResponseFormat: &core.ResponseFormat{Type: core.ResponseFormatJSONObject},
	}, false)
	if err != nil || wire.ResponseFormat == nil {
		t.Fatalf("future model structured output = %#v, err %v", wire, err)
	}

	if _, err = provider.buildRequest(&core.Request{
		Model:    "gpt-5.7",
		Messages: []core.Message{core.UserText("hi")},
	}, true); err != nil {
		t.Fatalf("future model streaming: %v", err)
	}
}

func TestBuildRequestMapsPromptCacheOptions(t *testing.T) {
	provider := mustProvider(t)
	wire, err := provider.buildRequest(&core.Request{
		Model:    "gpt-5.6",
		Messages: []core.Message{core.UserText("hi")},
		ProviderOptions: core.ProviderOptions{
			ProviderOptionPromptCacheOptions: map[string]any{"mode": "explicit", "ttl": "30m"},
		},
	}, false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if wire.PromptCacheOptions == nil || wire.PromptCacheOptions.Mode != "explicit" || wire.PromptCacheOptions.TTL != "30m" {
		t.Fatalf("prompt_cache_options = %#v", wire.PromptCacheOptions)
	}

	wire, err = provider.buildRequest(&core.Request{
		Model:    "gpt-5.7",
		Messages: []core.Message{core.UserText("hi")},
		ProviderOptions: core.ProviderOptions{
			ProviderOptionPromptCacheOptions: map[string]any{"mode": "implicit"},
		},
	}, false)
	if err != nil || wire.PromptCacheOptions == nil || wire.PromptCacheOptions.Mode != "implicit" {
		t.Fatalf("future model prompt_cache_options = %#v, err %v", wire.PromptCacheOptions, err)
	}
}

func TestBuildRequestMapsPromptCacheBreakpoint(t *testing.T) {
	provider := mustProvider(t)
	wire, err := provider.buildRequest(&core.Request{
		Model: "gpt-5.6",
		Messages: []core.Message{core.User(core.TextBlock{
			Text:  "stable prefix",
			Cache: &core.CacheControl{Type: core.CacheTypeEphemeral},
		})},
	}, false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	parts, ok := wire.Messages[0].Content.([]contentPart)
	if !ok || len(parts) != 1 || parts[0].PromptCacheBreakpoint == nil || parts[0].PromptCacheBreakpoint.Mode != "explicit" {
		t.Fatalf("content = %#v", wire.Messages[0].Content)
	}

	_, err = provider.buildRequest(&core.Request{
		Model: "gpt-5.6",
		Messages: []core.Message{core.User(core.TextBlock{
			Text:  "stable prefix",
			Cache: &core.CacheControl{Type: core.CacheTypeEphemeral, TTL: core.CacheTTL5m},
		})},
	}, false)
	if err == nil || !strings.Contains(err.Error(), "prompt_cache_options.ttl") {
		t.Fatalf("expected cache TTL error, got %v", err)
	}
}

func TestChatRetriesWhenRetryPolicyIsConfigured(t *testing.T) {
	var attempts int
	provider, err := New(Config{
		APIKey: "test-key",
		Retry:  &retry.Policy{MaxAttempts: 2, InitialDelay: 1},
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			if attempts == 1 {
				return jsonResponse(http.StatusTooManyRequests, `{"error":"retry"}`), nil
			}
			return jsonResponse(http.StatusOK, `{
				"model":"gpt-4.1",
				"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]
			}`), nil
		}),
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	resp, err := provider.Chat(context.Background(), &core.Request{
		Model:    "gpt-4.1",
		Messages: []core.Message{core.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if attempts != 2 || resp.Text() != "ok" {
		t.Fatalf("attempts/text = %d/%q", attempts, resp.Text())
	}
}

func TestNewRejectsUnknownAPI(t *testing.T) {
	_, err := New(Config{APIKey: "test-key", API: "legacy"})
	if err == nil || !strings.Contains(err.Error(), "api must be chat or responses") {
		t.Fatalf("expected api validation error, got %v", err)
	}
}

func TestDefaultAPIRoutesChatToCompletionsEndpoint(t *testing.T) {
	var capturedPath string
	provider, err := New(Config{
		APIKey:  "test-key",
		BaseURL: "https://example.test",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			capturedPath = req.URL.Path
			return jsonResponse(http.StatusOK, `{
				"model":"gpt-4.1",
				"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]
			}`), nil
		}),
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = provider.Chat(context.Background(), &core.Request{
		Model:    "gpt-4.1",
		Messages: []core.Message{core.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if capturedPath != "/v1/chat/completions" {
		t.Fatalf("path = %q, want /v1/chat/completions", capturedPath)
	}
}

func TestChatSetsProviderHeaders(t *testing.T) {
	provider, err := New(Config{
		APIKey:    "test-key",
		BaseURL:   "https://example.test",
		UserAgent: "codex-cli/0.142.3",
		Headers: map[string]string{
			"Originator": "Codex CLI",
			"Session_id": "ainovel",
		},
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if got := req.Header.Get("User-Agent"); got != "codex-cli/0.142.3" {
				t.Fatalf("User-Agent = %q", got)
			}
			if got := req.Header.Get("Originator"); got != "Codex CLI" {
				t.Fatalf("Originator = %q", got)
			}
			if got := req.Header.Get("Session_id"); got != "ainovel" {
				t.Fatalf("Session_id = %q", got)
			}
			return jsonResponse(http.StatusOK, `{
				"model":"gpt-5.4",
				"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]
			}`), nil
		}),
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	resp, err := provider.Chat(context.Background(), &core.Request{
		Model:    "gpt-5.4",
		Messages: []core.Message{core.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if resp.Text() != "ok" {
		t.Fatalf("Text = %q", resp.Text())
	}
}

func TestNewRejectsAmbiguousTransportConfig(t *testing.T) {
	_, err := New(Config{
		APIKey:     "test-key",
		HTTPClient: roundTripFunc(nil),
		Transport:  roundTripperFunc(nil),
	})
	if err == nil || !strings.Contains(err.Error(), "HTTPClient and Transport are mutually exclusive") {
		t.Fatalf("expected HTTPClient/Transport error, got %v", err)
	}

	_, err = New(Config{
		APIKey:     "test-key",
		HTTPClient: roundTripFunc(nil),
		Retry:      retry.DefaultPolicy(),
	})
	if err == nil || !strings.Contains(err.Error(), "Retry cannot be used with a custom HTTPClient") {
		t.Fatalf("expected HTTPClient/Retry error, got %v", err)
	}
}

func TestChatConvertsResponseBlocks(t *testing.T) {
	var capturedAuth string
	provider, err := New(Config{
		APIKey:  "test-key",
		BaseURL: "https://example.test",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			capturedAuth = req.Header.Get("Authorization")
			return jsonResponse(200, `{
				"model":"gpt-4.1",
				"choices":[{
					"message":{
						"content":"hello",
						"tool_calls":[{
							"id":"call_1",
							"type":"function",
							"function":{"name":"lookup","arguments":"{\"q\":\"x\"}"}
						}],
						"reasoning_content":"thought"
					},
					"finish_reason":"tool_calls"
				}],
				"usage":{
					"prompt_tokens":10,
					"completion_tokens":5,
					"total_tokens":15,
					"prompt_tokens_details":{"cached_tokens":3,"cache_write_tokens":4},
					"completion_tokens_details":{"reasoning_tokens":2}
				}
			}`), nil
		}),
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	resp, err := provider.Chat(context.Background(), &core.Request{
		Model:    "gpt-4.1",
		Messages: []core.Message{core.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if capturedAuth != "Bearer test-key" {
		t.Fatalf("Authorization = %q", capturedAuth)
	}
	if resp.Text() != "hello" || resp.Reasoning() != "thought" {
		t.Fatalf("text/reasoning = %q/%q", resp.Text(), resp.Reasoning())
	}
	calls := resp.ToolCalls()
	if len(calls) != 1 || calls[0].Name != "lookup" || string(calls[0].Arguments) != `{"q":"x"}` {
		t.Fatalf("tool calls = %+v", calls)
	}
	if resp.FinishReason != core.FinishReasonToolCall {
		t.Fatalf("finish reason = %q", resp.FinishReason)
	}
	if resp.Usage.InputTokens != 7 || resp.Usage.OutputTokens != 5 || resp.Usage.CacheReadTokens != 3 || resp.Usage.CacheWriteTokens != 4 || resp.Usage.ReasoningTokens != 2 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func TestConvertResponseRejectsNil(t *testing.T) {
	_, err := convertResponse(nil, &core.Request{Model: "gpt-4.1"})
	if err == nil || !strings.Contains(err.Error(), "response cannot be nil") {
		t.Fatalf("expected nil response error, got %v", err)
	}
}

func TestConvertResponsePreservesRefusal(t *testing.T) {
	resp, err := convertResponse(&chatResponse{
		Model: "gpt-5.1",
		Choices: []choice{{
			Message:      responseMessage{Refusal: "I can't help."},
			FinishReason: "stop",
		}},
	}, nil)
	if err != nil {
		t.Fatalf("convertResponse: %v", err)
	}
	if resp.Refusal != "I can't help." || resp.Text() != resp.Refusal {
		t.Fatalf("refusal/text = %q/%q", resp.Refusal, resp.Text())
	}
	if resp.FinishReason != core.FinishReasonSafety || resp.FinishReasonRaw != "stop" {
		t.Fatalf("finish/raw = %q/%q", resp.FinishReason, resp.FinishReasonRaw)
	}
}

func TestChatRejectsUnsupportedResponseContent(t *testing.T) {
	provider, err := New(Config{
		APIKey:  "test-key",
		BaseURL: "https://example.test",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(200, `{
				"model":"gpt-4.1",
				"choices":[{
					"message":{"content":[{"type":"audio","url":"https://example.test/a.wav"}]},
					"finish_reason":"stop"
				}]
			}`), nil
		}),
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = provider.Chat(context.Background(), &core.Request{
		Model:    "gpt-4.1",
		Messages: []core.Message{core.UserText("hi")},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported content part type") {
		t.Fatalf("expected unsupported content error, got %v", err)
	}
}

func TestChatPreservesInvalidToolCallArguments(t *testing.T) {
	provider, err := New(Config{
		APIKey:  "test-key",
		BaseURL: "https://example.test",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(200, `{
				"model":"gpt-4.1",
				"choices":[{
					"message":{"tool_calls":[{
						"id":"call_1",
						"type":"function",
						"function":{"name":"lookup","arguments":"{\"q\":"}
					}]},
					"finish_reason":"tool_calls"
				}]
			}`), nil
		}),
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	resp, err := provider.Chat(context.Background(), &core.Request{
		Model:    "gpt-4.1",
		Messages: []core.Message{core.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	calls := resp.ToolCalls()
	if len(calls) != 1 {
		t.Fatalf("tool calls len = %d, want 1", len(calls))
	}
	if got := string(calls[0].Arguments); got != `{"q":` {
		t.Fatalf("arguments = %q, want raw malformed args", got)
	}
}

func TestChatDecodeErrorIsStructuredProviderError(t *testing.T) {
	provider, err := New(Config{
		APIKey:  "test-key",
		BaseURL: "https://example.test",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{"choices":[`), nil
		}),
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = provider.Chat(context.Background(), &core.Request{
		Model:    "gpt-4.1",
		Messages: []core.Message{core.UserText("hi")},
	})
	if err == nil || !strings.Contains(err.Error(), "decode response") || !core.IsProviderError(err) {
		t.Fatalf("expected structured decode provider error, got %v", err)
	}
}

func TestStreamEmitsTypedEvents(t *testing.T) {
	provider, err := New(Config{
		APIKey:  "test-key",
		BaseURL: "https://example.test",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return streamResponse(testgolden.ReadFixtureString(t, "../../testdata/openai/chat_stream.sse")), nil
		}),
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	stream, err := provider.Stream(context.Background(), &core.Request{
		Model:    "gpt-4.1",
		Messages: []core.Message{core.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	resp, err := core.Collect(stream)
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}
	if resp.Text() != "hel" || resp.Reasoning() != "think " {
		t.Fatalf("text/reasoning = %q/%q", resp.Text(), resp.Reasoning())
	}
	calls := resp.ToolCalls()
	if len(calls) != 1 || calls[0].ID != "call_1" || calls[0].Name != "lookup" || string(calls[0].Arguments) != `{"q":"x"}` {
		t.Fatalf("tool calls = %+v", calls)
	}
	if resp.Usage.InputTokens != 4 || resp.Usage.OutputTokens != 3 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
	if resp.FinishReason != core.FinishReasonToolCall {
		t.Fatalf("finish reason = %q", resp.FinishReason)
	}
}

func TestStreamRejectsEOFBeforeDone(t *testing.T) {
	stream := newStream(streamResponse(`data: {"choices":[{"delta":{"content":"partial"}}]}`), &core.Request{Model: "gpt-4.1"})
	_, err := core.Collect(stream)
	if err == nil || !strings.Contains(err.Error(), "before [DONE]") || !core.IsProviderError(err) {
		t.Fatalf("expected truncated stream error, got %v", err)
	}
}

func TestStreamPreservesRefusal(t *testing.T) {
	stream := newStream(streamResponse(strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"refusal":"I can't help."}}]}`,
		`data: {"choices":[{"index":0,"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		``,
	}, "\n")), &core.Request{Model: "gpt-5.1"})
	resp, err := core.Collect(stream)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if resp.Refusal != "I can't help." || resp.FinishReason != core.FinishReasonSafety {
		t.Fatalf("refusal/finish = %q/%q", resp.Refusal, resp.FinishReason)
	}
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func streamResponse(body string) *http.Response {
	resp := jsonResponse(200, body)
	resp.Header.Set("Content-Type", "text/event-stream")
	return resp
}

func mustProvider(t *testing.T) *Provider {
	t.Helper()
	provider, err := New(Config{APIKey: "test"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	return provider
}

func mustTool(t *testing.T, name, description string, schema any) core.Tool {
	t.Helper()
	tool, err := core.NewTool(name, description, schema)
	if err != nil {
		t.Fatalf("NewTool: %v", err)
	}
	return tool
}

func TestConvertUsageClampsWhenCachedExceedsPrompt(t *testing.T) {
	u := convertUsage(&usage{
		PromptTokens:     5,
		CompletionTokens: 3,
		TotalTokens:      8,
		PromptTokensDetails: &promptTokensDetails{
			CachedTokens: 10, // exceeds prompt
		},
	}, "model")
	if u.InputTokens != 0 {
		t.Fatalf("InputTokens = %d, want 0 (clamped)", u.InputTokens)
	}
	if u.CacheReadTokens != 10 {
		t.Fatalf("CacheReadTokens = %d, want 10", u.CacheReadTokens)
	}
}

// TestBuildRequestRejectsEmptyReasoningBlockHistory is the guard: if a
// ReasoningBlock reaches the Chat request builder with empty text, no
// signature, and no extra, the reasoning was received from the provider
// (input) but is missing on replay (output). Force an error instead of
// silently sending an empty reasoning_content field.
func TestBuildRequestRejectsEmptyReasoningBlockHistory(t *testing.T) {
	provider := mustProvider(t)
	_, err := provider.buildRequest(&core.Request{
		Model: "gpt-4.1",
		Messages: []core.Message{
			core.Assistant(core.ReasoningBlock{Text: ""}),
		},
	}, false)
	if err == nil || !strings.Contains(err.Error(), "empty text") {
		t.Fatalf("expected empty reasoning error, got %v", err)
	}
}

// TestBuildRequestAcceptsPlaceholderReasoningBlock ensures the placeholder
// sentinel injected by the application layer is accepted and forwarded as
// reasoning_content — the guard only rejects truly empty reasoning.
func TestBuildRequestAcceptsPlaceholderReasoningBlock(t *testing.T) {
	provider := mustProvider(t)
	wire, err := provider.buildRequest(&core.Request{
		Model: "gpt-4.1",
		Messages: []core.Message{
			core.Assistant(core.ReasoningBlock{Text: "(Continue from the current context.)"}),
		},
	}, false)
	if err != nil {
		t.Fatalf("placeholder reasoning must pass, got error: %v", err)
	}
	if wire.Messages[0].ReasoningContent != "(Continue from the current context.)" {
		t.Fatalf("reasoning_content = %q", wire.Messages[0].ReasoningContent)
	}
}
