// Package tools searchwire_config.go builds a searchwire.Config from the
// providers and credentials already configured in NusaShell, so web_answer
// works without separate environment variables.
package tools

import (
	"strings"

	"nusashell/application"

	"github.com/jahrulnr/searchwire"
)

// SearchwireConfigFromProviders builds a searchwire.Config using API keys
// from NusaShell's configured providers. When a provider's BaseURL matches a
// known vendor (OpenRouter, OpenAI, Anthropic, Perplexity, xAI), its API key
// is passed to searchwire so web_answer works without separate env vars.
//
// Brave is not a chat provider in NusaShell, so it still relies on the
// BRAVE_SEARCH_API_KEY env var if the user wants the Brave answer provider.
func SearchwireConfigFromProviders(providers application.ProviderStore, creds application.CredentialStore) searchwire.Config {
	cfg := searchwire.Config{}
	if providers == nil || creds == nil {
		return cfg
	}
	for _, p := range providers.List() {
		if !p.Enabled {
			continue
		}
		key, has, _ := creds.Get(p.ID)
		if !has || strings.TrimSpace(key) == "" {
			continue
		}
		host := strings.ToLower(p.BaseURL)
		switch {
		case strings.Contains(host, "openrouter.ai"):
			cfg.OpenRouter = searchwire.OpenRouterConfig{APIKey: key}
		case strings.Contains(host, "api.openai.com"):
			cfg.OpenAI = searchwire.OpenAIConfig{APIKey: key}
		case strings.Contains(host, "anthropic.com"):
			cfg.Anthropic = searchwire.AnthropicConfig{APIKey: key}
		case strings.Contains(host, "perplexity.ai"):
			cfg.Perplexity = searchwire.PerplexityConfig{APIKey: key}
		case strings.Contains(host, "api.x.ai") || strings.Contains(host, "xai"):
			cfg.XAI = searchwire.XAIConfig{APIKey: key}
		}
	}
	return cfg
}
