package application

import (
	"strings"
	"testing"

	"nusashell/resources"
)

func TestBackgroundLearningPromptIsTheSinglePostConversationPrompt(t *testing.T) {
	prompt := resources.ReviewPrompt()
	if strings.TrimSpace(prompt) == "" {
		t.Fatal("background learning prompt must be non-empty")
	}
	for _, want := range []string{
		"conversation",
		"web_search",
		"user.md",
		"soul.md",
		"memory",
		"skill",
		"delete",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("background learning prompt missing %q", want)
		}
	}
	if resources.Prompt("improve") != "" {
		t.Fatal("improve.md must not remain as a second background prompt")
	}
}

func TestBackgroundLearningAllowedOperations(t *testing.T) {
	allowed := []struct {
		name string
		args string
	}{
		{"memory", `{"op":"replace","target":"user","content":"updated"}`},
		{"memory", `{"op":"replace","target":"agent","content":"updated"}`},
		{"memory", `{"op":"delete","id":"frag_1"}`},
		{"skill", `{"op":"save","id":"agent-skill","name":"agent-skill","content":"steps"}`},
		{"skill", `{"op":"delete","id":"agent-skill"}`},
		{"file_read", `{"path":"/tmp/evidence.md"}`},
		{"file_list", `{"path":"/tmp"}`},
		{"grep", `{"pattern":"needle","path":"/tmp"}`},
		{"web_search", `{"query":"current documentation"}`},
		{"web_fetch", `{"url":"https://example.com"}`},
		{"docs", `{"op":"list"}`},
		{"docs", `{"op":"search","query":"memory"}`},
		{"memory_project", `{"op":"query","kind":"decisions"}`},
	}
	for _, tc := range allowed {
		if !reviewAllowedOp(tc.name, []byte(tc.args)) {
			t.Errorf("background learning operation %s %s should be allowed", tc.name, tc.args)
		}
	}

	blocked := []struct {
		name string
		args string
	}{
		{"file_write", `{"path":"/tmp/evidence.md","content":"changed"}`},
		{"file_patch", `{"path":"/tmp/evidence.md","old_string":"a","new_string":"b"}`},
		{"file_delete", `{"path":"/tmp/evidence.md"}`},
		{"exec", `{"command":"rm -rf /tmp/evidence"}`},
		{"show", `{"op":"html","path":"/tmp/evidence.html"}`},
		{"automation", `{"op":"delete","workflow_id":"w1"}`},
		{"subagent", `{"prompt":"delegate"}`},
		{"memory_project", `{"op":"archive","id":"D-old"}`},
	}
	for _, tc := range blocked {
		if reviewAllowedOp(tc.name, []byte(tc.args)) {
			t.Errorf("background learning operation %s %s must be blocked", tc.name, tc.args)
		}
	}
}
