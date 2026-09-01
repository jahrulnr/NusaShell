package automation

import (
	"strings"
	"testing"

	"nusashell/domain"
	"nusashell/resources"
)

func TestBuiltinAutomationAuthoringTemplatesParse(t *testing.T) {
	t.Parallel()
	templates := map[string]struct {
		family domain.TriggerFamily
		event  string
	}{
		"telegram-auto-reply.yaml": {family: domain.FamilyWhen, event: "telegram.message"},
		"alarm-once.yaml":          {family: domain.FamilyOnce},
		"reminder-every.yaml":      {family: domain.FamilyEvery},
	}

	for name, want := range templates {
		name, want := name, want
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := "agent/skills/automation-authoring/templates/" + name
			raw, err := resources.BuiltinSkillsFS.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			workflow, err := ParseYAML(raw)
			if err != nil {
				t.Fatalf("parse %s: %v", name, err)
			}
			if result := domain.ValidateSyntax(workflow); result.Verdict() != "VALID" {
				t.Fatalf("validate %s: %s (%+v)", name, result.Verdict(), result.Issues)
			}
			if len(workflow.Triggers) != 1 {
				t.Fatalf("triggers = %d, want one", len(workflow.Triggers))
			}
			trigger := workflow.Triggers[0]
			if trigger.Family != want.family {
				t.Fatalf("trigger family = %q, want %q", trigger.Family, want.family)
			}
			if want.event != "" && trigger.Event != want.event {
				t.Fatalf("trigger event = %q, want %q", trigger.Event, want.event)
			}
			if len(workflow.Jobs) == 0 {
				t.Fatal("template has no jobs")
			}
		})
	}
}

func TestTelegramTemplateIncludesEventIdentity(t *testing.T) {
	raw, err := resources.BuiltinSkillsFS.ReadFile("agent/skills/automation-authoring/templates/telegram-auto-reply.yaml")
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)
	for _, variable := range []string{"${event.chat_id}", "${event.message_id}", "${event.text}", "${event.subject}"} {
		if !strings.Contains(content, variable) {
			t.Errorf("telegram template missing %s", variable)
		}
	}
	if strings.Contains(content, "unread_count") {
		t.Error("telegram template must not depend on unread_count")
	}
}
