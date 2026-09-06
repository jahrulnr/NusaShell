package resources

import (
	"strings"
	"testing"
)

func TestTemplatesEmbedUserAndSoul(t *testing.T) {
	user := Template("user")
	if !strings.Contains(user, "# Overview") {
		t.Fatalf("user.md template missing Overview heading:\n%s", user)
	}
	soul := Template("soul")
	if !strings.Contains(soul, "# About Agent") {
		t.Fatalf("soul.md template missing About Agent heading:\n%s", soul)
	}
}

func TestRenderLearnerUserPromptFillsPlaceholders(t *testing.T) {
	got := RenderLearnerUserPrompt("periodic", 0, "conv_source", "/tmp/conv.json", 2, 5)
	for _, want := range []string{
		"trigger_reason: periodic",
		"procedure_count: 0",
		"conversation_id: conv_source",
		"conversation_file: /tmp/conv.json",
		"message_range: [2,5)",
		"learn(",
		"SOURCE EVIDENCE",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "{{") {
		t.Fatalf("unreplaced placeholder:\n%s", got)
	}
}

func TestBuiltinSkillAccessor(t *testing.T) {
	body := BuiltinSkill("skill-creator")
	if body == "" || !strings.Contains(body, "Create an agent skill") {
		t.Fatalf("embedded skill-creator missing: %q", body[:min(len(body), 80)])
	}
	if BuiltinSkill("no-such-skill-xyz") != "" {
		t.Fatal("unknown skill must return empty")
	}
	if BuiltinSkill("../evil") != "" || BuiltinSkill("a/b") != "" {
		t.Fatal("path traversal inputs must be rejected")
	}
}

func TestRenderLearnerUserPromptRepeatedProcedureCount(t *testing.T) {
	got := RenderLearnerUserPrompt("repeated_procedure", 3, "c", "/p", 0, 0)
	if !strings.Contains(got, "procedure_count: 3") {
		t.Fatalf("missing procedure_count:\n%s", got)
	}
	if strings.Contains(got, "{{") {
		t.Fatalf("unreplaced placeholder:\n%s", got)
	}
}
