package domain

import "testing"

func TestIsOpenRouterHost(t *testing.T) {
	tests := []struct {
		name    string
		kind    ProviderKind
		baseURL string
		want    bool
	}{
		// Genuine OpenRouter hosts qualify for the OpenRouter wire format.
		{name: "openrouter chat", kind: ProviderChat, baseURL: "https://openrouter.ai/api/v1", want: true},
		{name: "openrouter api subdomain", kind: ProviderChat, baseURL: "https://api.openrouter.ai/api/v1", want: true},
		{name: "openrouter bare host", kind: ProviderChat, baseURL: "http://openrouter.ai", want: true},

		// OpenAI-compatible aggregators do NOT speak the OpenRouter wire
		// format; they get the vanilla OpenAI Chat wire.
		{name: "tokenrouter", kind: ProviderChat, baseURL: "https://api.tokenrouter.com/v1", want: false},
		{name: "9router localhost", kind: ProviderChat, baseURL: "http://localhost:20128/v1", want: false},
		{name: "opencode zen", kind: ProviderChat, baseURL: "https://opencode.ai/zen/v1", want: false},
		{name: "one-api", kind: ProviderChat, baseURL: "https://gateway.example.com/v1", want: false},
		{name: "direct openai", kind: ProviderChat, baseURL: "https://api.openai.com/v1", want: false},
		// Only the chat kind can be an OpenRouter host.
		{name: "openrouter host but responses kind", kind: ProviderResponses, baseURL: "https://openrouter.ai/api/v1", want: false},
		{name: "empty base url", kind: ProviderChat, baseURL: "", want: false},
		{name: "malformed base url", kind: ProviderChat, baseURL: "://bad", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsOpenRouterHost(tc.kind, tc.baseURL); got != tc.want {
				t.Fatalf("IsOpenRouterHost(%q, %q) = %v, want %v", tc.kind, tc.baseURL, got, tc.want)
			}
		})
	}
}
