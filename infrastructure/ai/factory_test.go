package ai

import (
	"context"
	"strings"
	"testing"

	"nusashell/domain"
	"nusashell/infrastructure/ai/codex"
	"nusashell/infrastructure/ai/embeddings"
	"nusashell/infrastructure/ai/imagegen"
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
	for _, kind := range []domain.ProviderKind{domain.ProviderMessages, domain.ProviderResponses, domain.ProviderChat, domain.ProviderCodex} {
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
		name    string
		driver  domain.ProviderDriver
		kind    domain.ProviderKind
		key     string
		baseURL string
		want    string
	}{
		{name: "anthropic messages", driver: domain.ProviderDriverAnthropic, kind: domain.ProviderMessages, key: "key", want: "anthropic"},
		{name: "openai responses", driver: domain.ProviderDriverOpenAI, kind: domain.ProviderResponses, key: "key", want: "openai"},
		{name: "custom openrouter chat uses OpenRouter profile", driver: domain.ProviderDriverOpenRouter, kind: domain.ProviderChat, want: "openrouter"},
		{name: "openrouter chat on openrouter.ai", driver: domain.ProviderDriverOpenRouter, kind: domain.ProviderChat, baseURL: "https://openrouter.ai/api/v1", want: "openrouter"},
		{name: "custom opencode uses OpenRouter profile", driver: domain.ProviderDriverOpenRouter, kind: domain.ProviderChat, baseURL: "https://opencode.ai/zen/go/v1", want: "openrouter"},
		{name: "openrouter responses", driver: domain.ProviderDriverOpenRouter, kind: domain.ProviderResponses, key: "key", want: "openrouter"},
		{name: "openrouter messages", driver: domain.ProviderDriverOpenRouter, kind: domain.ProviderMessages, key: "key", want: "openrouter"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			baseURL := tc.baseURL
			if baseURL == "" {
				baseURL = "https://example.test/v1"
			}
			provider, err := f(nil, &domain.Provider{
				Driver:  tc.driver,
				Kind:    tc.kind,
				BaseURL: baseURL,
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
	gen, err := f(context.Background(), &domain.Provider{Kind: domain.ProviderChat, BaseURL: "https://api.openai.com/v1"}, "tok")
	if err != nil {
		t.Fatal(err)
	}
	if gen == nil {
		t.Fatal("image generator must not be nil for chat kind")
	}
}

func TestNewImageGeneratorFactoryRejectsMessages(t *testing.T) {
	f := NewImageGeneratorFactory(&stubCreds{})
	_, err := f(context.Background(), &domain.Provider{Kind: domain.ProviderMessages, BaseURL: "https://api.anthropic.com"}, "key")
	if err == nil || !strings.Contains(err.Error(), "no image generation API") {
		t.Fatalf("err = %v", err)
	}
}

func TestNewImageGeneratorFactoryRoutesCodex(t *testing.T) {
	// Far-future expiry: no refresh is attempted, so the factory performs no
	// network I/O and returns the client with the resolved access token.
	json, err := (&codex.TokenJSON{
		AccessToken: "tok-1",
		AccountID:   "acc-1",
		ExpiresAt:   4102444800, // 2100-01-01
	}).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	f := NewImageGeneratorFactory(&stubCreds{})
	gen, err := f(context.Background(), &domain.Provider{
		Kind:    domain.ProviderCodex,
		BaseURL: "https://chatgpt.com/backend-api/codex",
	}, json)
	if err != nil {
		t.Fatal(err)
	}
	client, ok := gen.(*imagegen.Client)
	if !ok {
		t.Fatalf("generator = %T, want *imagegen.Client", gen)
	}
	if client.Backend != imagegen.BackendCodex {
		t.Fatalf("backend = %q, want %q", client.Backend, imagegen.BackendCodex)
	}
	if client.APIKey != "tok-1" || client.AccountID != "acc-1" {
		t.Fatalf("token = %q account = %q", client.APIKey, client.AccountID)
	}
	if client.BaseURL != "https://chatgpt.com/backend-api/codex" {
		t.Fatalf("base url = %q", client.BaseURL)
	}
}

func TestNewImageGeneratorFactoryCodexDefaultsBaseURL(t *testing.T) {
	f := NewImageGeneratorFactory(&stubCreds{})
	gen, err := f(context.Background(), &domain.Provider{Kind: domain.ProviderCodex}, "plain-token")
	if err != nil {
		t.Fatal(err)
	}
	client, ok := gen.(*imagegen.Client)
	if !ok {
		t.Fatalf("generator = %T, want *imagegen.Client", gen)
	}
	if client.BaseURL != codex.DefaultBaseURL {
		t.Fatalf("base url = %q, want %q", client.BaseURL, codex.DefaultBaseURL)
	}
	if client.APIKey != "plain-token" {
		t.Fatalf("api key = %q, want the plain pasted token", client.APIKey)
	}
}

func TestNewEmbedderFactoryPassesModelContextAsTokenCap(t *testing.T) {
	f := NewEmbedderFactory()
	p := &domain.Provider{
		Kind:    domain.ProviderChat,
		BaseURL: "https://example.test/v1",
		Models: []domain.Model{
			{ID: "embed-small", Kind: domain.ModelKindEmbedding, Context: 512},
			{ID: "chat-model", Kind: domain.ModelKindChat, Context: 128000},
		},
	}
	embed, err := f(p, "tok")
	if err != nil {
		t.Fatal(err)
	}
	e, ok := embed.(*embeddings.Embedder)
	if !ok {
		t.Fatalf("embedder = %T, want *embeddings.Embedder", embed)
	}
	if e.MaxTokens != 512 {
		t.Fatalf("MaxTokens = %d, want the embedding model's Context 512", e.MaxTokens)
	}
	if e.Model != "embed-small" {
		t.Fatalf("model = %q, want first embedding model", e.Model)
	}
}

func TestNewEmbedderFactoryFallsBackToDefaultCapWithoutCatalogContext(t *testing.T) {
	f := NewEmbedderFactory()
	p := &domain.Provider{
		Kind:    domain.ProviderChat,
		BaseURL: "https://example.test/v1",
		Models:  []domain.Model{{ID: "embed-local", Kind: domain.ModelKindEmbedding}}, // Context 0 = unknown
	}
	embed, err := f(p, "tok")
	if err != nil {
		t.Fatal(err)
	}
	e, ok := embed.(*embeddings.Embedder)
	if !ok {
		t.Fatalf("embedder = %T, want *embeddings.Embedder", embed)
	}
	if e.MaxTokens != 0 {
		t.Fatalf("MaxTokens = %d, want 0 (caller falls back to EmbeddingMaxTokens)", e.MaxTokens)
	}
}

func TestNewEmbedderFactoryNilWithoutEmbeddingModel(t *testing.T) {
	f := NewEmbedderFactory()
	p := &domain.Provider{
		Kind:    domain.ProviderChat,
		BaseURL: "https://example.test/v1",
		Models:  []domain.Model{{ID: "chat-model", Kind: domain.ModelKindChat}},
	}
	embed, err := f(p, "tok")
	if err != nil {
		t.Fatal(err)
	}
	if embed != nil {
		t.Fatalf("embedder = %#v, want nil for a provider without embedding models", embed)
	}
}
