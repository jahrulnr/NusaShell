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

func TestInteractivePromptKeepsPlanningInstructionsOperational(t *testing.T) {
	prompt := SystemPrompt()
	for _, stale := range []string{
		"referenced_image_paths",
		"share updates in the `todo` tool",
		"end your turn by sending a final message to the `todo` tool",
	} {
		if strings.Contains(prompt, stale) {
			t.Errorf("interactive prompt contains stale instruction %q", stale)
		}
	}
	for _, want := range []string{
		"Use `todo` to track multi-step work",
		"Your final assistant message should state the outcome",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("interactive prompt missing operational instruction %q", want)
		}
	}
}

func TestAutomationPromptMatchesAvailableWorkflowDispatchers(t *testing.T) {
	prompt := Prompt("automation-agent")
	if strings.Contains(prompt, "Modify automation workflows or schedules from within a step.") {
		t.Error("automation prompt claims workflow mutation is unavailable although the dispatcher is exposed")
	}
	for _, want := range []string{
		"`automation` and `automation_schedule` dispatchers may also be available",
		"Only perform workflow or schedule maintenance when the step explicitly requests it",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("automation prompt missing capability boundary %q", want)
		}
	}
}

func TestContinuationPromptDoesNotRequireSyntheticTodoTool(t *testing.T) {
	for _, prompt := range []struct {
		name string
		body string
	}{
		{name: "system", body: SystemPrompt()},
		{name: "announcement", body: Prompt("continue")},
		{name: "docs", body: string(mustReadPromptDoc(t))},
	} {
		if strings.Contains(prompt.body, "fresh `todo_list` result") {
			t.Errorf("%s still requires the synthetic todo_list tool", prompt.name)
		}
	}
}

func mustReadPromptDoc(t *testing.T) []byte {
	t.Helper()
	data, err := DocsFS.ReadFile("agent/docs/tools.md")
	if err != nil {
		t.Fatalf("read tools documentation: %v", err)
	}
	return data
}

func TestLearnerPromptMatchesTypedResultAndProfileContracts(t *testing.T) {
	prompt := LearnerPrompt()
	for _, stale := range []string{
		`"supersedes": "memory_id or null"`,
		"## Structure (skip empty sections) # Overview",
	} {
		if strings.Contains(prompt, stale) {
			t.Errorf("learner prompt contains stale contract text %q", stale)
		}
	}
	for _, want := range []string{
		"For `no_op`, omit `entry`",
		"For `write` or `update`, omit `supersedes`",
		"visibility is not permission to call it during Stage 1 or Stage 2",
		"## Structure (skip empty sections)\n\nUse these headings in order",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("learner prompt missing contract guidance %q", want)
		}
	}
}

func TestLearnerUserPromptUsesAvailableEvidenceAndSchemaFields(t *testing.T) {
	prompt := UserPrompt("learner")
	for _, stale := range []string{
		"justification/notes field",
	} {
		if strings.Contains(prompt, stale) {
			t.Errorf("learner user prompt names unavailable schema field %q", stale)
		}
	}
	for _, want := range []string{
		"file_read",
		"entry.evidence",
		"do not invent fields",
		"narrow the wording or exclude it",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("learner user prompt missing evidence/schema guidance %q", want)
		}
	}
}

func TestCompactionPromptsNameSummaryPayloadField(t *testing.T) {
	for _, prompt := range []struct {
		name string
		body string
	}{
		{name: "system", body: Prompt("compaction")},
		{name: "user", body: UserPrompt("compaction")},
	} {
		if !strings.Contains(prompt.body, "summary") || !strings.Contains(prompt.body, "`text` field") {
			t.Errorf("%s compaction prompt must name summary text payload: %q", prompt.name, prompt.body)
		}
	}
}

func TestMediaPromptsHonorDefaultOutputLimits(t *testing.T) {
	if !strings.Contains(ImageVisionSystemPrompt(), "under 400 words") {
		t.Error("image vision prompt must align with the default 400-word user prompt")
	}
	if !strings.Contains(VideoVisionSystemPrompt(), "under 600 words") {
		t.Error("video vision prompt must align with the default 600-word user prompt")
	}
	if !strings.Contains(AudioVisionSystemPrompt(), "If the user asks an explicit question") {
		t.Error("audio vision prompt must define the custom-question behavior")
	}
}
