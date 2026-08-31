package domain

import "testing"

func TestPromptCacheKeyConstants(t *testing.T) {
	if PromptCacheKeyLength != 32 {
		t.Errorf("PromptCacheKeyLength = %d, want 32", PromptCacheKeyLength)
	}
	if PromptCacheConversationPrefix != "nusashell_cv_" {
		t.Errorf("PromptCacheConversationPrefix = %q, want %q", PromptCacheConversationPrefix, "nusashell_cv_")
	}
	if PromptCacheBackgroundPrefix != "nusashell_bg_" {
		t.Errorf("PromptCacheBackgroundPrefix = %q, want %q", PromptCacheBackgroundPrefix, "nusashell_bg_")
	}
}
