package repoagent

import (
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
)

func TestAutoStartAndDefaultsPersist(t *testing.T) {
	repo := New(t.TempDir(), nil)
	a, err := aggregate.NewAgent("claude", "Claude", "claude-agent-acp", nil, enum.ApprovalPolicyAsk)
	if err != nil {
		t.Fatal(err)
	}
	a.SetAutoStart(true)
	if err := a.SetDefaults(map[string]string{"mode": "plan", "thought_level": "high"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(t.Context(), &a); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Inspect(t.Context(), "claude")
	if err != nil {
		t.Fatal(err)
	}
	if !got.AutoStart() || got.Defaults()["mode"] != "plan" || got.Defaults()["thought_level"] != "high" {
		t.Fatalf("loaded autoStart=%v defaults=%v", got.AutoStart(), got.Defaults())
	}

	// Turning them off leaves nothing behind.
	a.SetAutoStart(false)
	if err := a.SetDefaults(nil); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(t.Context(), &a); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.Inspect(t.Context(), "claude")
	if got.AutoStart() || len(got.Defaults()) != 0 {
		t.Fatalf("loaded autoStart=%v defaults=%v", got.AutoStart(), got.Defaults())
	}
}
