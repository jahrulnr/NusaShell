package domain

import "testing"

func TestOpenAISupportsServerCompaction(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		// gpt-5.x family — all supported, all >= 400k context
		{"gpt-5", true},
		{"gpt-5-mini", true},
		{"gpt-5-nano", true},
		{"gpt-5-codex", true},
		{"gpt-5-pro", true},
		{"gpt-5.1", true},
		{"gpt-5.1-codex", true},
		{"gpt-5.1-codex-max", true},
		{"gpt-5.2", true},
		{"gpt-5.2-pro", true},
		{"gpt-5.2-codex", true},
		{"gpt-5.3-codex", true},
		{"gpt-5.4", true},
		{"gpt-5.4-mini", true},
		{"gpt-5.4-nano", true},
		{"gpt-5.5", true},
		{"gpt-5.6-luna", true},
		{"gpt-5.6-sol", true},
		{"gpt-5.6-terra", true},
		// gpt-4.1 family — supported, 1M context
		{"gpt-4.1", true},
		{"gpt-4.1-mini", true},
		{"gpt-4.1-nano", true},
		// o-series — supported, 200k context (meets floor)
		{"o1", true},
		{"o3", true},
		{"o3-mini", true},
		{"o4-mini", true},
		{"o1-pro", true},
		{"o3-pro", true},
		{"codex-mini-latest", true},
		// gpt-4o family — supported by API but context 128k < 200k floor → skip
		{"gpt-4o", false},
		{"gpt-4o-mini", false},
		{"chatgpt-4o-latest", false},
		// chat-latest variants — 128k context < 200k floor → skip
		{"gpt-5-chat-latest", false},
		{"gpt-5.2-chat-latest", false},
		// NOT supported (not in table at all)
		{"gpt-4-turbo", false},
		{"gpt-4", false},
		{"gpt-4-32k", false},
		{"gpt-3.5-turbo", false},
		{"gpt-4-vision-preview", false},
		{"computer-use-preview", false},
		{"claude-sonnet-4", false},
		{"", false},
		// Normalization: openai/ prefix stripped, case-insensitive
		{"openai/gpt-5.2", true},
		{"GPT-5.2", true},
		{"OpenAI/gpt-5.6-luna", true},
	}
	for _, tc := range cases {
		got := OpenAISupportsServerCompaction(tc.model)
		if got != tc.want {
			t.Errorf("OpenAISupportsServerCompaction(%q) = %v, want %v", tc.model, got, tc.want)
		}
	}
}

func TestOpenAIServerCompactionContextWindow(t *testing.T) {
	cases := []struct {
		model string
		want  int
	}{
		{"gpt-5.2", 400000},
		{"gpt-5.6-luna", 1047576},
		{"gpt-5", 400000},
		{"gpt-5-mini", 400000},
		{"gpt-5.4", 1047576},
		{"gpt-5.5", 1047576},
		{"gpt-5.6-sol", 1047576},
		{"gpt-4.1", 1047576},
		{"gpt-4.1-mini", 1047576},
		{"o3", 200000},
		{"o1", 200000},
		{"o4-mini", 200000},
		{"codex-mini-latest", 200000},
		// Below 200k floor — in table (returns window) but not eligible
		{"gpt-4o", 128000},
		{"gpt-4o-mini", 128000},
		// Unknown models return 0
		{"gpt-4-turbo", 0},
		{"claude-sonnet-4", 0},
		{"", 0},
		// Normalization
		{"openai/gpt-5.2", 400000},
		{"GPT-5.6-LUNA", 1047576},
	}
	for _, tc := range cases {
		got := OpenAIServerCompactionContextWindow(tc.model)
		if got != tc.want {
			t.Errorf("OpenAIServerCompactionContextWindow(%q) = %d, want %d", tc.model, got, tc.want)
		}
	}
}
