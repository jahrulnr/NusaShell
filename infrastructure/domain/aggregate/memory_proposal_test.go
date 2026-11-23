package aggregate

import (
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

func proposalIn(op enum.MemoryProposalOp) MemoryProposalInput {
	in := MemoryProposalInput{
		ConversationID: "c_1", MessageID: "m_1", Op: op, Scope: valueobject.GlobalMemoryScope(), Type: enum.MemoryTypeUser,
		Name: "deploy-day", Description: "Owner deploys on Fridays", Body: "Said so in a thread.", Evidence: "I always  deploy\non Fridays",
	}
	switch op {
	case enum.MemoryProposalUpdate:
		in.TargetID, in.TargetUpdatedAt = "mem_1", memNow
	case enum.MemoryProposalStale:
		in.TargetID, in.TargetUpdatedAt, in.Description, in.Body, in.Reason = "mem_1", memNow, "", "", "Owner moved to Mondays"
	}
	return in
}

func TestMemoryProposalOps(t *testing.T) {
	for _, op := range []enum.MemoryProposalOp{enum.MemoryProposalAdd, enum.MemoryProposalUpdate, enum.MemoryProposalStale} {
		p, err := NewMemoryProposal("", proposalIn(op), memNow)
		if err != nil {
			t.Fatalf("%s: %v", op, err)
		}
		if p.Status() != enum.MemoryProposalPending || p.Evidence() != "I always deploy on Fridays" {
			t.Fatalf("%s: %+v", op, p)
		}
	}
	bad := map[string]func(*MemoryProposalInput){
		"no thread":         func(in *MemoryProposalInput) { in.ConversationID = "" },
		"no evidence":       func(in *MemoryProposalInput) { in.Evidence = "  " },
		"bad name":          func(in *MemoryProposalInput) { in.Name = "Deploy Day" },
		"no body":           func(in *MemoryProposalInput) { in.Body = "" },
		"bad type":          func(in *MemoryProposalInput) { in.Type = "mood" },
		"add with a target": func(in *MemoryProposalInput) { in.TargetID = "mem_1" },
		"unknown op":        func(in *MemoryProposalInput) { in.Op = "merge" },
	}
	for name, mutate := range bad {
		in := proposalIn(enum.MemoryProposalAdd)
		mutate(&in)
		if _, err := NewMemoryProposal("", in, memNow); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	upd := proposalIn(enum.MemoryProposalUpdate)
	upd.TargetID = ""
	if _, err := NewMemoryProposal("", upd, memNow); err == nil {
		t.Error("an update without a target was accepted")
	}
	stale := proposalIn(enum.MemoryProposalStale)
	stale.Reason = ""
	if _, err := NewMemoryProposal("", stale, memNow); err == nil {
		t.Error("a stale without a reason was accepted")
	}
}

func TestMemoryProposalDecidedOnce(t *testing.T) {
	p, _ := NewMemoryProposal("", proposalIn(enum.MemoryProposalAdd), memNow)
	if err := p.Accept("mem_9", memNow); err != nil || p.AcceptedID() != "mem_9" {
		t.Fatalf("accept: %v", err)
	}
	if p.Accept("mem_9", memNow) == nil || p.Dismiss(memNow) == nil {
		t.Fatal("a decided suggestion was decided again")
	}
}

func TestMemoryProposalChanges(t *testing.T) {
	m := newMem(t, enum.MemoryTypeUser)
	in := proposalIn(enum.MemoryProposalUpdate)
	in.TargetID, in.TargetUpdatedAt = m.ID(), m.UpdatedAt()
	p, _ := NewMemoryProposal("", in, memNow)
	if p.Changes(m) {
		t.Fatal("an unchanged target reads as changed")
	}
	_ = m.Revise("New", "New body", enum.MemorySourceOwner, "", memNow.Add(time.Hour))
	if !p.Changes(m) || !p.Changes(nil) {
		t.Fatal("a changed or missing target reads as unchanged")
	}
}
