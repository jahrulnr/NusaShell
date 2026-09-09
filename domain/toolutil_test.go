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

		// URL-only detection remains false for custom gateways. Explicit
		// OpenRouter drivers use the compatibility/profile path through
		// UsesOpenRouterWire, which is tested separately below.
		{name: "tokenrouter", kind: ProviderChat, baseURL: "https://api.tokenrouter.com/v1", want: false},
		{name: "9router localhost", kind: ProviderChat, baseURL: "http://localhost:20128/v1", want: false},
		{name: "opencode zen", kind: ProviderChat, baseURL: "https://opencode.ai/zen/v1", want: false},
		{name: "opencode zen go", kind: ProviderChat, baseURL: "https://opencode.ai/zen/go/v1", want: false},
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

func TestUsesOpenRouterWire(t *testing.T) {
	tests := []struct {
		name    string
		kind    ProviderKind
		driver  ProviderDriver
		baseURL string
		want    bool
	}{
		{
			name:    "genuine openrouter host",
			kind:    ProviderChat,
			driver:  ProviderDriverAuto,
			baseURL: "https://openrouter.ai/api/v1",
			want:    true,
		},
		{
			name:    "openrouter driver on openrouter host",
			kind:    ProviderChat,
			driver:  ProviderDriverOpenRouter,
			baseURL: "https://openrouter.ai/api/v1",
			want:    true,
		},
		// Custom providers default to the OpenRouter compatibility/profile path,
		// even when their gateway is not hosted at openrouter.ai. The custom
		// gateway remains the target; OpenRouter supplies the request profile.
		{
			name:    "opencode zen go with custom openrouter driver",
			kind:    ProviderChat,
			driver:  ProviderDriverOpenRouter,
			baseURL: "https://opencode.ai/zen/go/v1",
			want:    true,
		},
		{
			name:    "opencode zen with custom openrouter driver",
			kind:    ProviderChat,
			driver:  ProviderDriverOpenRouter,
			baseURL: "https://opencode.ai/zen/v1",
			want:    true,
		},
		{
			name:    "tokenrouter with custom openrouter driver",
			kind:    ProviderChat,
			driver:  ProviderDriverOpenRouter,
			baseURL: "https://api.tokenrouter.com/v1",
			want:    true,
		},
		{
			name:    "custom chat host with openrouter profile",
			kind:    ProviderChat,
			driver:  ProviderDriverOpenRouter,
			baseURL: "https://example.test/v1",
			want:    true,
		},
		{
			name:    "openrouter driver still selects openrouter for responses",
			kind:    ProviderResponses,
			driver:  ProviderDriverOpenRouter,
			baseURL: "https://example.test/v1",
			want:    true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := UsesOpenRouterWire(tc.kind, tc.driver, tc.baseURL)
			if got != tc.want {
				t.Fatalf("UsesOpenRouterWire(%q, %q, %q) = %v, want %v",
					tc.kind, tc.driver, tc.baseURL, got, tc.want)
			}
		})
	}
}

func TestWireCacheDriver(t *testing.T) {
	tests := []struct {
		name    string
		kind    ProviderKind
		driver  ProviderDriver
		baseURL string
		want    ProviderDriver
	}{
		{
			name:    "opencode zen go keeps 5m/1h cache enum",
			kind:    ProviderChat,
			driver:  ProviderDriverOpenRouter,
			baseURL: "https://opencode.ai/zen/go/v1",
			want:    ProviderDriverOpenRouter,
		},
		{
			name:    "opencode zen keeps 5m/1h cache enum",
			kind:    ProviderChat,
			driver:  ProviderDriverOpenRouter,
			baseURL: "https://opencode.ai/zen/v1",
			want:    ProviderDriverOpenRouter,
		},
		{
			name:    "openrouter.ai keeps openrouter cache enum",
			kind:    ProviderChat,
			driver:  ProviderDriverOpenRouter,
			baseURL: "https://openrouter.ai/api/v1",
			want:    ProviderDriverOpenRouter,
		},
		{
			name:    "custom tokenrouter uses openrouter-profile cache enum",
			kind:    ProviderChat,
			driver:  ProviderDriverOpenRouter,
			baseURL: "https://api.tokenrouter.com/v1",
			want:    ProviderDriverOpenRouter,
		},
		{
			name:    "messages openrouter driver unchanged",
			kind:    ProviderMessages,
			driver:  ProviderDriverOpenRouter,
			baseURL: "https://example.test/v1",
			want:    ProviderDriverOpenRouter,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := WireCacheDriver(tc.kind, tc.driver, tc.baseURL)
			if got != tc.want {
				t.Fatalf("WireCacheDriver(%q, %q, %q) = %q, want %q",
					tc.kind, tc.driver, tc.baseURL, got, tc.want)
			}
		})
	}
}

func TestIsOpenCodeHost(t *testing.T) {
	tests := []struct {
		baseURL string
		want    bool
	}{
		{"https://opencode.ai/zen/go/v1", true},
		{"https://opencode.ai/zen/v1", true},
		{"https://api.opencode.ai/v1", true},
		{"https://openrouter.ai/api/v1", false},
		{"https://api.tokenrouter.com/v1", false},
		{"", false},
	}
	for _, tc := range tests {
		if got := IsOpenCodeHost(tc.baseURL); got != tc.want {
			t.Fatalf("IsOpenCodeHost(%q) = %v, want %v", tc.baseURL, got, tc.want)
		}
	}
}
