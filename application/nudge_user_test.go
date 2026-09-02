package application

import (
	"testing"

	"nusashell/domain"
)

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

func TestNeedsUserMessageAtEnd(t *testing.T) {
	cases := []struct {
		name string
		msgs []ChatMessage
		want bool
	}{
		{name: "empty", want: true},
		{name: "system only", msgs: []ChatMessage{{Role: "system"}}, want: true},
		{name: "user last", msgs: []ChatMessage{{Role: "user"}}, want: false},
		{name: "assistant last", msgs: []ChatMessage{{Role: "user"}, {Role: "assistant", Content: "previous answer"}}, want: true},
		{name: "tool result last", msgs: []ChatMessage{{Role: "user"}, {Role: "assistant", ToolCalls: []domain.ToolCall{{ID: "call_1"}}}, {Role: "tool", ToolResult: &ToolResult{ToolCallID: "call_1"}}}, want: false},
		{name: "tool without user", msgs: []ChatMessage{{Role: "tool", ToolResult: &ToolResult{}}}, want: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := needsUserMessageAtEnd(c.msgs); got != c.want {
				t.Fatalf("needsUserMessageAtEnd(%s) = %v, want %v", c.name, got, c.want)
			}
		})
	}
}
