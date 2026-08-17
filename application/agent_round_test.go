package application

import "testing"

func TestEstimateRequestTokensIncludesSystemAndTools(t *testing.T) {
	system := "system prompt content"
	messages := []ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	}
	tools := []ToolDef{{Name: "exec", Description: "run a command"}}
	got := estimateRequestTokens(system, messages, tools)
	if got <= int64(0) {
		t.Fatalf("expected positive estimate, got %d", got)
	}
	if got < int64(10) {
		t.Fatalf("estimate too small for payload: %d", got)
	}
}
