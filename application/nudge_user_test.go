package application

import "testing"

func TestHasUserMessage(t *testing.T) {
	cases := []struct {
		name     string
		msgs     []ChatMessage
		expected bool
	}{
		{"empty", nil, false},
		{"only assistant", []ChatMessage{{Role: "assistant", Content: "hi"}}, false},
		{"only system", []ChatMessage{{Role: "system", Content: "sys"}}, false},
		{"only tool", []ChatMessage{{Role: "tool", ToolResult: &ToolResult{}}}, false},
		{"has user", []ChatMessage{{Role: "user", Content: "hello"}}, true},
		{"user after assistant", []ChatMessage{
			{Role: "assistant", Content: "hi"},
			{Role: "user", Content: "hello"},
		}, true},
		{"user empty content", []ChatMessage{{Role: "user", Content: ""}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasUserMessage(c.msgs); got != c.expected {
				t.Errorf("hasUserMessage(%s) = %v, want %v", c.name, got, c.expected)
			}
		})
	}
}
