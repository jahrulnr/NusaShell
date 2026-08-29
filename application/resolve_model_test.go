package application

import (
	"testing"

	"nusashell/application/internal/service/learnedparams"
	"nusashell/domain"
)

type fakeCreds struct {
	keys map[string]string
}

func (c *fakeCreds) Get(id string) (string, bool, error) {
	k, ok := c.keys[id]
	return k, ok, nil
}
func (c *fakeCreds) Set(id, key string) error                { return nil }
func (c *fakeCreds) Delete(id string) error                  { return nil }
func (c *fakeCreds) ListByPrefix(p string) ([]string, error) { return nil, nil }

func TestSplitQualifiedModel(t *testing.T) {
	tests := []struct {
		input        string
		wantProvider string
		wantModel    string
		wantOk       bool
	}{
		{"tokenrouter:deepseek-v4-flash", "tokenrouter", "deepseek-v4-flash", true},
		{"openrouter:gpt-4o", "openrouter", "gpt-4o", true},
		{"gw:nomic-embed-text:latest", "gw", "nomic-embed-text:latest", true},
		{"deepseek-v4-flash", "", "", false},
		{"", "", "", false},
		{":model-only", "", "", false}, // empty provider is invalid
	}
	for _, tt := range tests {
		gotProvider, gotModel, gotOk := domain.SplitQualifiedModel(tt.input)
		if gotProvider != tt.wantProvider || gotModel != tt.wantModel || gotOk != tt.wantOk {
			t.Errorf("splitQualifiedModel(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tt.input, gotProvider, gotModel, gotOk, tt.wantProvider, tt.wantModel, tt.wantOk)
		}
	}
}

func TestResolveModelQualified(t *testing.T) {
	deepseekModel := domain.Model{ID: "deepseek-v4-flash", MaxOutput: 1048576}
	providers := &fakeProviderStore{items: map[string]*domain.Provider{
		"openrouter":  {ID: "openrouter", Enabled: true, Kind: domain.ProviderChat, BaseURL: "https://openrouter.ai/api/v1", Models: []domain.Model{deepseekModel}},
		"tokenrouter": {ID: "tokenrouter", Enabled: true, Kind: domain.ProviderResponses, BaseURL: "https://api.tokenrouter.io/v1", Models: []domain.Model{deepseekModel}},
	}}
	creds := &fakeCreds{keys: map[string]string{
		"openrouter":  "or-key",
		"tokenrouter": "tr-key",
	}}
	app := &App{Providers: providers, Credentials: creds}

	// Qualified: should pick tokenrouter, not openrouter
	p, _, key, err := app.resolveModelWithMeta("tokenrouter:deepseek-v4-flash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.ID != "tokenrouter" {
		t.Errorf("qualified model should resolve to tokenrouter, got %q", p.ID)
	}
	if key != "tr-key" {
		t.Errorf("should use tokenrouter API key, got %q", key)
	}

	// Qualified: should pick openrouter
	p, _, key, err = app.resolveModelWithMeta("openrouter:deepseek-v4-flash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.ID != "openrouter" {
		t.Errorf("qualified model should resolve to openrouter, got %q", p.ID)
	}
	if key != "or-key" {
		t.Errorf("should use openrouter API key, got %q", key)
	}

	// Unqualified: backward compat — first match
	p, _, _, err = app.resolveModelWithMeta("deepseek-v4-flash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.ID == "" {
		t.Error("unqualified model should still resolve via first-match")
	}
}

func TestResolveModelQualifiedNotFound(t *testing.T) {
	providers := &fakeProviderStore{items: map[string]*domain.Provider{
		"openrouter": {ID: "openrouter", Enabled: true, Kind: domain.ProviderChat, Models: []domain.Model{{ID: "gpt-4o"}}},
	}}
	creds := &fakeCreds{keys: map[string]string{"openrouter": "or-key"}}
	app := &App{Providers: providers, Credentials: creds}

	// Provider exists but doesn't have the model
	_, _, _, err := app.resolveModelWithMeta("openrouter:nonexistent")
	if err == nil {
		t.Error("expected error for model not on provider")
	}

	// Provider doesn't exist
	_, _, _, err = app.resolveModelWithMeta("ghost:deepseek-v4-flash")
	if err == nil {
		t.Error("expected error for nonexistent provider")
	}
}

func TestResolveModelWithMetaAppliesLearnedOverrides(t *testing.T) {
	providers := &fakeProviderStore{items: map[string]*domain.Provider{
		"tokenrouter": {ID: "tokenrouter", Enabled: true, Kind: domain.ProviderChat, Models: []domain.Model{{
			ID:      "qwen/qwen3.8-max-free",
			Context: 1_000_000,
			Vision:  true,
		}}},
	}}
	creds := &fakeCreds{keys: map[string]string{"tokenrouter": "tr-key"}}
	store := &fakeLearnedParamStore{}
	cache := learnedparams.New(store)
	cache.LearnFrom400("tokenrouter", "qwen/qwen3.8-max-free",
		`Requested token count exceeds the model's maximum context length of 262144 tokens.`)
	cache.LearnFrom400("tokenrouter", "qwen/qwen3.8-max-free",
		`Qwen3.8 open checkpoint is text-only; messages[131].content[1] must be a text part`)

	app := &App{Providers: providers, Credentials: creds, learnedParams: cache}
	_, m, _, err := app.resolveModelWithMeta("tokenrouter:qwen/qwen3.8-max-free")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m == nil {
		t.Fatal("expected model metadata")
	}
	if m.Context != 262144 {
		t.Errorf("learned cap should set Context to 262144, got %d", m.Context)
	}
	if m.Vision {
		t.Error("learned text-only should set Vision=false")
	}
}
