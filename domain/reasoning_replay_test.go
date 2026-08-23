package domain

import "testing"

func TestRequiresReasoningReplay(t *testing.T) {
	tests := []struct {
		name             string
		provider         string
		model            string
		interleavedField string
		want             bool
	}{
		// Catalog signal (preferred source of truth)
		{"catalog reasoning_content", "any", "any-model", "reasoning_content", true},
		{"catalog reasoning_content uppercase", "any", "any-model", "REASONING_CONTENT", true},
		{"catalog reasoning_details", "any", "any-model", "reasoning_details", false},
		{"catalog empty field", "any", "any-model", "", false},

		// Provider whitelist
		{"deepseek provider", "deepseek", "deepseek-chat", "", true},
		{"opencode-go provider", "opencode-go", "ox-alpha-free", "", true},
		{"siliconflow provider", "siliconflow", "some-model", "", true},
		{"unknown provider", "unknown", "some-model", "", false},

		// Model pattern fallback
		{"deepseek-r1 pattern", "openrouter", "deepseek-r1", "", true},
		{"deepseek-reasoner pattern", "openrouter", "deepseek-reasoner", "", true},
		{"deepseek-v4-flash pattern", "openrouter", "deepseek-v4-flash", "", true},
		{"kimi pattern", "openrouter", "kimi/k2.6", "", true},
		{"qwq pattern", "openrouter", "qwq-32b", "", true},
		{"qwen think pattern", "openrouter", "qwen3-think-30b", "", true},
		{"glm think pattern", "openrouter", "glm-4-think", "", true},
		{"mimo pattern", "openrouter", "mimo-v7", "", true},
		{"ox-alpha pattern", "openrouter", "stealth/ox-alpha", "", true},
		{"non-matching model", "openrouter", "gpt-5.5", "", false},

		// Catalog signal overrides provider whitelist
		{"catalog overrides provider", "deepseek", "some-model", "reasoning_details", false},

		// Empty inputs
		{"all empty", "", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RequiresReasoningReplay(tt.provider, tt.model, tt.interleavedField)
			if got != tt.want {
				t.Errorf("RequiresReasoningReplay(%q, %q, %q) = %v, want %v",
					tt.provider, tt.model, tt.interleavedField, got, tt.want)
			}
		})
	}
}

func TestIsReasoningPlaceholder(t *testing.T) {
	if !IsReasoningPlaceholder(ReasoningPlaceholder) {
		t.Errorf("IsReasoningPlaceholder(%q) = false, want true", ReasoningPlaceholder)
	}
	if !IsReasoningPlaceholder("  " + ReasoningPlaceholder + "  ") {
		t.Errorf("IsReasoningPlaceholder with surrounding whitespace = false, want true")
	}
	if IsReasoningPlaceholder("actual reasoning text") {
		t.Errorf("IsReasoningPlaceholder with real text = true, want false")
	}
	if IsReasoningPlaceholder("") {
		t.Errorf("IsReasoningPlaceholder(\"\") = true, want false")
	}
}
