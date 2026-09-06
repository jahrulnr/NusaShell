package application

import (
	"strings"
	"testing"

	"nusashell/resources"
)

func TestBackgroundLearningPromptIsUnifiedLearner(t *testing.T) {
	prompt := resources.LearnerPrompt()
	if strings.TrimSpace(prompt) == "" {
		t.Fatal("learner prompt must be non-empty")
	}
	assertBackgroundPromptRoleFocused(t, "learner", prompt)
	for _, doc := range []string{"user.md", "soul.md"} {
		if !strings.Contains(prompt, doc) {
			t.Errorf("learner must mention %q", doc)
		}
	}
	if !strings.Contains(prompt, "file_patch") || !strings.Contains(prompt, "file_write") {
		t.Error("learner must allow profile-document writes via file_*")
	}
	if strings.Contains(prompt, "Never write memory/user.md") || strings.Contains(prompt, "Do not read or write user.md") {
		t.Error("learner must not forbid profile-document file writes")
	}
	if !strings.Contains(prompt, "Primary Memory Writing Rules") {
		t.Error("learner must include the Primary Memory Writing Rules")
	}
	if !strings.Contains(prompt, "Never promote") && !strings.Contains(strings.ToLower(prompt), "never promote a skill to trusted") {
		t.Error("learner must forbid trusted promotion")
	}
	if !strings.Contains(prompt, "explicit_teaching") || !strings.Contains(prompt, "repeated_procedure") {
		t.Error("learner must document the five language-agnostic trigger categories")
	}
	if !strings.Contains(prompt, "learn(") && !strings.Contains(prompt, "`learn`") {
		t.Error("learner must tell the model to submit results via learn()")
	}
	if !strings.Contains(prompt, "evidence") {
		t.Error("learner must require evidence for every entry")
	}
	if !strings.Contains(prompt, "skill-authoring reference") {
		t.Error("learner must reference the attached skill-authoring reference for Stages 2-3")
	}
	if strings.Contains(prompt, "Return ONLY that JSON object") {
		t.Error("learner must not treat assistant text as the JSON contract")
	}
	userPrompt := resources.UserPrompt("learner")
	if !strings.Contains(userPrompt, "learn") {
		t.Error("learner user prompt must ask for learn(), not a JSON-only reply")
	}
	if resources.Prompt("learn") != "" {
		t.Fatal("learn.md must not remain as a second unified review prompt")
	}
	if resources.Prompt("improve") != "" {
		t.Fatal("improve.md must not remain as a second background prompt")
	}
	if resources.Prompt("memory-consolidator") != "" || resources.Prompt("skill-evolver") != "" || resources.Prompt("skill-evaluator") != "" {
		t.Fatal("legacy consolidator/evolver/evaluator prompts must be removed")
	}
}

// assertBackgroundPromptRoleFocused guards the prompt-construction contract
// from resources/AGENTS.md: the learner prompt describes role, objectives,
// constraints, and output requirements — never the orchestration around it.
func assertBackgroundPromptRoleFocused(t *testing.T, name, prompt string) {
	t.Helper()
	normalized := strings.Join(strings.Fields(strings.ToLower(prompt)), " ")
	for _, leaked := range []string{
		"full conversation toolbox",
		"direct tool side effects are enabled",
		"exploratory background mode",
		"security restrictions",
		"orchestrator",
		"hydration",
		"checkpoint",
		"spawned headlessly",
	} {
		if strings.Contains(normalized, leaked) {
			t.Errorf("%s prompt must not expose orchestration detail %q", name, leaked)
		}
	}
}

func TestSystemPromptIncludesMemoryWritingRules(t *testing.T) {
	prompt := resources.SystemPrompt()
	if !strings.Contains(prompt, "Primary Memory Writing Rules") {
		t.Fatal("system prompt must include Primary Memory Writing Rules")
	}
	if !strings.Contains(prompt, "file_patch") {
		t.Fatal("system prompt must tell the agent to write user.md via file_patch")
	}
	if strings.Contains(prompt, "Never edit `memory/user.md`") || strings.Contains(prompt, "You cannot write durable memory") {
		t.Fatal("system prompt must not forbid profile-document writes")
	}
	if !strings.Contains(prompt, "{dataDir}/memory/user.md") {
		t.Fatal("system prompt must name the absolute user.md path pattern")
	}
}
