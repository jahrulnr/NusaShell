package compat

import (
	"testing"

	"nusashell/infrastructure/ai/core"
)

func TestCapabilitiesDefaultFromSpec(t *testing.T) {
	provider, err := New(Config{BaseURL: "https://compat.test"}, Spec{
		Name: "compat-test",
		Request: RequestSpec{
			Thinking:           func(*core.Thinking, string) (map[string]any, error) { return map[string]any{"thinking": true}, nil },
			SupportsJSONSchema: true,
		},
		Response: ResponseSpec{
			ReasoningFields:           []string{"reasoning_content"},
			HasCompletionTokenDetails: true,
			HasCacheTokens:            true,
		},
		Features: FeatureSpec{StrictTools: StrictToolsForward},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	caps := provider.Capabilities("model")
	if caps.Provider != "compat-test" || caps.Model != "model" {
		t.Fatalf("caps = %+v", caps)
	}
	if caps.Thinking.Supported != core.SupportYes || caps.Structured.JSONSchema != core.SupportYes {
		t.Fatalf("caps = %+v", caps)
	}
	if caps.Structured.Strict != core.SupportPartial {
		t.Fatalf("structured strict = %v, want partial", caps.Structured.Strict)
	}
	if caps.Tools.StrictSchema != core.SupportYes || caps.Reasoning.StreamingDeltas != core.SupportYes {
		t.Fatalf("caps = %+v", caps)
	}
	if caps.Usage.CacheReadTokens != core.SupportYes || caps.Usage.CacheWriteTokens != core.SupportPartial {
		t.Fatalf("usage caps = %+v", caps.Usage)
	}
}

func TestCapabilitiesMapperCanOverrideDefaults(t *testing.T) {
	provider, err := New(Config{BaseURL: "https://compat.test"}, Spec{
		Name: "compat-test",
		Capabilities: func(model string, caps core.Capabilities) core.Capabilities {
			caps.Model = model + "-override"
			caps.Thinking.Supported = core.SupportNo
			return caps
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	caps := provider.Capabilities("model")
	if caps.Model != "model-override" || caps.Thinking.Supported != core.SupportNo {
		t.Fatalf("caps = %+v", caps)
	}
}

func TestStrictToolCapabilities(t *testing.T) {
	for _, test := range []struct {
		mode StrictToolMode
		want core.Support
	}{
		{mode: StrictToolsRequireAll, want: core.SupportPartial},
		{mode: StrictToolsAlways, want: core.SupportYes},
	} {
		provider, err := New(Config{BaseURL: "https://compat.test"}, Spec{Name: "compat-test", Features: FeatureSpec{StrictTools: test.mode}})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if got := provider.Capabilities("model").Tools.StrictSchema; got != test.want {
			t.Fatalf("mode %d strict support = %v, want %v", test.mode, got, test.want)
		}
	}
}
