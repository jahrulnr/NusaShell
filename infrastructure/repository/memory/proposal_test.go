package repomemory

import (
	"context"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

func proposal(t *testing.T, conv string, op enum.MemoryProposalOp, target *aggregate.Memory, now time.Time) *aggregate.MemoryProposal {
	t.Helper()
	in := aggregate.MemoryProposalInput{
		ConversationID: conv, MessageID: "m_1", Op: op, Scope: valueobject.GlobalMemoryScope(), Type: enum.MemoryTypeUser,
		Name: "deploy-day", Description: "Owner deploys on Fridays", Body: "Fridays.", Evidence: "I deploy on Fridays",
	}
	if target != nil {
		in.TargetID, in.TargetUpdatedAt = target.ID(), target.UpdatedAt()
	}
	p, err := aggregate.NewMemoryProposal("", in, now)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestProposalsRoundTripAndDecideOnce(t *testing.T) {
	repo := open(t)
	props := NewProposals(repo.db)
	ctx := context.Background()
	now := time.UnixMilli(1_790_000_000_000)
	p := proposal(t, "c_1", enum.MemoryProposalAdd, nil, now)
	p.SetSimilar([]string{"mem_a", "mem_b"})
	if err := props.Add(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, err := props.Get(ctx, p.ID())
	if err != nil || got.Evidence() != "I deploy on Fridays" || len(got.Similar()) != 2 || got.MessageID() != "m_1" {
		t.Fatalf("got %+v %v", got, err)
	}
	if n, _ := props.CountPending(ctx); n != 1 {
		t.Fatalf("pending = %d", n)
	}
	key := drepo.MemoryProposalKey{Op: enum.MemoryProposalAdd, Scope: valueobject.GlobalMemoryScope(), Name: "deploy-day"}
	if seen, _ := props.Seen(ctx, key, "c_2", now); !seen {
		t.Fatal("a pending suggestion was not seen")
	}
	_ = got.Dismiss(now)
	if err := props.Decide(ctx, got); err != nil {
		t.Fatal(err)
	}
	// A copy read before the dismissal loses when it decides too.
	stale, _ := aggregate.NewMemoryProposal(p.ID(), aggregate.MemoryProposalInput{
		ConversationID: "c_1", Op: enum.MemoryProposalAdd, Scope: valueobject.GlobalMemoryScope(), Type: enum.MemoryTypeUser,
		Name: "deploy-day", Description: "d", Body: "b", Evidence: "e"}, now)
	_ = stale.Accept("mem_x", now)
	if err := props.Decide(ctx, stale); status(err) != 409 {
		t.Fatalf("second decision: %v", err)
	}
	if seen, _ := props.Seen(ctx, key, "c_1", now.Add(-time.Hour)); !seen {
		t.Fatal("a recent dismissal from the same thread was not seen")
	}
	if seen, _ := props.Seen(ctx, key, "c_2", now.Add(-time.Hour)); seen {
		t.Fatal("another thread's dismissal blocked a suggestion")
	}
	if list, _ := props.List(ctx, []enum.MemoryProposalStatus{enum.MemoryProposalDismissed}, 10); len(list) != 1 {
		t.Fatalf("dismissed = %d", len(list))
	}
	if n, _ := props.PurgeDecided(ctx, now.Add(time.Minute)); n != 1 {
		t.Fatalf("purged %d", n)
	}
}

func TestProposalsFollowTheirMemory(t *testing.T) {
	repo := open(t)
	props := NewProposals(repo.db)
	ctx := context.Background()
	now := time.UnixMilli(1_790_000_000_000)
	m := memory(t, valueobject.GlobalMemoryScope(), enum.MemoryTypeUser, "deploy-day", "Fridays", "b", now)
	if err := repo.Save(ctx, m); err != nil {
		t.Fatal(err)
	}
	p := proposal(t, "c_1", enum.MemoryProposalUpdate, m, now)
	if err := props.Add(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := repo.Delete(ctx, m.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err := props.Get(ctx, p.ID()); status(err) != 404 {
		t.Fatalf("a suggestion outlived its memory: %v", err)
	}
	gone := proposal(t, "c_1", enum.MemoryProposalUpdate, m, now)
	if err := props.Add(ctx, gone); status(err) != 409 {
		t.Fatalf("a suggestion for a deleted memory: %v", err)
	}
}

func TestProposalsDismissedWithTheirThreadOrScope(t *testing.T) {
	repo := open(t)
	props := NewProposals(repo.db)
	ctx := context.Background()
	now := time.UnixMilli(1_790_000_000_000)
	_ = props.Add(ctx, proposal(t, "c_1", enum.MemoryProposalAdd, nil, now))
	if n, _ := props.DismissFrom(ctx, "c_1", now); n != 1 {
		t.Fatalf("dismissed from thread %d", n)
	}
	_ = props.Add(ctx, proposal(t, "c_2", enum.MemoryProposalAdd, nil, now))
	if n, _ := props.DismissIn(ctx, valueobject.GlobalMemoryScope(), now); n != 1 {
		t.Fatalf("dismissed in scope %d", n)
	}
	if n, _ := props.CountPending(ctx); n != 0 {
		t.Fatalf("pending %d", n)
	}
}
