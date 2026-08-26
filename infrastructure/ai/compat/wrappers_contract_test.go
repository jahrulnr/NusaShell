package compat_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"nusashell/infrastructure/ai/compat"
	"nusashell/infrastructure/ai/core"
	"nusashell/infrastructure/ai/internal/testgolden"
	"nusashell/infrastructure/ai/openrouter"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

type wrapperCase struct {
	name               string
	apiKey             bool
	capabilityModel    string
	strictToolsForward bool
	new                func(compat.Config) (*compat.Provider, error)
}

func compatWrappers() []wrapperCase {
	return []wrapperCase{
		{name: "openrouter", apiKey: true, capabilityModel: "anthropic/claude-sonnet-4", strictToolsForward: true, new: openrouter.New},
	}
}

func TestCompatWrappersConvertResponseFixture(t *testing.T) {
	for _, tt := range compatWrappers() {
		t.Run(tt.name, func(t *testing.T) {
			provider := newWrapper(t, tt, roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusOK, testgolden.ReadFixtureString(t, "../../testdata/compat/chat_response.json")), nil
			}))
			resp, err := provider.Chat(context.Background(), &core.Request{
				Model:    "m",
				Messages: []core.Message{core.UserText("hi")},
			})
			if err != nil {
				t.Fatalf("Chat returned error: %v", err)
			}
			if resp.Provider != tt.name || resp.Model != "provider-model" {
				t.Fatalf("provider/model = %q/%q", resp.Provider, resp.Model)
			}
			if resp.Text() != "hello" {
				t.Fatalf("text = %q", resp.Text())
			}
			calls := resp.ToolCalls()
			if len(calls) != 1 || calls[0].ID != "call_1" || calls[0].Name != "lookup" || string(calls[0].Arguments) != `{"q":"x"}` {
				t.Fatalf("tool calls = %+v", calls)
			}
			if resp.FinishReason != core.FinishReasonToolCall {
				t.Fatalf("finish reason = %q", resp.FinishReason)
			}
		})
	}
}

func TestCompatWrappersConvertStreamFixture(t *testing.T) {
	for _, tt := range compatWrappers() {
		t.Run(tt.name, func(t *testing.T) {
			provider := newWrapper(t, tt, roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return streamResponse(testgolden.ReadFixtureString(t, "../../testdata/compat/stream.sse")), nil
			}))
			stream, err := provider.Stream(context.Background(), &core.Request{
				Model:    "m",
				Messages: []core.Message{core.UserText("hi")},
			})
			if err != nil {
				t.Fatalf("Stream returned error: %v", err)
			}
			resp, err := core.Collect(stream)
			if err != nil {
				t.Fatalf("Collect returned error: %v", err)
			}
			if resp.Provider != tt.name || resp.Text() != "hi" {
				t.Fatalf("provider/text = %q/%q", resp.Provider, resp.Text())
			}
			calls := resp.ToolCalls()
			if len(calls) != 1 || calls[0].ID != "call_1" || calls[0].Name != "lookup" || string(calls[0].Arguments) != `{"q":"x"}` {
				t.Fatalf("tool calls = %+v", calls)
			}
			if resp.FinishReason != core.FinishReasonToolCall {
				t.Fatalf("finish reason = %q", resp.FinishReason)
			}
		})
	}
}

func TestCompatWrappersRejectUnknownProviderOptions(t *testing.T) {
	for _, tt := range compatWrappers() {
		t.Run(tt.name, func(t *testing.T) {
			provider := newWrapper(t, tt, roundTripFunc(func(req *http.Request) (*http.Response, error) {
				t.Fatalf("request should not be sent when provider options are invalid")
				return nil, nil
			}))
			_, err := provider.Chat(context.Background(), &core.Request{
				Model:           "m",
				Messages:        []core.Message{core.UserText("hi")},
				ProviderOptions: core.ProviderOptions{"unknown": true},
			})
			if err == nil || !core.IsValidationError(err) || !strings.Contains(err.Error(), "unsupported provider option") {
				t.Fatalf("expected provider option validation error, got %v", err)
			}
		})
	}
}

func TestCompatWrappersWarnWhenStrictToolIsOmitted(t *testing.T) {
	for _, tt := range compatWrappers() {
		if tt.strictToolsForward {
			continue
		}
		t.Run(tt.name, func(t *testing.T) {
			provider := newWrapper(t, tt, roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusOK, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`), nil
			}))
			tool, err := core.NewTool("lookup", "Lookup.", map[string]any{"type": "object"})
			if err != nil {
				t.Fatalf("NewTool returned error: %v", err)
			}
			tool.Strict = core.StrictEnabled
			resp, err := provider.Chat(context.Background(), &core.Request{
				Model:    "m",
				Messages: []core.Message{core.UserText("hi")},
				Tools:    []core.Tool{tool},
			})
			if err != nil {
				t.Fatalf("Chat returned error: %v", err)
			}
			if len(resp.Warnings) != 1 {
				t.Fatalf("warnings len = %d, want 1: %#v", len(resp.Warnings), resp.Warnings)
			}
			warning := resp.Warnings[0]
			if warning.Code != "request.strict_tool_omitted" || warning.Provider != tt.name {
				t.Fatalf("warning = %#v", warning)
			}
		})
	}
}

func TestCompatWrapperCapabilitiesThinkingEffortsAreAccepted(t *testing.T) {
	for _, tt := range compatWrappers() {
		t.Run(tt.name, func(t *testing.T) {
			provider := newWrapper(t, tt, roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusOK, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`), nil
			}))
			caps := provider.Capabilities(tt.capabilityModel)
			for _, effort := range caps.Thinking.Efforts {
				t.Run(effort, func(t *testing.T) {
					_, err := provider.Chat(context.Background(), &core.Request{
						Model:    tt.capabilityModel,
						Messages: []core.Message{core.UserText("hi")},
						Thinking: &core.Thinking{Mode: core.ThinkingEnabled, Effort: effort},
					})
					if err != nil {
						t.Fatalf("capability effort %q was rejected: %v", effort, err)
					}
				})
			}
		})
	}
}

func newWrapper(t testing.TB, tt wrapperCase, client compat.HTTPClient) *compat.Provider {
	t.Helper()
	cfg := compat.Config{
		BaseURL:    "https://" + tt.name + ".test",
		HTTPClient: client,
	}
	if tt.apiKey {
		cfg.APIKey = "key"
	}
	provider, err := tt.new(cfg)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	return provider
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func streamResponse(body string) *http.Response {
	resp := jsonResponse(http.StatusOK, body)
	resp.Header.Set("Content-Type", "text/event-stream")
	return resp
}
