package domain

import "testing"

func TestOpenAISupportsNativeCompaction(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"gpt-5.2", true},
		{"gpt-5.6-luna", true},
		{"gpt-5", true},
		{"gpt-4o", false},
		{"gpt-4.1", false},
		{"o3", false},
		{"claude-sonnet-4", false},
		{"", false},
		{"gpt-5-mini", true},
	}
	for _, tc := range cases {
		got := OpenAISupportsNativeCompaction(tc.model)
		if got != tc.want {
			t.Errorf("OpenAISupportsNativeCompaction(%q) = %v, want %v", tc.model, got, tc.want)
		}
	}
}
