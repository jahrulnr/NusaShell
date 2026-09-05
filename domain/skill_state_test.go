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

func TestSkillEnsureStatusDefault(t *testing.T) {
	t.Run("learned defaults to experimental v1", func(t *testing.T) {
		skill := &Skill{ID: "s1", Origin: SkillOriginLearned}
		skill.EnsureStatusDefault()
		if skill.Status != SkillStatusExperimental {
			t.Fatalf("Status = %q", skill.Status)
		}
		if skill.Version != 1 || skill.ActiveVersion != 1 {
			t.Fatalf("version=%d active=%d", skill.Version, skill.ActiveVersion)
		}
	})
	t.Run("user curated defaults to trusted", func(t *testing.T) {
		skill := &Skill{ID: "s1", Origin: SkillOriginUser}
		skill.EnsureStatusDefault()
		if skill.Status != SkillStatusTrusted {
			t.Fatalf("Status = %q", skill.Status)
		}
	})
	t.Run("non-empty status is preserved", func(t *testing.T) {
		skill := &Skill{ID: "s1", Status: SkillStatusDeprecated, Version: 3, ActiveVersion: 2}
		skill.EnsureStatusDefault()
		if skill.Status != SkillStatusDeprecated {
			t.Fatalf("Status = %q", skill.Status)
		}
		if skill.ActiveVersion != 2 {
			t.Fatalf("ActiveVersion = %d", skill.ActiveVersion)
		}
	})
}

func TestSkillRoutable(t *testing.T) {
	if (&Skill{Status: SkillStatusExperimental}).Routable() {
		t.Fatal("experimental must not be default-routable")
	}
	if !(&Skill{Status: SkillStatusTrusted}).Routable() {
		t.Fatal("trusted must be routable")
	}
}

func TestSkillCanAgentMutate(t *testing.T) {
	if (&Skill{Origin: SkillOriginUser, Status: SkillStatusTrusted}).CanAgentMutate() {
		t.Fatal("agent must not mutate trusted user skills")
	}
	if !(&Skill{Origin: SkillOriginLearned, Status: SkillStatusExperimental}).CanAgentMutate() {
		t.Fatal("agent may version experimental learned skills")
	}
}
