package application

import (
	"testing"

	"nusashell/contracts"
	"nusashell/domain"
)

func strPtr(s string) *string { return &s }

func TestHandleProvidersSavePersistsCacheTTL(t *testing.T) {
	app, providers, _ := newSeedTestApp()

	res, rpcErr := app.handleProvidersSave(contracts.ProviderSaveRequest{
		Kind:     "messages",
		Name:     "Anthropic",
		BaseURL:  "https://api.anthropic.com",
		Enabled:  true,
		CacheTTL: strPtr("1h"),
	})
	if rpcErr != nil {
		t.Fatalf("save: %+v", rpcErr)
	}
	out, ok := res.(contracts.ProvidersListResult)
	if !ok || len(out.Providers) != 1 {
		t.Fatalf("result = %#v", res)
	}
	if out.Providers[0].CacheTTL != "1h" {
		t.Errorf("dto cache_ttl = %q, want 1h", out.Providers[0].CacheTTL)
	}
	stored, err := providers.Get(out.Providers[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.CacheTTL != "1h" {
		t.Errorf("stored cache_ttl = %q, want 1h", stored.CacheTTL)
	}
}

func TestHandleProvidersSaveRejectsInvalidCacheTTL(t *testing.T) {
	app, _, _ := newSeedTestApp()
	_, rpcErr := app.handleProvidersSave(contracts.ProviderSaveRequest{
		Kind:     "messages",
		Name:     "Anthropic",
		BaseURL:  "https://api.anthropic.com",
		Enabled:  true,
		CacheTTL: strPtr("30m"),
	})
	if rpcErr == nil || rpcErr.Code != contracts.CodeValidation {
		t.Fatalf("30m on messages must be VALIDATION_ERROR, got %+v", rpcErr)
	}
}

func TestHandleProvidersSavePreservesCacheTTLWhenOmitted(t *testing.T) {
	app, providers, _ := newSeedTestApp()
	created, rpcErr := app.handleProvidersSave(contracts.ProviderSaveRequest{
		Kind:     "messages",
		Name:     "Anthropic",
		BaseURL:  "https://api.anthropic.com",
		Enabled:  true,
		CacheTTL: strPtr("1h"),
	})
	if rpcErr != nil {
		t.Fatalf("create: %+v", rpcErr)
	}
	id := created.(contracts.ProvidersListResult).Providers[0].ID

	_, rpcErr = app.handleProvidersSave(contracts.ProviderSaveRequest{
		ID:      id,
		Kind:    "messages",
		Name:    "Anthropic",
		BaseURL: "https://api.anthropic.com",
		Enabled: true,
	})
	if rpcErr != nil {
		t.Fatalf("update: %+v", rpcErr)
	}
	stored, err := providers.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if stored.CacheTTL != "1h" {
		t.Errorf("omitted cache_ttl wiped stored value: %q", stored.CacheTTL)
	}
}

func TestHandleProvidersSaveClearsInvalidTTLOnKindChange(t *testing.T) {
	app, providers, _ := newSeedTestApp()
	created, rpcErr := app.handleProvidersSave(contracts.ProviderSaveRequest{
		Driver:   "openrouter",
		Kind:     "chat",
		Name:     "Gateway",
		BaseURL:  "https://openrouter.ai/api/v1",
		Enabled:  true,
		CacheTTL: strPtr("1h"),
	})
	if rpcErr != nil {
		t.Fatalf("create: %+v", rpcErr)
	}
	id := created.(contracts.ProvidersListResult).Providers[0].ID

	_, rpcErr = app.handleProvidersSave(contracts.ProviderSaveRequest{
		ID:      id,
		Driver:  "openrouter",
		Kind:    "responses",
		Name:    "Gateway",
		BaseURL: "https://openrouter.ai/api/v1",
		Enabled: true,
	})
	if rpcErr != nil {
		t.Fatalf("kind change: %+v", rpcErr)
	}
	stored, err := providers.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if stored.CacheTTL != "" {
		t.Errorf("1h must be cleared when kind becomes responses, got %q", stored.CacheTTL)
	}
	dto := app.providerDTO(stored)
	if dto.CacheTTL != "30m" {
		t.Errorf("effective dto cache_ttl = %q, want 30m", dto.CacheTTL)
	}
}

func TestProviderDTOAdvertisesSendableCacheTTLs(t *testing.T) {
	app, _, _ := newSeedTestApp()
	or := &domain.Provider{
		ID: "openrouter", Driver: domain.ProviderDriverOpenRouter, Kind: domain.ProviderChat,
		Name: "OpenRouter", Enabled: true, BaseURL: "https://openrouter.ai/api/v1",
	}
	dto := app.providerDTO(or)
	if got := dto.CacheTTLs; len(got) != 3 || got[0] != "5m" || got[1] != "1h" || got[2] != domain.CacheTTLOff {
		t.Errorf("openrouter chat cache_ttls = %v, want [5m 1h off]", dto.CacheTTLs)
	}
	if dto.CacheTTL != "5m" {
		t.Errorf("openrouter default cache_ttl = %q, want 5m", dto.CacheTTL)
	}

	oa := &domain.Provider{
		ID: "openai", Driver: domain.ProviderDriverOpenAI, Kind: domain.ProviderResponses,
		Name: "OpenAI", Enabled: true,
	}
	dto = app.providerDTO(oa)
	if got := dto.CacheTTLs; len(got) != 2 || got[0] != "30m" || got[1] != domain.CacheTTLOff {
		t.Errorf("openai cache_ttls = %v, want [30m off]", dto.CacheTTLs)
	}
	if dto.CacheTTL != "30m" {
		t.Errorf("openai default cache_ttl = %q, want 30m", dto.CacheTTL)
	}

	oc := &domain.Provider{
		ID: "prov_oc", Driver: domain.ProviderDriverOpenRouter, Kind: domain.ProviderChat,
		Name: "OpenCode", Enabled: true, BaseURL: "https://opencode.ai/zen/go/v1", CacheTTL: "1h",
	}
	dto = app.providerDTO(oc)
	if got := dto.CacheTTLs; len(got) != 3 || got[0] != "5m" || got[1] != "1h" || got[2] != domain.CacheTTLOff {
		t.Errorf("opencode cache_ttls = %v, want [5m 1h off]", dto.CacheTTLs)
	}
	if dto.CacheTTL != "1h" {
		t.Errorf("opencode effective cache_ttl = %q, want 1h", dto.CacheTTL)
	}
}

func TestHandleProvidersSavePersistsCacheTTLOff(t *testing.T) {
	app, providers, _ := newSeedTestApp()
	res, rpcErr := app.handleProvidersSave(contracts.ProviderSaveRequest{
		Kind:     "messages",
		Name:     "Anthropic",
		BaseURL:  "https://api.anthropic.com",
		Enabled:  true,
		CacheTTL: strPtr(domain.CacheTTLOff),
	})
	if rpcErr != nil {
		t.Fatalf("save: %+v", rpcErr)
	}
	out := res.(contracts.ProvidersListResult)
	if out.Providers[0].CacheTTL != domain.CacheTTLOff {
		t.Errorf("dto cache_ttl = %q, want off", out.Providers[0].CacheTTL)
	}
	stored, err := providers.Get(out.Providers[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.CacheTTL != domain.CacheTTLOff {
		t.Errorf("stored cache_ttl = %q, want off", stored.CacheTTL)
	}
}
