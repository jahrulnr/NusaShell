package openai

import (
	"testing"

	"nusashell/infrastructure/ai/core"
)

func TestCapabilitiesCurrentBaseline(t *testing.T) {
	provider := mustProvider(t)
	caps := provider.Capabilities("gpt-5.6")
	if caps.Provider != "openai" || caps.Model != "gpt-5.6" {
		t.Fatalf("caps = %+v", caps)
	}
	if caps.Thinking.Supported != core.SupportPartial || caps.Thinking.Disable != core.SupportPartial {
		t.Fatalf("thinking caps = %+v", caps.Thinking)
	}
	if !caps.Thinking.SupportsEffort("high") || !caps.Thinking.SupportsEffort("xhigh") || !caps.Thinking.SupportsEffort("max") || caps.Thinking.SupportsEffort("minimal") {
		t.Fatalf("thinking caps = %+v", caps.Thinking)
	}
	if caps.Streaming.NativeResponses != core.SupportYes {
		t.Fatalf("native responses = %v, want yes", caps.Streaming.NativeResponses)
	}
}

func TestCapabilitiesUseStableProviderBaseline(t *testing.T) {
	provider := mustProvider(t)
	caps := provider.Capabilities("gpt-5.6")
	if !caps.Thinking.SupportsEffort("max") || caps.Cache.UsageWrite != core.SupportPartial {
		t.Fatalf("gpt-5.6 caps = %+v", caps)
	}

	future := provider.Capabilities("gpt-6")
	if future.Thinking.Disable != core.SupportPartial || !future.Thinking.SupportsEffort("max") || future.Tools.Calls != core.SupportYes {
		t.Fatalf("future GPT-5 baseline = %+v", future)
	}

	nonReasoning := provider.Capabilities("gpt-4.1")
	if nonReasoning.Thinking.Supported != core.SupportPartial || !nonReasoning.Thinking.SupportsEffort("max") {
		t.Fatalf("provider baseline changed by model = %+v", nonReasoning.Thinking)
	}
}

// TestCapabilitiesStructuredOutputsByEndpoint verifies that Structured
// Outputs are an official endpoint contract, while compatible custom
// endpoints remain Unknown unless the caller explicitly declares support.
func TestCapabilitiesStructuredOutputsByEndpoint(t *testing.T) {
	official := Config{APIKey: "test"}
	custom := Config{APIKey: "test", BaseURL: "https://relay.example.com/v1"}
	cases := []struct {
		name  string
		cfg   Config
		model string
		want  core.Support
	}{
		{"official endpoint", official, "gpt-5.6", core.SupportYes},
		{"custom endpoint", custom, "gpt-5.6", core.SupportUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := New(tc.cfg)
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			caps := provider.Capabilities(tc.model)
			if caps.Structured.JSONSchema != tc.want || caps.Structured.Strict != tc.want {
				t.Fatalf("structured caps = %+v, want json_schema/strict %v", caps.Structured, tc.want)
			}
		})
	}
	provider, err := New(custom)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if got := provider.Capabilities("gpt-4o").Structured.JSONObject; got != core.SupportUnknown {
		t.Fatalf("custom base URL json_object = %v, want unknown", got)
	}
	if got := provider.Capabilities("gpt-5.7").Streaming.Supported; got != core.SupportYes {
		t.Fatalf("custom base URL streaming = %v, want yes", got)
	}
}

// TestCapabilitiesPromptCacheParamsGating verifies that prompt cache params
// are only advertised for the official endpoint by default: compatible
// backends have no unknown-field contract (strict ones return 400/422), so
// custom BaseURLs degrade to SupportUnknown unless explicitly opted in.
func TestCapabilitiesPromptCacheParamsGating(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want core.Support
	}{
		{"default official endpoint", Config{APIKey: "test"}, core.SupportYes},
		{"explicit official endpoint", Config{APIKey: "test", BaseURL: "https://api.openai.com/v1"}, core.SupportYes},
		{"third-party compatible endpoint", Config{APIKey: "test", BaseURL: "https://relay.example.com/v1"}, core.SupportUnknown},
		{"third-party with opt-in", Config{APIKey: "test", BaseURL: "https://relay.example.com/v1", PromptCacheParams: true}, core.SupportYes},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := New(tc.cfg)
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			caps := provider.Capabilities("gpt-4.1")
			if caps.Cache.Block != tc.want || caps.Cache.PromptKey != tc.want || caps.Cache.Retention != tc.want {
				t.Fatalf("prompt cache caps = (block=%v key=%v retention=%v), want %v", caps.Cache.Block, caps.Cache.PromptKey, caps.Cache.Retention, tc.want)
			}
		})
	}
}
