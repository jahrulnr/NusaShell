package acp

import (
	"encoding/json"
	"testing"
)

func TestSessionLifecycleCapabilities(t *testing.T) {
	for _, tc := range []struct {
		raw       string
		supported bool
	}{{"{}", true}, {"null", false}, {"", false}} {
		var result InitializeResult
		result.AgentCapabilities.SessionCapabilities.Close = json.RawMessage(tc.raw)
		result.AgentCapabilities.SessionCapabilities.Delete = json.RawMessage(tc.raw)
		result.AgentCapabilities.SessionCapabilities.Resume = json.RawMessage(tc.raw)
		result.AgentCapabilities.Auth.Logout = json.RawMessage(tc.raw)
		if result.SupportsClose() != tc.supported || result.SupportsDelete() != tc.supported || result.SupportsResume() != tc.supported || result.SupportsLogout() != tc.supported {
			t.Fatalf("capability %q: close=%v delete=%v", tc.raw, result.SupportsClose(), result.SupportsDelete())
		}
	}
}

// A project's other folders reach the agent only when it takes them, and
// agents say so with an empty object: Devin advertises
// "additionalDirectories": {} and nothing more.
func TestAdditionalDirectoriesCapability(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want bool
	}{{"{}", true}, {`{"x":1}`, true}, {"true", true}, {"null", false}, {"false", false}, {"", false}} {
		var result InitializeResult
		result.AgentCapabilities.SessionCapabilities.AdditionalDirectories = json.RawMessage(tc.raw)
		if got := result.SupportsAdditionalDirectories(); got != tc.want {
			t.Errorf("%q: got %v, want %v", tc.raw, got, tc.want)
		}
	}

	// The capabilities Devin sends, as sent.
	var devin InitializeResult
	if err := json.Unmarshal([]byte(`{"agentCapabilities":{"loadSession":true,"sessionCapabilities":{"list":{},"delete":{},"additionalDirectories":{},"close":{"_meta":{"cognition.ai/force":true}}}}}`), &devin); err != nil {
		t.Fatal(err)
	}
	if !devin.SupportsAdditionalDirectories() || !devin.SupportsClose() || !devin.SupportsDelete() {
		t.Fatalf("Devin's capabilities read as additionalDirectories=%v close=%v delete=%v",
			devin.SupportsAdditionalDirectories(), devin.SupportsClose(), devin.SupportsDelete())
	}
}

func TestSystemPromptCapability(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{"", false}, {"null", false}, {"false", false}, {"true", false}, {"[]", false}, {"{}", true}, {`{"mode":{"override":{}}}`, true},
	} {
		var result InitializeResult
		result.AgentCapabilities.SessionCapabilities.SystemPrompt = json.RawMessage(tc.raw)
		if got := result.SupportsSystemPrompt(); got != tc.want {
			t.Errorf("%q: got %v, want %v", tc.raw, got, tc.want)
		}
	}
}
