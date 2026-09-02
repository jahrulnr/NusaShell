package compat

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

func TestChatBuildsRequestAndConvertsResponse(t *testing.T) {
	var capturedBody map[string]any
	provider, err := New(Config{
		APIKey:  "test-key",
		BaseURL: "https://compat.example/v1",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if got := req.Header.Get("Authorization"); got != "Bearer test-key" {
				t.Fatalf("Authorization = %q", got)
			}
			if req.URL.String() != "https://compat.example/v1/chat/completions" {
				t.Fatalf("url = %s", req.URL.String())
			}
			if err := json.NewDecoder(req.Body).Decode(&capturedBody); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			return jsonResponse(http.StatusOK, testgolden.ReadFixtureString(t, "../../testdata/compat/chat_response.json")), nil
		}),
	}, Spec{
		Name: "testcompat",
		Auth: AuthSpec{APIKeyRequired: true},
		Request: RequestSpec{
			SupportsJSONSchema: true,
			AllowedProviderOptions: map[string]struct{}{
				"extra_body": {},
			},
		},
		Response: ResponseSpec{
			ModelFromResponse:         true,
			HasCompletionTokenDetails: true,
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	tool := mustTool(t, "lookup", "Lookup.", map[string]any{"type": "object"})
	resp, err := provider.Chat(context.Background(), &core.Request{
		Model: "m",
		Messages: []core.Message{
			core.User(core.Text("hi"), core.ImageURL("https://example.test/image.png")),
			core.Assistant(core.ToolUseBlock{ID: "call_1", Name: "lookup", Arguments: core.MustJSONRaw(map[string]any{"q": "x"})}),
			core.ToolResultText("call_1", "ok"),
		},
		Tools:           []core.Tool{tool},
		ProviderOptions: core.ProviderOptions{"extra_body": true},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if capturedBody["model"] != "m" || capturedBody["extra_body"] != true {
		t.Fatalf("captured body = %#v", capturedBody)
	}
	if _, ok := capturedBody["messages"].([]any); !ok {
		t.Fatalf("messages not encoded as array: %#v", capturedBody["messages"])
	}
	if resp.Model != "provider-model" || resp.Text() != "hello" || resp.Reasoning() != "think" {
		t.Fatalf("response model/text/reasoning = %q/%q/%q", resp.Model, resp.Text(), resp.Reasoning())
	}
	calls := resp.ToolCalls()
	if len(calls) != 1 || calls[0].ID != "call_1" || calls[0].Name != "lookup" || string(calls[0].Arguments) != `{"q":"x"}` {
		t.Fatalf("tool calls = %+v", calls)
	}
	if resp.Usage.ReasoningTokens != 2 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func TestChatRetriesWhenRetryPolicyIsConfigured(t *testing.T) {
	var attempts int
	provider, err := New(Config{
		BaseURL: "https://compat.example/v1",
		Retry:   &retry.Policy{MaxAttempts: 2, InitialDelay: 1},
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			if attempts == 1 {
				return jsonResponse(http.StatusTooManyRequests, `{"error":"retry"}`), nil
			}
			return jsonResponse(http.StatusOK, `{
				"model":"m",
				"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]
			}`), nil
		}),
	}, Spec{Name: "retrycompat"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	resp, err := provider.Chat(context.Background(), &core.Request{
		Model:    "m",
		Messages: []core.Message{core.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if attempts != 2 || resp.Text() != "ok" {
		t.Fatalf("attempts/text = %d/%q", attempts, resp.Text())
	}
}

func TestNewRejectsAmbiguousTransportConfig(t *testing.T) {
	_, err := New(Config{
		BaseURL:    "https://compat.example/v1",
		HTTPClient: roundTripFunc(nil),
		Transport:  roundTripperFunc(nil),
	}, Spec{Name: "testcompat"})
	if err == nil || !strings.Contains(err.Error(), "HTTPClient and Transport are mutually exclusive") {
		t.Fatalf("expected HTTPClient/Transport error, got %v", err)
	}

	_, err = New(Config{
		BaseURL:    "https://compat.example/v1",
		HTTPClient: roundTripFunc(nil),
		Retry:      retry.DefaultPolicy(),
	}, Spec{Name: "testcompat"})
	if err == nil || !strings.Contains(err.Error(), "Retry cannot be used with a custom HTTPClient") {
		t.Fatalf("expected HTTPClient/Retry error, got %v", err)
	}
}

func TestRejectsUnknownProviderOptionByDefault(t *testing.T) {
	provider, err := New(Config{BaseURL: "https://compat.example", HTTPClient: roundTripFunc(nil)}, Spec{Name: "strict"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = provider.Chat(context.Background(), &core.Request{
		Model:           "m",
		Messages:        []core.Message{core.UserText("hi")},
		ProviderOptions: core.ProviderOptions{"unknown": true},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported provider option") {
		t.Fatalf("expected unknown option error, got %v", err)
	}
}

func TestConfigCanAllowUnknownProviderOptions(t *testing.T) {
	provider, err := New(Config{
		BaseURL:                     "https://compat.example",
		HTTPClient:                  roundTripFunc(nil),
		AllowUnknownProviderOptions: true,
	}, Spec{Name: "passthrough"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	data, _, err := provider.buildRequest(&core.Request{
		Model:           "m",
		Messages:        []core.Message{core.UserText("hi")},
		ProviderOptions: core.ProviderOptions{"min_p": 0.05},
	}, false)
	if err != nil {
		t.Fatalf("buildRequest returned error: %v", err)
	}
	if !strings.Contains(string(data), `"min_p":0.05`) {
		t.Fatalf("body missing passthrough option: %s", data)
	}
}

func TestBuildRequestRequiresSingleOutput(t *testing.T) {
	provider, err := New(Config{
		BaseURL:    "https://compat.example",
		HTTPClient: roundTripFunc(nil),
	}, Spec{
		Name: "testcompat",
		Request: RequestSpec{
			AllowedProviderOptions: map[string]struct{}{"n": {}},
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	req := &core.Request{
		Model:           "m",
		Messages:        []core.Message{core.UserText("hi")},
		ProviderOptions: core.ProviderOptions{"n": 1},
	}
	data, _, err := provider.buildRequest(req, false)
	if err != nil {
		t.Fatalf("buildRequest returned error for n=1: %v", err)
	}
	if !strings.Contains(string(data), `"n":1`) {
		t.Fatalf("body missing n=1: %s", data)
	}

	req.ProviderOptions["n"] = 2
	_, _, err = provider.buildRequest(req, false)
	if err == nil || !strings.Contains(err.Error(), `provider option "n" must be 1`) {
		t.Fatalf("expected single-output error, got %v", err)
	}
}

func TestConfigAllowsUnknownProviderOptionsWithoutBypassingKnownMapper(t *testing.T) {
	provider, err := New(Config{
		BaseURL:                     "https://compat.example",
		HTTPClient:                  roundTripFunc(nil),
		AllowUnknownProviderOptions: true,
	}, Spec{
		Name: "mapped",
		Request: RequestSpec{
			AllowedProviderOptions: map[string]struct{}{"known": {}},
			ProviderOptions: func(options core.ProviderOptions, body map[string]any, _ *core.Request) error {
				for key, value := range options {
					if key != "known" {
						t.Fatalf("mapper saw unknown option %q", key)
					}
					body["mapped_known"] = value
				}
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	data, _, err := provider.buildRequest(&core.Request{
		Model:    "m",
		Messages: []core.Message{core.UserText("hi")},
		ProviderOptions: core.ProviderOptions{
			"known": "typed",
			"min_p": 0.05,
		},
	}, false)
	if err != nil {
		t.Fatalf("buildRequest returned error: %v", err)
	}
	body := string(data)
	for _, want := range []string{`"mapped_known":"typed"`, `"min_p":0.05`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %s: %s", want, body)
		}
	}
}

func TestConfigAllowedUnknownProviderOptionsRejectGeneratedFieldConflict(t *testing.T) {
	provider, err := New(Config{
		BaseURL:                     "https://compat.example",
		HTTPClient:                  roundTripFunc(nil),
		AllowUnknownProviderOptions: true,
	}, Spec{
		Name: "mapped",
		Request: RequestSpec{
			AllowedProviderOptions: map[string]struct{}{"known": {}},
			ProviderOptions: func(options core.ProviderOptions, body map[string]any, _ *core.Request) error {
				body["known"] = options["known"]
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, _, err = provider.buildRequest(&core.Request{
		Model:    "m",
		Messages: []core.Message{core.UserText("hi")},
		ProviderOptions: core.ProviderOptions{
			"known":    "typed",
			"messages": []any{"override"},
		},
	}, false)
	if err == nil || !strings.Contains(err.Error(), `provider option "messages" conflicts with generated request field`) {
		t.Fatalf("expected generated field conflict, got %v", err)
	}
}

func TestProviderOptionsRejectGeneratedFieldConflict(t *testing.T) {
	provider, err := New(Config{
		BaseURL:    "https://compat.example",
		HTTPClient: roundTripFunc(nil),
	}, Spec{
		Name: "passthrough",
		Request: RequestSpec{
			AllowUnknownProviderOptions: true,
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, _, err = provider.buildRequest(&core.Request{
		Model:           "m",
		Messages:        []core.Message{core.UserText("hi")},
		ProviderOptions: core.ProviderOptions{"model": "override"},
	}, false)
	if err == nil || !strings.Contains(err.Error(), `provider option "model" conflicts with generated request field`) {
		t.Fatalf("expected generated field conflict, got %v", err)
	}
}

func TestChatReturnsStructuredValidationError(t *testing.T) {
	provider, err := New(Config{BaseURL: "https://compat.example", HTTPClient: roundTripFunc(nil)}, Spec{Name: "strict"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = provider.Chat(context.Background(), &core.Request{
		Model:           "m",
		Messages:        []core.Message{core.UserText("hi")},
		ProviderOptions: core.ProviderOptions{"unknown": true},
	})
	if err == nil || !core.IsValidationError(err) {
		t.Fatalf("expected structured validation error, got %v", err)
	}
}

func TestRejectsStopSequencesAboveProviderLimit(t *testing.T) {
	provider, err := New(Config{BaseURL: "https://compat.example", HTTPClient: roundTripFunc(nil)}, Spec{
		Name:    "glm",
		Request: RequestSpec{MaxStopSequences: 1},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = provider.Chat(context.Background(), &core.Request{
		Model:    "m",
		Messages: []core.Message{core.UserText("hi")},
		Stop:     []string{"a", "b"},
	})
	if err == nil || !strings.Contains(err.Error(), "stop supports at most 1 sequence") {
		t.Fatalf("expected stop limit error, got %v", err)
	}
}

func TestReasoningBlockHistoryRequiresProviderMapping(t *testing.T) {
	provider, err := New(Config{BaseURL: "https://compat.example", HTTPClient: roundTripFunc(nil)}, Spec{Name: "strict"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = provider.Chat(context.Background(), &core.Request{
		Model:    "m",
		Messages: []core.Message{core.Assistant(core.ReasoningBlock{Text: "think"})},
	})
	if err == nil || !strings.Contains(err.Error(), "ReasoningBlock history is not supported") {
		t.Fatalf("expected reasoning history error, got %v", err)
	}
}

func TestReasoningBlockExtraRoundTripsThroughConfiguredField(t *testing.T) {
	var capturedBody map[string]any
	provider, err := New(Config{
		BaseURL: "https://compat.example",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if err := json.NewDecoder(req.Body).Decode(&capturedBody); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			return jsonResponse(http.StatusOK, `{"choices":[{"message":{"content":"ok"}}]}`), nil
		}),
	}, Spec{
		Name: "minimax",
		Response: ResponseSpec{
			ReasoningFields: []string{"reasoning_details"},
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = provider.Chat(context.Background(), &core.Request{
		Model: "m",
		Messages: []core.Message{
			core.Assistant(core.ReasoningBlock{
				Text:  "step 1",
				Extra: json.RawMessage(`[{"type":"reasoning.text","text":"step 1"}]`),
			}),
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	messages := capturedBody["messages"].([]any)
	details := messages[0].(map[string]any)["reasoning_details"].([]any)
	if len(details) != 1 || details[0].(map[string]any)["text"] != "step 1" {
		t.Fatalf("reasoning_details = %#v", details)
	}
}

func TestResponseReasoningBlocksRoundTripThroughConfiguredField(t *testing.T) {
	provider, err := New(Config{
		BaseURL: "https://compat.example",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{
				"choices":[{
					"message":{
						"content":"ok",
						"reasoning_details":[{"type":"reasoning.text","text":"step 1"}]
					}
				}]
			}`), nil
		}),
	}, Spec{
		Name: "minimax",
		Response: ResponseSpec{
			ReasoningFields: []string{"reasoning_details"},
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	resp, err := provider.Chat(context.Background(), &core.Request{
		Model:    "m",
		Messages: []core.Message{core.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}

	var capturedBody map[string]any
	provider.cfg.HTTPClient = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(req.Body).Decode(&capturedBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		return jsonResponse(http.StatusOK, `{"choices":[{"message":{"content":"ok"}}]}`), nil
	})
	_, err = provider.Chat(context.Background(), &core.Request{
		Model:    "m",
		Messages: []core.Message{core.Assistant(resp.Blocks...)},
	})
	if err != nil {
		t.Fatalf("round-trip Chat: %v", err)
	}
	messages := capturedBody["messages"].([]any)
	details := messages[0].(map[string]any)["reasoning_details"].([]any)
	if len(details) != 1 || details[0].(map[string]any)["text"] != "step 1" {
		t.Fatalf("reasoning_details = %#v", details)
	}
}

func TestAssistantToolCallsCanEmitEmptyContentWhenConfigured(t *testing.T) {
	tests := []struct {
		name        string
		spec        Spec
		wantContent bool
	}{
		{name: "default_omits_empty_content", spec: Spec{Name: "compat"}},
		{name: "configured_emits_empty_content", spec: Spec{
			Name: "deepseek",
			Request: RequestSpec{
				EmitEmptyAssistantContentWithToolCalls: true,
			},
		}, wantContent: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedBody map[string]any
			provider, err := New(Config{
				BaseURL: "https://compat.example",
				HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					if err := json.NewDecoder(req.Body).Decode(&capturedBody); err != nil {
						t.Fatalf("decode body: %v", err)
					}
					return jsonResponse(http.StatusOK, `{"choices":[{"message":{"content":"ok"}}]}`), nil
				}),
			}, tt.spec)
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			_, err = provider.Chat(context.Background(), &core.Request{
				Model: "m",
				Messages: []core.Message{
					core.Assistant(core.ToolUseBlock{
						ID:        "call_1",
						Name:      "lookup",
						Arguments: json.RawMessage(`{"q":"weather"}`),
					}),
				},
			})
			if err != nil {
				t.Fatalf("Chat returned error: %v", err)
			}
			messages := capturedBody["messages"].([]any)
			assistant := messages[0].(map[string]any)
			_, hasContent := assistant["content"]
			if hasContent != tt.wantContent {
				t.Fatalf("has content = %v, want %v; assistant=%#v", hasContent, tt.wantContent, assistant)
			}
			if tt.wantContent && assistant["content"] != "" {
				t.Fatalf("content = %#v, want empty string", assistant["content"])
			}
		})
	}
}

func TestChatConvertsPromptTokensDetailsCachedTokens(t *testing.T) {
	provider, err := New(Config{
		BaseURL: "https://compat.example",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{
				"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],
				"usage":{
					"prompt_tokens":10,
					"completion_tokens":2,
					"total_tokens":12,
					"prompt_tokens_details":{"cached_tokens":7}
				}
			}`), nil
		}),
	}, Spec{Name: "strict"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	resp, err := provider.Chat(context.Background(), &core.Request{
		Model:    "m",
		Messages: []core.Message{core.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if resp.Usage.CacheReadTokens != 7 {
		t.Fatalf("cache read tokens = %d, want 7", resp.Usage.CacheReadTokens)
	}
}

func TestChatConvertsRefusalMessageAndContentPart(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "message_refusal",
			body: `{"choices":[{"message":{"content":null,"refusal":"I can't help."},"finish_reason":"content_filter"}]}`,
			want: "I can't help.",
		},
		{
			name: "content_part_refusal",
			body: `{"choices":[{"message":{"content":[{"type":"refusal","refusal":"I can't help."}]},"finish_reason":"content_filter"}]}`,
			want: "I can't help.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := New(Config{
				BaseURL: "https://compat.example",
				HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					return jsonResponse(http.StatusOK, tt.body), nil
				}),
			}, Spec{Name: "strict"})
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			resp, err := provider.Chat(context.Background(), &core.Request{
				Model:    "m",
				Messages: []core.Message{core.UserText("hi")},
			})
			if err != nil {
				t.Fatalf("Chat returned error: %v", err)
			}
			if resp.Text() != tt.want {
				t.Fatalf("text = %q, want %q", resp.Text(), tt.want)
			}
			if resp.Refusal != tt.want || resp.FinishReason != core.FinishReasonSafety {
				t.Fatalf("refusal/finish = %q/%q", resp.Refusal, resp.FinishReason)
			}
		})
	}
}

func TestInjectJSONSchemaAddsUserMessageWhenMissing(t *testing.T) {
	messages := injectJSONSchema([]core.Message{core.System("system only")}, &core.JSONSchema{
		Name:   "result",
		Schema: core.Schema(`{"type":"object"}`),
	})
	if len(messages) != 2 || messages[1].Role != core.RoleUser {
		t.Fatalf("messages = %#v", messages)
	}
	text, ok := messages[1].Blocks[0].(core.TextBlock)
	if !ok || !strings.Contains(text.Text, "Return JSON matching schema result") {
		t.Fatalf("injected block = %#v", messages[1].Blocks)
	}
}

func TestChatRejectsUnsupportedResponseContent(t *testing.T) {
	provider, err := New(Config{
		BaseURL: "https://compat.example",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{
				"choices":[{
					"message":{"content":[{"type":"audio","url":"https://example.test/a.wav"}]},
					"finish_reason":"stop"
				}]
			}`), nil
		}),
	}, Spec{Name: "strict"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = provider.Chat(context.Background(), &core.Request{
		Model:    "m",
		Messages: []core.Message{core.UserText("hi")},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported content part type") {
		t.Fatalf("expected unsupported content error, got %v", err)
	}
}

func TestChatPreservesInvalidToolCallArguments(t *testing.T) {
	provider, err := New(Config{
		BaseURL: "https://compat.example",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{
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
	}, Spec{Name: "strict"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	resp, err := provider.Chat(context.Background(), &core.Request{
		Model:    "m",
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
		BaseURL: "https://compat.example",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{"choices":[`), nil
		}),
	}, Spec{Name: "strict"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = provider.Chat(context.Background(), &core.Request{
		Model:    "m",
		Messages: []core.Message{core.UserText("hi")},
	})
	if err == nil || !strings.Contains(err.Error(), "decode response") || !core.IsProviderError(err) {
		t.Fatalf("expected structured decode provider error, got %v", err)
	}
}

func TestThinkingMapperMayEmitNoFields(t *testing.T) {
	called := false
	provider, err := New(Config{
		BaseURL: "https://compat.example",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			called = true
			return jsonResponse(http.StatusOK, `{"choices":[{"message":{"content":"ok"}}]}`), nil
		}),
	}, Spec{
		Name: "empty-thinking",
		Request: RequestSpec{
			Thinking: func(*core.Thinking, string) (map[string]any, error) {
				return nil, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = provider.Chat(context.Background(), &core.Request{
		Model:    "m",
		Messages: []core.Message{core.UserText("hi")},
		Thinking: &core.Thinking{Mode: core.ThinkingEnabled},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if !called {
		t.Fatal("request was not sent")
	}
}

func TestConvertToolsRequireAllStrict(t *testing.T) {
	strict := mustTool(t, "strict", "Strict.", map[string]any{"type": "object"})
	strict.Strict = core.StrictEnabled
	unspecified := mustTool(t, "unspecified", "Unspecified.", map[string]any{"type": "object"})

	tools, _, err := convertTools([]core.Tool{strict, unspecified}, StrictToolsRequireAll)
	if err != nil {
		t.Fatalf("convertTools: %v", err)
	}
	for _, tool := range tools {
		fn := tool["function"].(map[string]any)
		if fn["strict"] != true {
			t.Fatalf("function = %#v, want strict=true", fn)
		}
	}

	nonStrict := unspecified
	nonStrict.Strict = core.StrictDisabled
	if _, _, err := convertTools([]core.Tool{strict, nonStrict}, StrictToolsRequireAll); err == nil || !strings.Contains(err.Error(), "all tools") {
		t.Fatalf("expected mixed strict error, got %v", err)
	}
}

func TestConvertToolsAlwaysStrict(t *testing.T) {
	tool := mustTool(t, "lookup", "Lookup.", map[string]any{"type": "object"})
	tool.Strict = core.StrictEnabled
	tools, warnings, err := convertTools([]core.Tool{tool}, StrictToolsAlways)
	if err != nil {
		t.Fatalf("convertTools: %v", err)
	}
	fn := tools[0]["function"].(map[string]any)
	if _, ok := fn["strict"]; ok || len(warnings) != 0 {
		t.Fatalf("function/warnings = %#v/%#v", fn, warnings)
	}

	tool.Strict = core.StrictDisabled
	if _, _, err := convertTools([]core.Tool{tool}, StrictToolsAlways); err == nil || !strings.Contains(err.Error(), "cannot disable strict") {
		t.Fatalf("expected strict disable error, got %v", err)
	}
}

// TestReasoningBlockEmptyTextErrorsWhenReasoningConfigured is the guard:
// if a ReasoningBlock reaches the provider builder with empty text AND no
// Extra AND no Signature, the reasoning was received from the provider
// (input) but is now empty on replay (output). This is a consistency
// violation — force an error instead of silently sending an empty
// reasoning field that models requiring reasoning_content would reject.
func TestReasoningBlockEmptyTextErrorsWhenReasoningConfigured(t *testing.T) {
	provider, err := New(Config{
		BaseURL: "https://compat.example",
		HTTPClient: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("request must not be sent when reasoning block is empty")
			return nil, nil
		}),
	}, Spec{
		Name: "deepseek",
		Response: ResponseSpec{
			ReasoningFields: []string{"reasoning_content"},
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = provider.Chat(context.Background(), &core.Request{
		Model: "m",
		Messages: []core.Message{
			core.UserText("hi"),
			core.Assistant(core.ReasoningBlock{Text: ""}),
		},
	})
	if err == nil || !strings.Contains(err.Error(), "empty reasoning") {
		t.Fatalf("expected empty reasoning error, got %v", err)
	}
}

// TestReasoningBlockPlaceholderTextPasses ensures the placeholder injected
// by the application layer (domain.ReasoningPlaceholder) is accepted and
// forwarded — the guard only rejects truly empty reasoning, not the
// placeholder sentinel.
func TestReasoningBlockPlaceholderTextPasses(t *testing.T) {
	var capturedBody map[string]any
	provider, err := New(Config{
		BaseURL: "https://compat.example",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if err := json.NewDecoder(req.Body).Decode(&capturedBody); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			return jsonResponse(http.StatusOK, `{"choices":[{"message":{"content":"ok"}}]}`), nil
		}),
	}, Spec{
		Name: "deepseek",
		Response: ResponseSpec{
			ReasoningFields: []string{"reasoning_content"},
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = provider.Chat(context.Background(), &core.Request{
		Model: "m",
		Messages: []core.Message{
			core.UserText("hi"),
			core.Assistant(core.ReasoningBlock{Text: "(Continue from the current context.)"}),
		},
	})
	if err != nil {
		t.Fatalf("placeholder reasoning must pass, got error: %v", err)
	}
	messages := capturedBody["messages"].([]any)
	if messages[1].(map[string]any)["reasoning_content"] != "(Continue from the current context.)" {
		t.Fatalf("reasoning_content = %#v", messages[1])
	}
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
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
	u := convertUsage(usage{
		PromptTokens:     3,
		CompletionTokens: 2,
		TotalTokens:      5,
		PromptTokensDetails: &promptTokensDetails{
			CachedTokens: 8, // exceeds prompt
		},
	}, Spec{}, "test", "model")
	if u.InputTokens != 0 {
		t.Fatalf("InputTokens = %d, want 0 (clamped)", u.InputTokens)
	}
	if u.CacheReadTokens != 8 {
		t.Fatalf("CacheReadTokens = %d, want 8", u.CacheReadTokens)
	}
}

func TestToolResultImageReinjectsAsUserMessage(t *testing.T) {
	var capturedBody map[string]any
	provider, err := New(Config{
		APIKey:  "test-key",
		BaseURL: "https://compat.example/v1",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if err := json.NewDecoder(req.Body).Decode(&capturedBody); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			return jsonResponse(http.StatusOK, `{"choices":[{"message":{"content":"ok"}}]}`), nil
		}),
	}, Spec{
		Name: "testcompat",
		Auth: AuthSpec{APIKeyRequired: true},
		Request: RequestSpec{
			SupportsJSONSchema: true,
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := provider.Chat(context.Background(), &core.Request{
		Model: "m",
		Messages: []core.Message{
			core.UserText("read this image"),
			core.Assistant(core.ToolUseBlock{ID: "call_1", Name: "read_media", Arguments: core.MustJSONRaw(map[string]any{"file_path": "/tmp/x.png"})}),
			core.ToolResult("call_1", core.Text("/tmp/x.png"), core.ImageURL("https://example.test/x.png")),
		},
	}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	msgs := capturedBody["messages"].([]any)
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
	var capturedBody map[string]any
	provider, err := New(Config{
		APIKey:  "test-key",
		BaseURL: "https://compat.example/v1",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if err := json.NewDecoder(req.Body).Decode(&capturedBody); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			return jsonResponse(http.StatusOK, `{"choices":[{"message":{"content":"ok"}}]}`), nil
		}),
	}, Spec{
		Name: "testcompat",
		Auth: AuthSpec{APIKeyRequired: true},
		Request: RequestSpec{
			SupportsJSONSchema: true,
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := provider.Chat(context.Background(), &core.Request{
		Model: "deepseek-v4-flash-vision-exp",
		Messages: []core.Message{
			core.UserText("inspect the screenshot and summarize it"),
			core.Assistant(
				core.ToolUseBlock{ID: "call_image", Name: "read_media", Arguments: core.MustJSONRaw(map[string]any{})},
				core.ToolUseBlock{ID: "call_summary", Name: "memory", Arguments: core.MustJSONRaw(map[string]any{})},
			),
			core.ToolResult("call_image", core.Text("/tmp/screenshot.png"), core.ImageURL("https://example.test/screenshot.png")),
			core.ToolResultText("call_summary", "summary"),
		},
	}); err != nil {
		t.Fatalf("Chat: %v", err)
	}

	msgs := capturedBody["messages"].([]any)
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
	var capturedBody map[string]any
	provider, err := New(Config{
		APIKey:  "test-key",
		BaseURL: "https://compat.example/v1",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if err := json.NewDecoder(req.Body).Decode(&capturedBody); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			return jsonResponse(http.StatusOK, `{"choices":[{"message":{"content":"ok"}}]}`), nil
		}),
	}, Spec{
		Name: "testcompat",
		Auth: AuthSpec{APIKeyRequired: true},
		Request: RequestSpec{
			SupportsJSONSchema: true,
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := provider.Chat(context.Background(), &core.Request{
		Model: "m",
		Messages: []core.Message{
			core.UserText("hi"),
			core.Assistant(core.ToolUseBlock{ID: "call_1", Name: "lookup", Arguments: core.MustJSONRaw(map[string]any{"q": "x"})}),
			core.ToolResultText("call_1", "ok"),
		},
	}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	msgs := capturedBody["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3 (no reinject for text-only result): %+v", len(msgs), msgs)
	}
}
