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

// A catalog document shares the directory List globs — it must not be
// read back as an agent record (production: one stray catalog failed
// every List and blanked the Agents page). Written after the agent's
// save, the way a live session leaves it.
func TestListSkipsCatalogDocuments(t *testing.T) {
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
	list, err := repo.List(t.Context())
	if err != nil || len(list) != 1 || list[0].ID() != "codex" {
		t.Fatalf("List = %+v, %v — a catalog file must not be an agent record", list, err)
	}
}

// A catalog with no agent record — orphaned by a hand-deleted record or
// an interrupted delete — still cannot fail the list.
func TestListSkipsAnOrphanCatalog(t *testing.T) {
	repo := New(t.TempDir(), nil)
	if err := repo.SaveCatalog(t.Context(), "ghost", drepo.AgentCatalog{ProbedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	list, err := repo.List(t.Context())
	if err != nil || len(list) != 0 {
		t.Fatalf("List = %+v, %v — an orphan catalog must not fail it", list, err)
	}
}

// An id ending in the catalog suffix writes <id>.json — the very file
// List skips — so the agent could never appear. Refuse it at the boundary.
func TestCatalogSuffixIDsAreRejected(t *testing.T) {
	repo := New(t.TempDir(), nil)
	a, err := aggregate.NewAgent("x.catalog", "X", "x-acp", nil, enum.ApprovalPolicyAsk)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(t.Context(), &a); err == nil {
		t.Fatal("an id ending in the catalog suffix was stored")
	}
	if err := repo.SaveCatalog(t.Context(), "x.catalog", drepo.AgentCatalog{}); err == nil {
		t.Fatal("a catalog for a suffix id was stored")
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

// A conversation's command catalog round-trips as one replacement: the second
// report is the whole list, including an empty list, never a merge.
func TestCommandCatalogRoundTripAndReplacement(t *testing.T) {
	repo := New(t.TempDir(), nil)
	cat := drepo.AgentCommandCatalog{
		AgentID: "codex",
		Commands: []dservice.AgentCommand{
			{Name: "compact", Description: "Compact", Hint: "keep"},
			{Name: "review", Description: "Review"},
		},
		ReportedAt: time.Now().Truncate(time.Second),
	}
	if err := repo.SaveCommandCatalog(t.Context(), "c_room", cat); err != nil {
		t.Fatal(err)
	}
	got, err := repo.CommandCatalog(t.Context(), "c_room")
	if err != nil {
		t.Fatal(err)
	}
	if got.AgentID != "codex" || len(got.Commands) != 2 || got.Commands[0].Hint != "keep" || got.ReportedAt.IsZero() {
		t.Fatalf("command catalog = %+v", got)
	}
	got.Commands = nil
	if err := repo.SaveCommandCatalog(t.Context(), "c_room", drepo.AgentCommandCatalog{AgentID: "codex", Commands: []dservice.AgentCommand{}, ReportedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	replaced, err := repo.CommandCatalog(t.Context(), "c_room")
	if err != nil {
		t.Fatal(err)
	}
	if len(replaced.Commands) != 0 {
		t.Fatalf("replacement merged old commands: %+v", replaced.Commands)
	}
}

// No report is not an error: the conversation reads as the zero catalog.
func TestCommandCatalogMissingReadsAsZero(t *testing.T) {
	repo := New(t.TempDir(), nil)
	cat, err := repo.CommandCatalog(t.Context(), "c_missing")
	if err != nil || cat.AgentID != "" || cat.Commands != nil {
		t.Fatalf("CommandCatalog(absent) = %+v, %v — want zero, nil", cat, err)
	}
}

// The id becomes a path under the store; reject anything the domain id rule
// would reject instead of letting it escape the conversations directory.
func TestCommandCatalogRejectsAnInvalidConversationID(t *testing.T) {
	repo := New(t.TempDir(), nil)
	if err := repo.SaveCommandCatalog(t.Context(), "../escape", drepo.AgentCommandCatalog{}); err == nil {
		t.Fatal("a traversal id was stored")
	}
	if _, err := repo.CommandCatalog(t.Context(), "../escape"); err == nil {
		t.Fatal("a traversal id was read")
	}
}

// Deleting a conversation's command report is idempotent: present or absent,
// the room's delete lifecycle can call it and finish.
func TestDeleteCommandCatalog(t *testing.T) {
	repo := New(t.TempDir(), nil)
	if err := repo.SaveCommandCatalog(t.Context(), "c_room", drepo.AgentCommandCatalog{AgentID: "codex", Commands: []dservice.AgentCommand{{Name: "compact"}}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteCommandCatalog(t.Context(), "c_room"); err != nil {
		t.Fatal(err)
	}
	cat, err := repo.CommandCatalog(t.Context(), "c_room")
	if err != nil || cat.AgentID != "" {
		t.Fatalf("catalog survived delete: %+v, %v", cat, err)
	}
	if err := repo.DeleteCommandCatalog(t.Context(), "c_room"); err != nil {
		t.Fatalf("second delete: %v", err)
	}
}
