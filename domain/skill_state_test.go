package domain

import (
	"testing"
	"time"
)

func TestSkillTouch(t *testing.T) {
	skill := &Skill{ID: "s1", Name: "test"}
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	skill.Touch(now)
	if !skill.UpdatedAt.Equal(now) {
		t.Fatalf("UpdatedAt = %v, want %v", skill.UpdatedAt, now)
	}
}

func TestSkillSetOwner(t *testing.T) {
	t.Run("sets owned by and plugin dir", func(t *testing.T) {
		skill := &Skill{ID: "s1", Name: "test"}
		skill.SetOwner("plugin:myplugin", "/plugins/myplugin/skills")
		if skill.OwnedBy != "plugin:myplugin" {
			t.Fatalf("OwnedBy = %q, want %q", skill.OwnedBy, "plugin:myplugin")
		}
		if skill.PluginDir != "/plugins/myplugin/skills" {
			t.Fatalf("PluginDir = %q, want %q", skill.PluginDir, "/plugins/myplugin/skills")
		}
	})
}

func TestSkillEnsureStateDefault(t *testing.T) {
	t.Run("empty state becomes active", func(t *testing.T) {
		skill := &Skill{ID: "s1", State: ""}
		skill.EnsureStateDefault()
		if skill.State != SkillStateActive {
			t.Fatalf("State = %q, want %q", skill.State, SkillStateActive)
		}
	})
	t.Run("non-empty state is preserved", func(t *testing.T) {
		skill := &Skill{ID: "s1", State: SkillStateArchived}
		skill.EnsureStateDefault()
		if skill.State != SkillStateArchived {
			t.Fatalf("State = %q, want %q", skill.State, SkillStateArchived)
		}
	})
}
