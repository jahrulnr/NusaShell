package tools

import (
	"testing"

	"nusashell/domain"
)

type swTestCreds struct {
	keys map[string]string
}

func (c *swTestCreds) Get(id string) (string, bool, error) {
	k, ok := c.keys[id]
	return k, ok, nil
}
func (c *swTestCreds) Set(id, key string) error                { return nil }
func (c *swTestCreds) Delete(id string) error                  { return nil }
func (c *swTestCreds) ListByPrefix(p string) ([]string, error) { return nil, nil }

type swTestProviders struct {
	items map[string]*domain.Provider
}

func (s *swTestProviders) List() []*domain.Provider {
	out := make([]*domain.Provider, 0, len(s.items))
	for _, p := range s.items {
		out = append(out, p)
	}
	return out
}
func (s *swTestProviders) Get(id string) (*domain.Provider, error) { return s.items[id], nil }
func (s *swTestProviders) Save(p *domain.Provider) error           { return nil }
func (s *swTestProviders) Delete(id string) error                  { return nil }

func TestSearchwireConfigFromProviders(t *testing.T) {
	providers := &swTestProviders{items: map[string]*domain.Provider{
		"openrouter": {ID: "openrouter", Enabled: true, Kind: domain.ProviderChat, BaseURL: "https://openrouter.ai/api/v1"},
		"openai":     {ID: "openai", Enabled: true, Kind: domain.ProviderResponses, BaseURL: "https://api.openai.com/v1"},
		"anthropic":  {ID: "anthropic", Enabled: true, Kind: domain.ProviderMessages, BaseURL: "https://api.anthropic.com"},
		"perplexity": {ID: "perplexity", Enabled: true, Kind: domain.ProviderChat, BaseURL: "https://api.perplexity.ai"},
		"xai":        {ID: "xai", Enabled: true, Kind: domain.ProviderResponses, BaseURL: "https://api.x.ai/v1"},
		"disabled":   {ID: "disabled", Enabled: false, Kind: domain.ProviderChat, BaseURL: "https://openrouter.ai/api/v1"},
		"nokey":      {ID: "nokey", Enabled: true, Kind: domain.ProviderChat, BaseURL: "https://openrouter.ai/api/v1"},
		"unknown":    {ID: "unknown", Enabled: true, Kind: domain.ProviderChat, BaseURL: "https://custom.example.com/v1"},
	}}
	creds := &swTestCreds{keys: map[string]string{
		"openrouter": "or-key",
		"openai":     "oai-key",
		"anthropic":  "ant-key",
		"perplexity": "ppx-key",
		"xai":        "xai-key",
		"disabled":   "dis-key",
		"unknown":    "unk-key",
	}}

	cfg := SearchwireConfigFromProviders(providers, creds)

	if cfg.OpenRouter.APIKey != "or-key" {
		t.Errorf("OpenRouter key = %q, want or-key", cfg.OpenRouter.APIKey)
	}
	if cfg.OpenAI.APIKey != "oai-key" {
		t.Errorf("OpenAI key = %q, want oai-key", cfg.OpenAI.APIKey)
	}
	if cfg.Anthropic.APIKey != "ant-key" {
		t.Errorf("Anthropic key = %q, want ant-key", cfg.Anthropic.APIKey)
	}
	if cfg.Perplexity.APIKey != "ppx-key" {
		t.Errorf("Perplexity key = %q, want ppx-key", cfg.Perplexity.APIKey)
	}
	if cfg.XAI.APIKey != "xai-key" {
		t.Errorf("XAI key = %q, want xai-key", cfg.XAI.APIKey)
	}
}

func TestSearchwireConfigFromProvidersDisabled(t *testing.T) {
	providers := &swTestProviders{items: map[string]*domain.Provider{
		"openrouter": {ID: "openrouter", Enabled: false, Kind: domain.ProviderChat, BaseURL: "https://openrouter.ai/api/v1"},
	}}
	creds := &swTestCreds{keys: map[string]string{"openrouter": "or-key"}}

	cfg := SearchwireConfigFromProviders(providers, creds)
	if cfg.OpenRouter.APIKey != "" {
		t.Errorf("disabled provider should not contribute key, got %q", cfg.OpenRouter.APIKey)
	}
}

func TestSearchwireConfigFromProvidersNoKey(t *testing.T) {
	providers := &swTestProviders{items: map[string]*domain.Provider{
		"openrouter": {ID: "openrouter", Enabled: true, Kind: domain.ProviderChat, BaseURL: "https://openrouter.ai/api/v1"},
	}}
	creds := &swTestCreds{keys: map[string]string{}}

	cfg := SearchwireConfigFromProviders(providers, creds)
	if cfg.OpenRouter.APIKey != "" {
		t.Errorf("provider without key should not contribute, got %q", cfg.OpenRouter.APIKey)
	}
}

func TestSearchwireConfigFromProvidersNil(t *testing.T) {
	cfg := SearchwireConfigFromProviders(nil, nil)
	if cfg.OpenRouter.APIKey != "" {
		t.Error("nil stores should produce empty config")
	}
}
