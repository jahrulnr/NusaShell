package repoagent

import (
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

// A catalog round-trips: options, verbatim model ids, caps, and the probe
// timestamp all come back as written.
func TestCatalogRoundTrip(t *testing.T) {
	repo := New(t.TempDir(), nil)
	cat := drepo.AgentCatalog{
		Options: []dservice.AgentOption{
			{ID: "effort", Name: "Effort", Type: "select", Current: "high",
				Choices: []dservice.OptionChoice{{Value: "low"}, {Value: "high"}}},
			{ID: "strict", Name: "Strict", Type: "boolean", Current: "false"},
		},
		Models:       []dservice.AgentModel{{ID: "gpt-6-luna[max]", Name: "6 Luna", Current: true}},
		CurrentModel: "gpt-6-luna[max]",
		Caps:         []byte(`{"loadSession":true}`),
		ProbedAt:     time.Now().Truncate(time.Second),
	}
	if err := repo.SaveCatalog(t.Context(), "codex", cat); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Catalog(t.Context(), "codex")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Options) != 2 || got.Options[0].ID != "effort" || got.Options[1].Current != "false" {
		t.Fatalf("options = %+v", got.Options)
	}
	if len(got.Models) != 1 || got.Models[0].ID != "gpt-6-luna[max]" || got.CurrentModel != "gpt-6-luna[max]" {
		t.Fatalf("composite model ids not kept verbatim: %+v", got)
	}
	if !strings.Contains(string(got.Caps), `"loadSession": true`) || got.ProbedAt.IsZero() {
		t.Fatalf("caps/probedAt = %+v", got)
	}
}

// No catalog is not an error: an agent never seen reads as the zero value.
func TestCatalogMissingReadsAsZero(t *testing.T) {
	repo := New(t.TempDir(), nil)
	cat, err := repo.Catalog(t.Context(), "ghost")
	if err != nil || cat.Options != nil || !cat.ProbedAt.IsZero() {
		t.Fatalf("Catalog(absent) = %+v, %v — want zero, nil", cat, err)
	}
}

// Deleting the agent takes its catalog with it.
func TestCatalogRemovedWithTheAgent(t *testing.T) {
	repo := New(t.TempDir(), nil)
	a, err := aggregate.NewAgent("codex", "Codex", "codex-acp", nil, enum.ApprovalPolicyAsk)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(t.Context(), &a); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveCatalog(t.Context(), "codex", drepo.AgentCatalog{ProbedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Delete(t.Context(), "codex"); err != nil {
		t.Fatal(err)
	}
	cat, err := repo.Catalog(t.Context(), "codex")
	if err != nil || cat.Options != nil {
		t.Fatalf("catalog survived the agent's delete: %+v, %v", cat, err)
	}
}
