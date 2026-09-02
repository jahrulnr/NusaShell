package application

import (
	"path/filepath"
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
	dataDir := t.TempDir()
	workspace := t.TempDir()
	conv := &domain.Conversation{ID: "c1", Workspace: workspace}
	app := &App{DataDir: dataDir}
	p := app.improverPrompt(conv)
	if p == "" {
		t.Fatal("improve prompt resource must load")
	}
	expectedConversationPath := filepath.Join(dataDir, "conversations", "c1.json")
	if !strings.Contains(p, expectedConversationPath) {
		t.Errorf("prompt missing conversation path, got:\n%s", p)
	}
	if !strings.Contains(p, workspace) {
		t.Errorf("prompt missing workspace, got:\n%s", p)
	}
	if strings.Contains(p, "{{conversation_path}}") || strings.Contains(p, "{{workspace}}") {
		t.Errorf("prompt still contains placeholders:\n%s", p)
	}
}

func TestConversationJSONPathLayout(t *testing.T) {
	dataDir := t.TempDir()
	expected := filepath.Join(dataDir, "conversations", "conv_123.json")
	if got := conversationJSONPath(dataDir, "conv_123"); got != expected {
		t.Errorf("path = %q", got)
	}
	if got := conversationJSONPath("", "conv_123"); got != "" {
		t.Errorf("empty DataDir must yield empty path, got %q", got)
	}
}
