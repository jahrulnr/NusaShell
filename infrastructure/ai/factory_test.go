package ai

import (
	"strings"
	"testing"

	"nusashell/domain"
)

type stubCreds struct {
	val string
	ok  bool
}

func (s *stubCreds) Get(string) (string, bool, error)      { return s.val, s.ok, nil }
func (s *stubCreds) Set(_, v string) error                 { s.val = v; s.ok = true; return nil }
func (s *stubCreds) Delete(string) error                   { s.ok = false; return nil }
func (s *stubCreds) ListByPrefix(string) ([]string, error) { return nil, nil }

func TestNewProviderHTTPClientHasNoBodyTimeout(t *testing.T) {
	client := newProviderHTTPClient()
	if client.Timeout != 0 {
		t.Fatalf("client timeout = %s, want 0 so SSE bodies can outlive 60s", client.Timeout)
	}
}

func TestNewFactoryBuildsAdapterForSupportedKinds(t *testing.T) {
	f := NewFactory(&stubCreds{})
	for _, kind := range []domain.ProviderKind{domain.ProviderMessages, domain.ProviderResponses, domain.ProviderChat} {
		adapter, err := f(nil, &domain.Provider{Kind: kind, BaseURL: "https://example.test/v1"}, "key")
		if err != nil {
			t.Fatalf("factory for %s returned error: %v", kind, err)
		}
		got, ok := adapter.(*Adapter)
		if !ok {
			t.Fatalf("adapter for %s = %T, want *Adapter", kind, adapter)
		}
		if got.ProviderKind != kind {
			t.Fatalf("adapter kind = %s, want %s", got.ProviderKind, kind)
		}
	}
}

func TestNewFactoryRejectsUnknownKind(t *testing.T) {
	f := NewFactory(&stubCreds{})
	_, err := f(nil, &domain.Provider{Kind: "unknown"}, "")
	if err == nil {
		t.Fatal("expected error for unknown provider kind")
	}
}

func TestNewFactoryUsesExplicitProviderDrivers(t *testing.T) {
	f := NewFactory(&stubCreds{})
	tests := []struct {
		name   string
		driver domain.ProviderDriver
		kind   domain.ProviderKind
		key    string
		want   string
	}{
		{name: "anthropic messages", driver: domain.ProviderDriverAnthropic, kind: domain.ProviderMessages, key: "key", want: "anthropic"},
		{name: "openai responses", driver: domain.ProviderDriverOpenAI, kind: domain.ProviderResponses, key: "key", want: "openai"},
		{name: "openrouter chat", driver: domain.ProviderDriverOpenRouter, kind: domain.ProviderChat, want: "openrouter"},
		{name: "openrouter responses", driver: domain.ProviderDriverOpenRouter, kind: domain.ProviderResponses, key: "key", want: "openrouter"},
		{name: "openrouter messages", driver: domain.ProviderDriverOpenRouter, kind: domain.ProviderMessages, key: "key", want: "openrouter"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := f(nil, &domain.Provider{
				Driver:  tc.driver,
				Kind:    tc.kind,
				BaseURL: "https://example.test/v1",
			}, tc.key)
			if err != nil {
				t.Fatalf("factory returned error: %v", err)
			}
			adapter, ok := provider.(*Adapter)
			if !ok {
				t.Fatalf("provider = %T, want *Adapter", provider)
			}
			routed, err := adapter.providerFor()
			if err != nil {
				t.Fatalf("providerFor returned error: %v", err)
			}
			if got := routed.Name(); got != tc.want {
				t.Fatalf("routed provider name = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNewImageGeneratorFactoryRoutesOpenAIChat(t *testing.T) {
	f := NewImageGeneratorFactory(&stubCreds{})
	gen, err := f(&domain.Provider{Kind: domain.ProviderChat, BaseURL: "https://api.openai.com/v1"}, "tok")
	if err != nil {
		t.Fatal(err)
	}
	if gen == nil {
		t.Fatal("image generator must not be nil for chat kind")
	}
}

func TestNewImageGeneratorFactoryRejectsMessages(t *testing.T) {
	f := NewImageGeneratorFactory(&stubCreds{})
	_, err := f(&domain.Provider{Kind: domain.ProviderMessages, BaseURL: "https://api.anthropic.com"}, "key")
	if err == nil || !strings.Contains(err.Error(), "no image generation API") {
		t.Fatalf("err = %v", err)
	}
}
