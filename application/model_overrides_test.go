package application

import (
	"testing"

	"nusashell/application/service/learnedparams"
	"nusashell/application/service/modeloverrides"
	"nusashell/domain"
)

// fakeModelOverrideStore is an in-memory ModelOverrideStore for testing.
type fakeModelOverrideStore struct {
	registry *domain.ModelOverrideRegistry
	saves    int
}

func (f *fakeModelOverrideStore) Load() *domain.ModelOverrideRegistry {
	if f.registry == nil {
		return domain.NewModelOverrideRegistry()
	}
	return f.registry
}

func (f *fakeModelOverrideStore) Save(r *domain.ModelOverrideRegistry) error {
	f.saves++
	f.registry = r
	return nil
}

func boolP(b bool) *bool { return &b }
func intP(i int) *int    { return &i }

// TestResolveModelWithMetaManualOverrideWins proves that a manual override
// applied at resolve time beats both the catalog value and a learned 400
// adaptation for the same field.
func TestResolveModelWithMetaManualOverrideWins(t *testing.T) {
	providers := &fakeProviderStore{items: map[string]*domain.Provider{
		"tokenrouter": {ID: "tokenrouter", Enabled: true, Kind: domain.ProviderChat, Models: []domain.Model{{
			ID:      "qwen/qwen3.8-max-free",
			Context: 1_000_000,
			Vision:  true,
		}}},
	}}
	creds := &fakeCreds{keys: map[string]string{"tokenrouter": "tr-key"}}

	// Learned: cap context to 262144 and disable vision (text-only).
	learned := learnedparams.New(&fakeLearnedParamStore{})
	learned.LearnFrom400("tokenrouter", "qwen/qwen3.8-max-free",
		`Requested token count exceeds the model's maximum context length of 262144 tokens.`)
	learned.LearnFrom400("tokenrouter", "qwen/qwen3.8-max-free",
		`Qwen3.8 open checkpoint is text-only; messages[131].content[1] must be a text part`)

	// Manual: assert context=1000000 and vision=true — must win.
	manual := modeloverrides.New(&fakeModelOverrideStore{})
	if err := manual.Set(&domain.ModelOverride{
		Provider: "tokenrouter", Model: "qwen/qwen3.8-max-free",
		Context: intP(1000000), Vision: boolP(true),
	}); err != nil {
		t.Fatalf("manual Set: %v", err)
	}

	app := &App{Providers: providers, Credentials: creds, learnedParams: learned, modelOverrides: manual}
	_, m, _, err := app.resolveModelWithMeta("tokenrouter:qwen/qwen3.8-max-free")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m == nil {
		t.Fatal("expected model metadata")
	}
	if m.Context != 1000000 {
		t.Errorf("manual override must win: Context = %d, want 1000000", m.Context)
	}
	if !m.Vision {
		t.Error("manual override must win: Vision should be true")
	}
}

// TestResolveModelWithMetaManualOverrideOnly proves a manual override applies
// cleanly when there is no learned adaptation.
func TestResolveModelWithMetaManualOverrideOnly(t *testing.T) {
	providers := &fakeProviderStore{items: map[string]*domain.Provider{
		"openrouter": {ID: "openrouter", Enabled: true, Kind: domain.ProviderChat, Models: []domain.Model{{
			ID:      "deepseek/deepseek-v4-flash",
			Context: 200000,
			Vision:  true,
		}}},
	}}
	creds := &fakeCreds{keys: map[string]string{"openrouter": "or-key"}}

	manual := modeloverrides.New(&fakeModelOverrideStore{})
	if err := manual.Set(&domain.ModelOverride{
		Provider: "openrouter", Model: "deepseek/deepseek-v4-flash",
		Vision: boolP(false),
	}); err != nil {
		t.Fatalf("manual Set: %v", err)
	}

	app := &App{Providers: providers, Credentials: creds, modelOverrides: manual}
	_, m, _, err := app.resolveModelWithMeta("openrouter:deepseek/deepseek-v4-flash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Vision {
		t.Error("manual Vision=false should disable vision")
	}
	if m.Context != 200000 {
		t.Errorf("untouched Context changed: %d", m.Context)
	}
}

// TestModelCapabilitiesManualOverrideWins proves the capabilities path also
// honors manual overrides over learned disabled modalities.
func TestModelCapabilitiesManualOverrideWins(t *testing.T) {
	learned := learnedparams.New(&fakeLearnedParamStore{})
	learned.LearnFrom400("openrouter", "qwen3.8-max-free", `text-only`)

	manual := modeloverrides.New(&fakeModelOverrideStore{})
	if err := manual.Set(&domain.ModelOverride{
		Provider: "openrouter", Model: "qwen3.8-max-free",
		Vision: boolP(true),
	}); err != nil {
		t.Fatalf("manual Set: %v", err)
	}

	provider := &domain.Provider{ID: "openrouter", Models: nil}
	caps := modelCapabilitiesWithLearned(provider, "qwen3.8-max-free", learned, manual)
	if !caps.Vision {
		t.Error("manual Vision=true must win over learned text-only")
	}
}

// TestResolveContextWindowManualOverrideWins proves the context-window path
// honors a manual context override over the learned cap.
func TestResolveContextWindowManualOverrideWins(t *testing.T) {
	learned := learnedparams.New(&fakeLearnedParamStore{})
	learned.LearnFrom400("tokenrouter", "qwen/qwen3.8-max-free",
		`maximum context length of 262144 tokens.`)

	manual := modeloverrides.New(&fakeModelOverrideStore{})
	if err := manual.Set(&domain.ModelOverride{
		Provider: "tokenrouter", Model: "qwen/qwen3.8-max-free",
		Context: intP(1000000),
	}); err != nil {
		t.Fatalf("manual Set: %v", err)
	}

	provider := &domain.Provider{ID: "tokenrouter", Models: []domain.Model{{
		ID: "qwen/qwen3.8-max-free", Context: 1000000,
	}}}
	app := &App{learnedParams: learned, modelOverrides: manual}
	settings := domain.DefaultSettings()
	if got := app.resolveContextWindow(provider, "qwen/qwen3.8-max-free", settings); got != 1000000 {
		t.Errorf("manual context override must win: got %d, want 1000000", got)
	}
}
