package application

import (
	"errors"
	"testing"

	"nusashell/domain"
)

// fakeLearnedParamStore is an in-memory LearnedParamStore for testing.
type fakeLearnedParamStore struct {
	registry *domain.LearnedParamRegistry
	saves    int
}

func (f *fakeLearnedParamStore) Load() *domain.LearnedParamRegistry {
	if f.registry == nil {
		return domain.NewLearnedParamRegistry()
	}
	return f.registry
}

func (f *fakeLearnedParamStore) Save(r *domain.LearnedParamRegistry) error {
	f.saves++
	f.registry = r
	return nil
}

func TestLearnedParamsCacheLearnFrom400Strip(t *testing.T) {
	store := &fakeLearnedParamStore{}
	cache := newLearnedParamsCache(store)

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

func TestLearnedParamsCacheLearnFrom400Inject(t *testing.T) {
	store := &fakeLearnedParamStore{}
	cache := newLearnedParamsCache(store)

	action, param := cache.LearnFrom400("openrouter", "stealth/ox-alpha",
		`reasoning_content must be passed back`)
	if action != domain.LearnedActionInject || param != "reasoning_content" {
		t.Fatalf("LearnFrom400 = (%q, %q), want (inject, reasoning_content)", action, param)
	}
	if !cache.HasInjectFor("openrouter", "stealth/ox-alpha") {
		t.Error("HasInjectFor returned false after learning inject")
	}
	if !hasLearnedReasoningReplay(cache, "openrouter", "stealth/ox-alpha") {
		t.Error("hasLearnedReasoningReplay returned false after learning reasoning_content inject")
	}
}

func TestLearnedParamsCacheLearnFrom400DisableModality(t *testing.T) {
	store := &fakeLearnedParamStore{}
	cache := newLearnedParamsCache(store)

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

func TestLearnedParamsCacheDisabledModalitiesNilSafe(t *testing.T) {
	var cache *learnedParamsCache
	if cache.DisabledModalities("p", "m") != nil {
		t.Error("nil cache DisabledModalities must return nil")
	}
}

func TestLearnedParamsCacheNoMatch(t *testing.T) {
	store := &fakeLearnedParamStore{}
	cache := newLearnedParamsCache(store)

	action, param := cache.LearnFrom400("p", "m", "internal server error")
	if action != "" || param != "" {
		t.Errorf("LearnFrom400 on unknown body = (%q, %q), want empty", action, param)
	}
	if store.saves != 0 {
		t.Errorf("no match should not save, got %d saves", store.saves)
	}
}

func TestLearnedParamsCacheNilSafe(t *testing.T) {
	// A cache built from a nil store still works (empty registry, no
	// persistence). This mirrors the App path when LearnedParams is nil.
	cache := newLearnedParamsCache(nil)
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

func TestLearnedParamsCachePersistAcrossInstances(t *testing.T) {
	store := &fakeLearnedParamStore{}
	cache1 := newLearnedParamsCache(store)
	cache1.LearnFrom400("openrouter", "glm-5.2", "Unsupported parameter: logprobs")

	// Simulate process restart: new cache loads from store
	cache2 := newLearnedParamsCache(store)
	strip := cache2.StripParams("openrouter", "glm-5.2")
	if len(strip) != 1 || strip[0] != "logprobs" {
		t.Fatalf("learned rule did not survive restart: %v", strip)
	}
}

func TestIsLearnable400(t *testing.T) {
	if !isLearnable400(&UpstreamError{StatusCode: 400, Err: errors.New("bad request")}) {
		t.Error("400 should be learnable")
	}
	if isLearnable400(&UpstreamError{StatusCode: 429, Err: errors.New("rate limit")}) {
		t.Error("429 should not be learnable")
	}
	if isLearnable400(&UpstreamError{StatusCode: 500, Err: errors.New("server error")}) {
		t.Error("500 should not be learnable")
	}
	if isLearnable400(errors.New("plain error")) {
		t.Error("non-UpstreamError should not be learnable")
	}
}

func TestApplyStrippedParams(t *testing.T) {
	store := &fakeLearnedParamStore{}
	cache := newLearnedParamsCache(store)
	cache.LearnFrom400("openrouter", "glm-5.2", "Unsupported parameter: logprobs")

	body := map[string]any{
		"model":    "glm-5.2",
		"logprobs": true,
		"stream":   true,
	}
	stripped := applyStrippedParams(body, "openrouter", "glm-5.2", cache)
	if len(stripped) != 1 || stripped[0] != "logprobs" {
		t.Fatalf("stripped = %v, want [logprobs]", stripped)
	}
	if _, ok := body["logprobs"]; ok {
		t.Error("logprobs should have been deleted from body")
	}
	if _, ok := body["stream"]; !ok {
		t.Error("stream should still be present")
	}
}

func TestExtractErrBody(t *testing.T) {
	if got := extractErrBody(nil); got != "" {
		t.Errorf("extractErrBody(nil) = %q, want empty", got)
	}
	if got := extractErrBody(errors.New("plain")); got != "plain" {
		t.Errorf("extractErrBody(plain) = %q, want plain", got)
	}
	if got := extractErrBody(&UpstreamError{StatusCode: 400, Err: errors.New("unsupported")}); got != "unsupported" {
		t.Errorf("extractErrBody(upstream) = %q, want unsupported", got)
	}
}
