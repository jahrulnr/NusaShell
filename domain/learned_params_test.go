package domain

import (
	"strings"
	"testing"
)

func TestClassify400ErrorUnsupportedParam(t *testing.T) {
	cases := []struct {
		body   string
		action LearnedParamAction
		param  string
	}{
		{`{"error":{"message":"Unsupported parameter: logprobs"}}`, LearnedActionStrip, "logprobs"},
		{`Unsupported parameter(s): 'reasoning_budget'`, LearnedActionStrip, "reasoning_budget"},
		{`Unsupported parameter 'top_logprobs'`, LearnedActionStrip, "top_logprobs"},
		{`unsupported parameter: verbosity`, LearnedActionStrip, "verbosity"},
	}
	for _, c := range cases {
		action, param := Classify400Error(c.body)
		if action != c.action || param != c.param {
			t.Errorf("Classify400Error(%q) = (%q, %q), want (%q, %q)", c.body, action, param, c.action, c.param)
		}
	}
}

func TestClassify400ErrorRequiredField(t *testing.T) {
	cases := []struct {
		body   string
		action LearnedParamAction
		param  string
	}{
		{`reasoning_content must be passed back`, LearnedActionInject, "reasoning_content"},
		{`field 'reasoning_content' is required`, LearnedActionInject, "reasoning_content"},
		{`reasoning_content is a required field`, LearnedActionInject, "reasoning_content"},
		{`reasoning_content must be provided`, LearnedActionInject, "reasoning_content"},
	}
	for _, c := range cases {
		action, param := Classify400Error(c.body)
		if action != c.action || param != c.param {
			t.Errorf("Classify400Error(%q) = (%q, %q), want (%q, %q)", c.body, action, param, c.action, c.param)
		}
	}
}

func TestClassify400ErrorEmptyOrUnknown(t *testing.T) {
	cases := []string{"", "internal server error", `{"error":"rate limit"}`, "model not found"}
	for _, body := range cases {
		action, param := Classify400Error(body)
		if action != "" || param != "" {
			t.Errorf("Classify400Error(%q) = (%q, %q), want empty", body, action, param)
		}
	}
}

// TestClassify400ErrorTextOnly proves that "text-only" / "must be a text
// part" errors from text-only models (e.g. Qwen3.8) are classified as
// LearnedActionDisableModality with param "vision" — the most common
// non-text modality that triggers this error. The retry loop will set
// caps.Vision=false and strip images on retry.
func TestClassify400ErrorTextOnly(t *testing.T) {
	cases := []struct {
		body  string
		param string
	}{
		{
			`Qwen3.8 open checkpoint is text-only; messages[131].content[1] must be a text part`,
			"vision",
		},
		{
			`{"error":{"message":"model is text-only; messages[5].content[2] must be a text part"}}`,
			"vision",
		},
		{
			`this model is text-only and cannot process images`,
			"vision",
		},
	}
	for _, c := range cases {
		action, param := Classify400Error(c.body)
		if action != LearnedActionDisableModality {
			t.Errorf("Classify400Error(%q) action = %q, want %q", c.body, action, LearnedActionDisableModality)
		}
		if param != c.param {
			t.Errorf("Classify400Error(%q) param = %q, want %q", c.body, param, c.param)
		}
	}
}

// Required-field pattern must win over unsupported-param when both could
// match (a model can reject reasoning_content as unsupported on one
// endpoint while requiring it on another).
func TestClassify400ErrorRequiredWinsOverUnsupported(t *testing.T) {
	body := `reasoning_content is required: unsupported parameter reasoning_content`
	action, param := Classify400Error(body)
	if action != LearnedActionInject {
		t.Errorf("expected inject, got %q (body should match required-field first)", action)
	}
	if param != "reasoning_content" {
		t.Errorf("expected reasoning_content, got %q", param)
	}
}

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
