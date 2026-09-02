package application

import (
	"strings"
	"testing"

	"nusashell/domain"
)

func TestToolFactoryImproverFullToolsNoACProNoDelegate(t *testing.T) {
	f := &ToolFactory{
		Toolbox:     func() []ToolInfo { return factoryStubTools() },
		Dispatchers: FilterDispatcherToolInfos,
	}
	defs := f.Get(AgentImprover, "")
	names := namesOf(defs)
	for _, want := range []string{"review_transcript", "model_override", "memory", "exec", "file_read"} {
		if !hasTool(defs, want) {
			t.Fatalf("improver missing %q, got %v", want, names)
		}
	}
	for _, banned := range []string{"subagent", "delegate"} {
		if hasTool(defs, banned) {
			t.Fatalf("improver must not see %q (no permission stalls, no recursion), got %v", banned, names)
		}
	}
}

func TestImproverPromptInjectsPathAndWorkspace(t *testing.T) {
	conv := &domain.Conversation{ID: "c1", Workspace: "/work/ns"}
	app := &App{DataDir: "/data"}
	p := app.improverPrompt(conv)
	if p == "" {
		t.Fatal("improve prompt resource must load")
	}
	if !strings.Contains(p, "/data/conversations/c1.json") {
		t.Errorf("prompt missing conversation path, got:\n%s", p)
	}
	if !strings.Contains(p, "/work/ns") {
		t.Errorf("prompt missing workspace, got:\n%s", p)
	}
	if strings.Contains(p, "{{conversation_path}}") || strings.Contains(p, "{{workspace}}") {
		t.Errorf("prompt still contains placeholders:\n%s", p)
	}
}

func TestConversationJSONPathLayout(t *testing.T) {
	if got := conversationJSONPath("/data", "conv_123"); got != "/data/conversations/conv_123.json" {
		t.Errorf("path = %q", got)
	}
	if got := conversationJSONPath("", "conv_123"); got != "" {
		t.Errorf("empty DataDir must yield empty path, got %q", got)
	}
}
