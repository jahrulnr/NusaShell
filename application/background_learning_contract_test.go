package application

import (
	"strings"
	"testing"

	"nusashell/resources"
)

func TestBackgroundLearningPromptsAreSeparateAgents(t *testing.T) {
	consolidator := resources.ConsolidatorPrompt()
	evolver := resources.SkillEvolverPrompt()
	evaluator := resources.SkillEvaluatorPrompt()
	if strings.TrimSpace(consolidator) == "" || strings.TrimSpace(evolver) == "" || strings.TrimSpace(evaluator) == "" {
		t.Fatal("consolidator, evolver, and evaluator prompts must be non-empty")
	}
	for _, banned := range []string{"user.md", "soul.md"} {
		if !strings.Contains(consolidator, banned) {
			t.Errorf("consolidator must mention %q as forbidden writes", banned)
		}
	}
	if !strings.Contains(consolidator, "Never write") {
		t.Error("consolidator must forbid writing the human profile documents")
	}
	if strings.Contains(evolver, "skill.promote") && !strings.Contains(evolver, "Do not emit skill.promote") {
		t.Error("evolver must forbid promote")
	}
	if !strings.Contains(evaluator, "Never trusted") && !strings.Contains(evaluator, "never promote") {
		t.Error("evaluator must forbid trusted promotion")
	}
	if resources.Prompt("learn") != "" {
		t.Fatal("learn.md must not remain as a unified review prompt")
	}
	if resources.Prompt("improve") != "" {
		t.Fatal("improve.md must not remain as a second background prompt")
	}
}
