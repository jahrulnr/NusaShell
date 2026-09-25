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

func TestRenderLearnerUserPromptUsesPeriodicSourceRange(t *testing.T) {
	got := RenderLearnerUserPrompt("c", "/p", 0, 2)
	if !strings.Contains(got, "periodic review") {
		t.Fatalf("missing periodic review instruction:\n%s", got)
	}
	if !strings.Contains(got, "conversation_file: /p") || !strings.Contains(got, "message_range: [0,2]") {
		t.Fatalf("missing source range:\n%s", got)
	}
	if !strings.Contains(got, "JSON Lines") || !strings.Contains(got, "line N+2") {
		t.Fatalf("learner prompt must explain the JSONL transcript layout:\n%s", got)
	}
	if strings.Contains(got, "trigger_reason") || strings.Contains(got, "procedure_count") || strings.Contains(got, "stage") {
		t.Fatalf("periodic prompt still exposes staged trigger metadata:\n%s", got)
	}
	if strings.Contains(got, "{{") {
		t.Fatalf("unreplaced placeholder:\n%s", got)
	}
}

func TestRenderLearnerUserPromptIncludesSourceProjectLabel(t *testing.T) {
	got := RenderLearnerUserPromptForProject("c", "/p", 0, 2, "NusaShell")
	if !strings.Contains(got, "project_label: NusaShell") {
		t.Fatalf("missing source project label:\n%s", got)
	}
	if strings.Contains(got, "{{") {
		t.Fatalf("unreplaced placeholder:\n%s", got)
	}
}

func TestLearnerTaskPromptStaysWithinMemoryReview(t *testing.T) {
	user := UserPrompt("learner")
	for _, stale := range []string{"manage skills", "skill-creator", "public skills", "research relevant"} {
		if strings.Contains(strings.ToLower(user), stale) {
			t.Errorf("learner task prompt expands beyond memory review with %q", stale)
		}
	}
	if !strings.Contains(user, "source conversation") || !strings.Contains(user, "learn()") {
		t.Fatal("learner task must keep its evidence source and typed result")
	}
}

func TestProfilePromptsKeepAgentVoiceUserDirected(t *testing.T) {
	for _, prompt := range []string{LearnerPrompt(), SystemPrompt()} {
		if !strings.Contains(prompt, "user-chosen voice") {
			t.Error("profile guidance must preserve the user's chosen agent voice")
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
		"headless runner validates the final assistant content",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("automation prompt missing capability boundary %q", want)
		}
	}
	if strings.Contains(prompt, "Structured output validation is a future enhancement") {
		t.Error("automation prompt still describes output_schema validation as future work")
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
		"Choose the narrowest supported memory scope",
		"copy the exact `project_label`",
		"If `project_label` is empty, do not use project scope",
		"The `skill` dispatcher is read-only for this agent",
		"## Structure (skip empty sections)\n\nUse these headings in order",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("learner prompt missing contract guidance %q", want)
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
