package application

import (
	"strings"
	"testing"

	"nusashell/domain"
)

func TestHydrationSkillCreatorSlot(t *testing.T) {
	b := NewHydrationBuilder(HydrationSource{
		SkillCreatorPath:    "/data/skills/skill-creator/SKILL.md",
		SkillCreatorContent: "# Create an agent skill\n\nAuthoring reference body.",
	})
	res := b.Build()
	found := false
	for _, m := range res.Messages {
		for _, tc := range m.ToolCalls {
			if tc.Name == "file_read" && strings.Contains(tc.Args, "skill-creator/SKILL.md") {
				found = true
			}
		}
		if m.ToolResult != nil && strings.Contains(m.ToolResult.Content, "Authoring reference body.") {
			found = found && true
		}
	}
	if !found {
		t.Fatalf("hydration must attach the skill-creator file_read slot: %+v", res.Messages)
	}
}

func TestHydrationSkillCreatorSlotHiddenWhenEmpty(t *testing.T) {
	b := NewHydrationBuilder(HydrationSource{
		SkillCreatorPath:    "/data/skills/skill-creator/SKILL.md",
		SkillCreatorContent: "   ",
	})
	res := b.Build()
	for _, m := range res.Messages {
		for _, tc := range m.ToolCalls {
			if strings.Contains(tc.Args, "skill-creator") {
				t.Fatalf("empty skill-creator content must hide the slot: %+v", tc)
			}
		}
	}
}

func TestBuildHydrationInjectsSkillCreatorForBackgroundLearner(t *testing.T) {
	skills := &fakeSkillStore{items: map[string]*domain.Skill{
		"skill-creator": {
			ID: "skill-creator", Name: "skill-creator", Origin: domain.SkillOriginBuiltin,
			Content: "# Create an agent skill\n\nHydration copy.",
		},
	}}
	app := &App{DataDir: "/home/u/.config/nusashell", Skills: skills}

	bg := &domain.Conversation{ID: "c_bg", Type: domain.ConversationTypeBackground}
	msgs := app.buildHydration(bg)
	blob := messagesBlob(msgs)
	if !strings.Contains(blob, "skill-creator/SKILL.md") || !strings.Contains(blob, "Hydration copy.") {
		t.Fatalf("background learner hydration must carry the skill-creator file_read slot:\n%s", blob)
	}

	// Interactive rooms and pipeline turns must not carry the slot.
	chat := &domain.Conversation{ID: "c_chat"}
	if blob := messagesBlob(app.buildHydration(chat)); strings.Contains(blob, "skill-creator") {
		t.Fatalf("interactive conversation must not get the learner slot:\n%s", blob)
	}
	pipe := &domain.Conversation{ID: "c_pipe", Type: domain.ConversationTypeAutomation}
	if blob := messagesBlob(app.buildHydration(pipe)); strings.Contains(blob, "skill-creator") {
		t.Fatalf("automation pipeline must not get the learner slot:\n%s", blob)
	}
}

func messagesBlob(msgs []ChatMessage) string {
	var sb strings.Builder
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			sb.WriteString(tc.Name)
			sb.WriteString(" ")
			sb.WriteString(tc.Args)
			sb.WriteString(" ")
		}
		if m.ToolResult != nil {
			sb.WriteString(m.ToolResult.Content)
			sb.WriteString(" ")
		}
	}
	return sb.String()
}
