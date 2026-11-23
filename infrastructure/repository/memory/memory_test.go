package repomemory

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	"github.com/jahrulnr/nusashell/infrastructure/repository/store"
	"github.com/jahrulnr/nusashell/migration"
)

func open(t *testing.T) *Repository {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "n.db"), migration.Files)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return New(db)
}

func memory(t *testing.T, scope valueobject.MemoryScope, typ enum.MemoryType, name, desc, body string, now time.Time) *aggregate.Memory {
	t.Helper()
	m, err := aggregate.NewMemory("", scope, typ, name, desc, body, enum.MemorySourceAgent, "c_1", now)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func status(err error) int {
	var ae *apperror.AppError
	if errors.As(err, &ae) {
		return ae.StatusCode()
	}
	return 0
}

func TestSaveFindAndUniqueLiveName(t *testing.T) {
	r, ctx := open(t), context.Background()
	now := time.UnixMilli(1_800_000_000_000)
	global := valueobject.GlobalMemoryScope()
	proj, _ := valueobject.ProjectMemoryScope("p_1")
	m := memory(t, global, enum.MemoryTypeFeedback, "terse-replies", "Owner wants short answers", "Keep replies short.\n\n**Why:** reads on phone.", now)
	if err := r.Save(ctx, m); err != nil {
		t.Fatal(err)
	}
	got, err := r.FindByName(ctx, global, "terse-replies")
	if err != nil || got.ID() != m.ID() || got.Body() != m.Body() || !got.VerifiedAt().Equal(now) || got.Status() != enum.MemoryStatusActive {
		t.Fatalf("got %+v, %v", got, err)
	}
	if _, err := r.FindByName(ctx, proj, "terse-replies"); status(err) != 404 {
		t.Fatalf("other scope: %v", err)
	}
	dup := memory(t, global, enum.MemoryTypeUser, "terse-replies", "d", "b", now)
	if err := r.Save(ctx, dup); status(err) != 409 {
		t.Fatalf("duplicate live name: %v", err)
	}
	// A forgotten memory frees its name.
	if err := m.Archive("wrong", now); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(ctx, m); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(ctx, dup); err != nil {
		t.Fatalf("name of an archived memory: %v", err)
	}
	if n, err := r.PurgeArchived(ctx, now.Add(time.Second)); err != nil || n != 1 {
		t.Fatalf("purge = %d, %v", n, err)
	}
	if _, err := r.Get(ctx, m.ID()); status(err) != 404 {
		t.Fatalf("purged memory still there: %v", err)
	}
}

func TestListRanksAndFilters(t *testing.T) {
	r, ctx := open(t), context.Background()
	now := time.UnixMilli(1_800_000_000_000)
	global := valueobject.GlobalMemoryScope()
	room, _ := valueobject.RoomMemoryScope("c_9")
	for _, m := range []*aggregate.Memory{
		memory(t, global, enum.MemoryTypeProject, "deploy-target", "Production deploys go to the Hetzner box", "Deploy with make deploy; the box is hetzner-1.", now),
		memory(t, global, enum.MemoryTypeUser, "role", "Owner is a backend engineer", "Go and SQL daily; new to frontend.", now.Add(time.Minute)),
		memory(t, room, enum.MemoryTypeReference, "deploy-dashboard", "Grafana board for deploys", "https://grafana.example/d/deploy", now.Add(2*time.Minute)),
	} {
		if err := r.Save(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	all, err := r.List(ctx, drepo.MemoryFilter{})
	if err != nil || len(all) != 3 || all[0].Memory.Name() != "deploy-dashboard" {
		t.Fatalf("newest first: %v, %v", names(all), err)
	}
	hits, err := r.List(ctx, drepo.MemoryFilter{Query: "how do we deploy?"})
	if err != nil || len(hits) != 2 || hits[0].Score <= 0 || hits[0].Score > 1 {
		t.Fatalf("search: %v %v", names(hits), err)
	}
	hits, err = r.List(ctx, drepo.MemoryFilter{Query: "deploy", Scopes: []valueobject.MemoryScope{global}})
	if err != nil || len(hits) != 1 || hits[0].Memory.Name() != "deploy-target" {
		t.Fatalf("scoped search: %v %v", names(hits), err)
	}
	hits, err = r.List(ctx, drepo.MemoryFilter{Types: []enum.MemoryType{enum.MemoryTypeUser}})
	if err != nil || len(hits) != 1 {
		t.Fatalf("typed: %v %v", names(hits), err)
	}
	if hits, err := r.List(ctx, drepo.MemoryFilter{Query: `") OR * NEAR( "hetzner`}); err != nil || len(hits) != 1 {
		t.Fatalf("fts syntax in a query must be inert: %v %v", names(hits), err)
	}
	if n, err := r.DeleteScope(ctx, room); err != nil || n != 1 {
		t.Fatalf("delete scope = %d, %v", n, err)
	}
}

func TestEmbeddingsFollowText(t *testing.T) {
	r, ctx := open(t), context.Background()
	now := time.UnixMilli(1_800_000_000_000)
	m := memory(t, valueobject.GlobalMemoryScope(), enum.MemoryTypeUser, "tz", "Owner is in Jakarta", "UTC+7", now)
	if err := r.Save(ctx, m); err != nil {
		t.Fatal(err)
	}
	if err := r.SetEmbedding(ctx, m.ID(), "e1", []float32{0.5, -1, 3}); err != nil {
		t.Fatal(err)
	}
	got, err := r.Embeddings(ctx, "e1", nil)
	if err != nil || len(got[m.ID()]) != 3 || got[m.ID()][1] != -1 {
		t.Fatalf("got %v, %v", got, err)
	}
	if got, _ := r.Embeddings(ctx, "other-model", nil); len(got) != 0 {
		t.Fatalf("another model's vectors: %v", got)
	}
	m.Recalled(now)
	_ = r.Save(ctx, m)
	if got, _ := r.Embeddings(ctx, "e1", nil); len(got) != 1 {
		t.Fatal("a recall dropped the embedding")
	}
	_ = m.Revise("Owner is in Bali", "UTC+8", enum.MemorySourceOwner, "", now)
	_ = r.Save(ctx, m)
	if got, _ := r.Embeddings(ctx, "e1", nil); len(got) != 0 {
		t.Fatal("a rewrite kept the old embedding")
	}
}

func names(hits []drepo.MemoryHit) []string {
	var out []string
	for _, h := range hits {
		out = append(out, h.Memory.Name())
	}
	return out
}
