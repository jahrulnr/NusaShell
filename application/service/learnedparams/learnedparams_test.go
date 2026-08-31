package learnedparams

import (
	"testing"

	"nusashell/domain"
)

type fakeStore struct {
	registry *domain.LearnedParamRegistry
	saves    int
}

func (f *fakeStore) Load() *domain.LearnedParamRegistry {
	if f.registry == nil {
		return domain.NewLearnedParamRegistry()
	}
	return f.registry
}

func (f *fakeStore) Save(r *domain.LearnedParamRegistry) error {
	f.saves++
	f.registry = r
	return nil
}

func TestCacheLearnFrom400Strip(t *testing.T) {
	store := &fakeStore{}
	cache := New(store)

	action, param := cache.LearnFrom400("openrouter", "glm-5.2",
		`Unsupported parameter: logprobs`)
	if action != domain.LearnedActionStrip || param != "logprobs" {
		t.Fatalf("LearnFrom400 = (%q, %q), want (strip, logprobs)", action, param)
	}
	strip := cache.StripParams("openrouter", "glm-5.2")
	if len(strip) != 1 || strip[0] != "logprobs" {
		t.Fatalf("StripParams = %v, want [logprobs]", strip)
	}
	if store.saves != 1 {
		t.Errorf("expected 1 save, got %d", store.saves)
	}
}

func TestCacheLearnFrom400Inject(t *testing.T) {
	store := &fakeStore{}
	cache := New(store)

	action, param := cache.LearnFrom400("openrouter", "stealth/ox-alpha",
		`reasoning_content must be passed back`)
	if action != domain.LearnedActionInject || param != "reasoning_content" {
		t.Fatalf("LearnFrom400 = (%q, %q), want (inject, reasoning_content)", action, param)
	}
	if !cache.HasInjectFor("openrouter", "stealth/ox-alpha") {
		t.Error("HasInjectFor returned false after learning inject")
	}
}

func TestCacheLearnFrom400CapContext(t *testing.T) {
	store := &fakeStore{}
	cache := New(store)

	body := `Requested token count exceeds the model's maximum context length of 262144 tokens. You requested a total of 267042 tokens.`
	action, param := cache.LearnFrom400("tokenrouter", "qwen/qwen3.8-max-free", body)
	if action != domain.LearnedActionCapContext || param != "262144" {
		t.Fatalf("LearnFrom400 = (%q, %q), want (cap_context, 262144)", action, param)
	}
	if got := cache.ContextCap("tokenrouter", "qwen/qwen3.8-max-free"); got != 262144 {
		t.Fatalf("ContextCap = %d, want 262144", got)
	}
	if store.saves != 1 {
		t.Errorf("expected 1 save, got %d", store.saves)
	}

	// Smallest cap wins on a second, larger observation.
	cache.LearnFrom400("tokenrouter", "qwen/qwen3.8-max-free",
		`This model's maximum context length is 500000 tokens.`)
	if got := cache.ContextCap("tokenrouter", "qwen/qwen3.8-max-free"); got != 262144 {
		t.Fatalf("ContextCap should keep smallest, got %d", got)
	}
}

func TestCacheContextCapNilSafe(t *testing.T) {
	var cache *Cache
	if got := cache.ContextCap("p", "m"); got != 0 {
		t.Errorf("nil cache ContextCap must return 0, got %d", got)
	}
}

func TestCacheLearnFrom400DisableModality(t *testing.T) {
	store := &fakeStore{}
	cache := New(store)

	action, param := cache.LearnFrom400("openrouter", "qwen3.8-max-free",
		`Qwen3.8 open checkpoint is text-only; messages[131].content[1] must be a text part`)
	if action != domain.LearnedActionDisableModality || param != "vision" {
		t.Fatalf("LearnFrom400 = (%q, %q), want (disable_modality, vision)", action, param)
	}
	disabled := cache.DisabledModalities("openrouter", "qwen3.8-max-free")
	if len(disabled) != 1 || disabled[0] != "vision" {
		t.Fatalf("DisabledModalities = %v, want [vision]", disabled)
	}
	if store.saves != 1 {
		t.Errorf("expected 1 save, got %d", store.saves)
	}
}

func TestCacheDisabledModalitiesNilSafe(t *testing.T) {
	var cache *Cache
	if cache.DisabledModalities("p", "m") != nil {
		t.Error("nil cache DisabledModalities must return nil")
	}
}

func TestCacheNeedsUserNudge(t *testing.T) {
	store := &fakeStore{}
	cache := New(store)
	if cache.NeedsUserNudge("openrouter", "glm-5.2") {
		t.Fatal("expected false before learning")
	}
	action, param := cache.LearnFrom400("openrouter", "glm-5.2",
		`bad_response_status_code: No user query found in messages.`)
	if action != domain.LearnedActionNudgeUser || param != "user_message" {
		t.Fatalf("LearnFrom400 = (%q, %q), want (nudge_user, user_message)", action, param)
	}
	if !cache.NeedsUserNudge("openrouter", "glm-5.2") {
		t.Fatal("expected true after learning")
	}
	if cache.NeedsUserNudge("openrouter", "gpt-5") {
		t.Fatal("expected false for different model")
	}
}

func TestCacheNeedsUserNudgeNilSafe(t *testing.T) {
	var cache *Cache
	if cache.NeedsUserNudge("p", "m") {
		t.Error("nil cache NeedsUserNudge must return false")
	}
}

func TestCacheNoMatch(t *testing.T) {
	store := &fakeStore{}
	cache := New(store)

	action, param := cache.LearnFrom400("p", "m", "internal server error")
	if action != "" || param != "" {
		t.Errorf("LearnFrom400 on unknown body = (%q, %q), want empty", action, param)
	}
	if store.saves != 0 {
		t.Errorf("no match should not save, got %d saves", store.saves)
	}
}

func TestCacheNilSafe(t *testing.T) {
	// A cache built from a nil store still works (empty registry, no
	// persistence). This mirrors the App path when LearnedParams is nil.
	cache := New(nil)
	if cache.StripParams("p", "m") != nil {
		t.Error("nil-store cache StripParams must return nil")
	}
	if cache.InjectParams("p", "m") != nil {
		t.Error("nil-store cache InjectParams must return nil")
	}
	// LearnFrom400 on a nil-store cache classifies but does not persist.
	action, param := cache.LearnFrom400("p", "m", "Unsupported parameter: logprobs")
	if action != domain.LearnedActionStrip || param != "logprobs" {
		t.Errorf("nil-store LearnFrom400 = (%q, %q), want (strip, logprobs)", action, param)
	}
	// The learned rule is in memory even without a store.
	if got := cache.StripParams("p", "m"); len(got) != 1 || got[0] != "logprobs" {
		t.Errorf("nil-store learned rule missing: %v", got)
	}
}

func TestCachePersistAcrossInstances(t *testing.T) {
	store := &fakeStore{}
	cache1 := New(store)
	cache1.LearnFrom400("openrouter", "glm-5.2", "Unsupported parameter: logprobs")

	// Simulate process restart: new cache loads from store
	cache2 := New(store)
	strip := cache2.StripParams("openrouter", "glm-5.2")
	if len(strip) != 1 || strip[0] != "logprobs" {
		t.Fatalf("learned rule did not survive restart: %v", strip)
	}
}

// TestCacheSanitizeOnLoad proves that garbage entries persisted by older
// classifier versions (param="this") are dropped when the cache is built,
// and the cleaned registry is saved back so the cleanup persists.
func TestCacheSanitizeOnLoad(t *testing.T) {
	store := &fakeStore{}
	// Seed the store the way an old binary would have left it: one garbage
	// inject entry plus one valid strip entry.
	seed := domain.NewLearnedParamRegistry()
	seed.RecordInject("prov", "gemini-3.7-flash", "this", "This is required")
	seed.RecordStrip("openrouter", "glm-5.2", "logprobs", "Unsupported parameter: logprobs")
	store.registry = seed

	cache := New(store)

	// Garbage is gone from the in-memory registry.
	if got := cache.InjectParams("prov", "gemini-3.7-flash"); len(got) != 0 {
		t.Errorf("garbage inject entry survived: %v", got)
	}
	// Valid entry survives.
	if got := cache.StripParams("openrouter", "glm-5.2"); len(got) != 1 || got[0] != "logprobs" {
		t.Errorf("valid strip entry lost: %v", got)
	}
	// The cleaned registry was persisted back.
	if store.saves != 1 {
		t.Errorf("expected 1 save after sanitize, got %d", store.saves)
	}
	if store.registry.Lookup("prov", "gemini-3.7-flash", "this") != nil {
		t.Error("garbage entry still present in persisted store")
	}

	// A second construction finds nothing to sanitize and does not save.
	_ = New(store)
	if store.saves != 1 {
		t.Errorf("second construction should not re-save, saves = %d", store.saves)
	}
}
