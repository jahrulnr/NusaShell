package repoagent

import (
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

func TestAutoStartAndDefaultsPersist(t *testing.T) {
	repo := New(t.TempDir(), nil)
	a, err := aggregate.NewAgent("claude", "Claude", "claude-agent-acp", nil, enum.ApprovalPolicyAsk)
	if err != nil {
		t.Fatal(err)
	}
	a.SetAutoStart(true)
	if err := a.SetDefaults(valueobject.ConfigSelections{{ID: "mode", Value: "plan"}, {ID: "thought_level", Value: "high"}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(t.Context(), &a); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Inspect(t.Context(), "claude")
	if err != nil {
		t.Fatal(err)
	}
	mode, _ := got.Defaults().Value("mode")
	lvl, _ := got.Defaults().Value("thought_level")
	if !got.AutoStart() || mode != "plan" || lvl != "high" {
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
