package domain

import (
	"strings"
	"testing"
)

func TestLearnedParamRegistryRecordAndLookup(t *testing.T) {
	r := NewLearnedParamRegistry()
	r.RecordStrip("openrouter", "glm-5.2", "logprobs", "Unsupported parameter: logprobs")
	r.RecordInject("openrouter", "stealth/ox-alpha", "reasoning_content", "reasoning_content must be passed back")

	if e := r.Lookup("openrouter", "glm-5.2", "logprobs"); e == nil || e.Action != LearnedActionStrip {
		t.Fatalf("strip entry missing or wrong action: %+v", e)
	}
	if e := r.Lookup("openrouter", "stealth/ox-alpha", "reasoning_content"); e == nil || e.Action != LearnedActionInject {
		t.Fatalf("inject entry missing or wrong action: %+v", e)
	}
}

func TestLearnedParamRegistryNeedsUserNudge(t *testing.T) {
	r := NewLearnedParamRegistry()
	if r.NeedsUserNudge("openrouter", "glm-5.2") {
		t.Fatal("expected false before learning")
	}
	r.RecordNudgeUser("openrouter", "glm-5.2", "user_message", "No user query found in messages.")
	if !r.NeedsUserNudge("openrouter", "glm-5.2") {
		t.Fatal("expected true after learning")
	}
	if r.NeedsUserNudge("openrouter", "gpt-5") {
		t.Fatal("expected false for different model")
	}
	if r.NeedsUserNudge("anthropic", "glm-5.2") {
		t.Fatal("expected false for different provider")
	}
}

func TestLearnedParamRegistryStripParams(t *testing.T) {
	r := NewLearnedParamRegistry()
	r.RecordStrip("openrouter", "glm-5.2", "logprobs", "")
	r.RecordStrip("openrouter", "glm-5.2", "top_logprobs", "")
	r.RecordInject("openrouter", "glm-5.2", "reasoning_content", "")
	r.RecordStrip("openrouter", "gpt-5", "temperature", "")

	strip := r.StripParams("openrouter", "glm-5.2")
	if len(strip) != 2 {
		t.Fatalf("expected 2 strip params, got %d: %v", len(strip), strip)
	}
	// Must not contain reasoning_content (that's inject, not strip)
	for _, p := range strip {
		if p == "reasoning_content" {
			t.Errorf("strip list must not contain inject param: %v", strip)
		}
	}
	// Must not contain gpt-5's temperature
	for _, p := range strip {
		if p == "temperature" {
			t.Errorf("strip list leaked from different model: %v", strip)
		}
	}
}

func TestLearnedParamRegistryInjectParams(t *testing.T) {
	r := NewLearnedParamRegistry()
	r.RecordInject("openrouter", "ox-alpha", "reasoning_content", "")
	r.RecordStrip("openrouter", "ox-alpha", "logprobs", "")

	inject := r.InjectParams("openrouter", "ox-alpha")
	if len(inject) != 1 || inject[0] != "reasoning_content" {
		t.Fatalf("expected [reasoning_content], got %v", inject)
	}
}

func TestLearnedParamRegistryCaseInsensitive(t *testing.T) {
	r := NewLearnedParamRegistry()
	r.RecordStrip("OpenRouter", "GLM-5.2", "LogProbs", "")

	strip := r.StripParams("openrouter", "glm-5.2")
	if len(strip) != 1 || strip[0] != "logprobs" {
		t.Fatalf("case-insensitive lookup failed: %v", strip)
	}
}

func TestLearnedParamRegistryBumpHit(t *testing.T) {
	r := NewLearnedParamRegistry()
	r.RecordStrip("p", "m", "logprobs", "first")
	if e := r.Lookup("p", "m", "logprobs"); e.HitCount != 1 {
		t.Fatalf("initial HitCount = %d, want 1", e.HitCount)
	}
	// Re-record bumps hit count
	r.RecordStrip("p", "m", "logprobs", "second")
	if e := r.Lookup("p", "m", "logprobs"); e.HitCount != 2 {
		t.Fatalf("after re-record HitCount = %d, want 2", e.HitCount)
	}
	// BumpHit also works
	r.BumpHit("p", "m", "logprobs")
	if e := r.Lookup("p", "m", "logprobs"); e.HitCount != 3 {
		t.Fatalf("after BumpHit HitCount = %d, want 3", e.HitCount)
	}
}

func TestLearnedParamRegistryRemove(t *testing.T) {
	r := NewLearnedParamRegistry()
	r.RecordStrip("p", "m", "logprobs", "")
	if !r.Remove("p", "m", "logprobs") {
		t.Fatal("Remove returned false for existing entry")
	}
	if r.Lookup("p", "m", "logprobs") != nil {
		t.Fatal("entry still present after Remove")
	}
	if r.Remove("p", "m", "logprobs") {
		t.Fatal("Remove returned true for non-existent entry")
	}
}

func TestLearnedParamRegistryNilSafe(t *testing.T) {
	var r *LearnedParamRegistry
	if r.StripParams("p", "m") != nil {
		t.Error("nil registry StripParams must return nil")
	}
	if r.InjectParams("p", "m") != nil {
		t.Error("nil registry InjectParams must return nil")
	}
	if r.DisabledModalities("p", "m") != nil {
		t.Error("nil registry DisabledModalities must return nil")
	}
	if r.Lookup("p", "m", "x") != nil {
		t.Error("nil registry Lookup must return nil")
	}
	if r.Len() != 0 {
		t.Error("nil registry Len must return 0")
	}
}

func TestLearnedParamRegistryDisableModality(t *testing.T) {
	r := NewLearnedParamRegistry()
	r.RecordDisableModality("openrouter", "qwen3.8-max-free", "vision", "text-only")

	modalities := r.DisabledModalities("openrouter", "qwen3.8-max-free")
	if len(modalities) != 1 || modalities[0] != "vision" {
		t.Fatalf("expected [vision], got %v", modalities)
	}
	// Different model should not see this entry
	if m := r.DisabledModalities("openrouter", "other-model"); len(m) != 0 {
		t.Fatalf("entry leaked to different model: %v", m)
	}
}

func TestLearnedParamRegistryDisableModalityCaseInsensitive(t *testing.T) {
	r := NewLearnedParamRegistry()
	r.RecordDisableModality("OpenRouter", "Qwen3.8", "Vision", "")

	modalities := r.DisabledModalities("openrouter", "qwen3.8")
	if len(modalities) != 1 || modalities[0] != "vision" {
		t.Fatalf("case-insensitive lookup failed: %v", modalities)
	}
}

func TestLearnedParamRegistryContextCap(t *testing.T) {
	r := NewLearnedParamRegistry()
	r.RecordCapContext("tokenrouter", "qwen/qwen3.8-max-free", "262144", "exceeded context")

	if got := r.ContextCap("tokenrouter", "qwen/qwen3.8-max-free"); got != 262144 {
		t.Fatalf("ContextCap = %d, want 262144", got)
	}
	// Different provider/model should not see this cap.
	if got := r.ContextCap("openrouter", "qwen/qwen3.8-max-free"); got != 0 {
		t.Fatalf("ContextCap leaked across providers: %d", got)
	}

	// Multiple caps for the same pair: smallest wins.
	r.RecordCapContext("tokenrouter", "qwen/qwen3.8-max-free", "500000", "later error")
	if got := r.ContextCap("tokenrouter", "qwen/qwen3.8-max-free"); got != 262144 {
		t.Fatalf("ContextCap should be smallest, got %d", got)
	}

	// Invalid param is ignored.
	r.RecordCapContext("tokenrouter", "qwen/qwen3.8-max-free", "not-a-number", "bad")
	if got := r.ContextCap("tokenrouter", "qwen/qwen3.8-max-free"); got != 262144 {
		t.Fatalf("ContextCap should ignore invalid param, got %d", got)
	}
}

func TestLearnedParamRegistryContextCapNilSafe(t *testing.T) {
	var r *LearnedParamRegistry
	if got := r.ContextCap("p", "m"); got != 0 {
		t.Errorf("nil registry ContextCap must return 0, got %d", got)
	}
}

func TestTruncateReason(t *testing.T) {
	short := "error message"
	if got := truncateReason(short); got != short {
		t.Errorf("truncateReason short = %q, want %q", got, short)
	}
	long := strings.Repeat("x", 250)
	got := truncateReason(long)
	if len(got) != 203 { // 200 + 3-byte ellipsis
		t.Errorf("truncateReason long len = %d, want 203", len(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncateReason long must end with ellipsis, got %q", got)
	}
}

func TestLearnedParamRegistryOverrideModel(t *testing.T) {
	r := NewLearnedParamRegistry()
	r.RecordCapContext("tokenrouter", "qwen/qwen3.8-max-free", "262144", "exceeded context")
	r.RecordDisableModality("tokenrouter", "qwen/qwen3.8-max-free", "vision", "text-only")

	m := &Model{
		ID:      "qwen/qwen3.8-max-free",
		Context: 1_000_000,
		Vision:  true,
		Audio:   true,
	}
	if !r.OverrideModel(m, "tokenrouter", "qwen/qwen3.8-max-free") {
		t.Fatal("OverrideModel should have changed the model")
	}
	if m.Context != 262144 {
		t.Errorf("Context = %d, want 262144", m.Context)
	}
	if m.Vision {
		t.Error("Vision should be disabled by learned modality")
	}
	if !m.Audio {
		t.Error("Audio should not be touched")
	}

	// A larger learned cap should not re-expand a model that was already
	// capped to a smaller value.
	r.RecordCapContext("tokenrouter", "qwen/qwen3.8-max-free", "500000", "larger cap")
	if r.OverrideModel(m, "tokenrouter", "qwen/qwen3.8-max-free") {
		t.Error("OverrideModel should not change the model when learned cap is larger")
	}
	if m.Context != 262144 {
		t.Errorf("Context should stay at 262144, got %d", m.Context)
	}

	// Learned overrides must not leak to a different provider+model.
	other := &Model{ID: "other-model", Context: 1_000_000, Vision: true}
	if r.OverrideModel(other, "openrouter", "other-model") {
		t.Error("OverrideModel should not change an unrelated model")
	}
}

// TestLearnedParamRegistrySanitize proves that Sanitize drops entries whose
// param is a stopword (legacy garbage like "this") while keeping valid
// entries, and reports how many were removed.
func TestLearnedParamRegistrySanitize(t *testing.T) {
	r := NewLearnedParamRegistry()
	// Force legacy garbage entries the way an older classifier would have.
	r.RecordInject("prov", "gemini-3.7-flash", "this", "This is required")
	r.RecordInject("prov", "other", "that", "That is required")
	// Valid entries that must survive.
	r.RecordStrip("openrouter", "glm-5.2", "logprobs", "Unsupported parameter: logprobs")
	r.RecordCapContext("tokenrouter", "qwen/qwen3.8-max-free", "262144", "exceeded")

	removed := r.Sanitize()
	if removed != 2 {
		t.Fatalf("Sanitize removed = %d, want 2", removed)
	}
	if r.Lookup("prov", "gemini-3.7-flash", "this") != nil {
		t.Error("garbage 'this' entry survived Sanitize")
	}
	if r.Lookup("prov", "other", "that") != nil {
		t.Error("garbage 'that' entry survived Sanitize")
	}
	if r.Lookup("openrouter", "glm-5.2", "logprobs") == nil {
		t.Error("valid strip entry was removed by Sanitize")
	}
	if r.Lookup("tokenrouter", "qwen/qwen3.8-max-free", "262144") == nil {
		t.Error("valid cap_context entry was removed by Sanitize")
	}
	// Idempotent: a second pass removes nothing.
	if removed := r.Sanitize(); removed != 0 {
		t.Errorf("second Sanitize removed = %d, want 0", removed)
	}
}

func TestLearnedParamRegistrySanitizeNilSafe(t *testing.T) {
	var r *LearnedParamRegistry
	if got := r.Sanitize(); got != 0 {
		t.Errorf("nil registry Sanitize = %d, want 0", got)
	}
}
