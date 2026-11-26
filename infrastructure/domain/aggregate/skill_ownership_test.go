package aggregate

import (
	"errors"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
)

func ownershipNow() time.Time { return time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC) }

func ownershipProposal(t *testing.T, id string, in SkillProposalInput) *SkillProposal {
	t.Helper()
	p, err := NewSkillProposal(id, in, ownershipNow())
	if err != nil {
		t.Fatalf("proposal fixture: %v", err)
	}
	return p
}

func ownershipCreateIn() SkillProposalInput {
	return SkillProposalInput{
		Op: enum.SkillProposalCreate, Kind: enum.SkillKindMethod,
		Name: "release-notes", Description: "Use when writing release notes from merged changes",
		Body:         "## Steps\n1. read the log\n2. group by area",
		EvidenceKind: enum.SkillEvidenceQuote, Evidence: "write release notes",
	}
}

func ownershipPatchIn() SkillProposalInput {
	in := ownershipCreateIn()
	in.Op = enum.SkillProposalPatch
	in.TargetName, in.TargetDigest, in.TargetLevel = "release-notes", "d1", enum.SkillLevelAgent
	return in
}

func acceptedProposal(t *testing.T, id string, in SkillProposalInput, level enum.SkillLevel) *SkillProposal {
	t.Helper()
	p := ownershipProposal(t, id, in)
	if err := p.Accept("/root/skills/"+p.Name()+"/SKILL.md", level, ownershipNow()); err != nil {
		t.Fatalf("accept fixture: %v", err)
	}
	return p
}

func mustOwnership(t *testing.T, root enum.SkillRoot, name string, level enum.SkillLevel, digest string) *SkillOwnership {
	t.Helper()
	o, err := NewSkillOwnership(root, name, level, digest)
	if err != nil {
		t.Fatalf("ownership fixture: %v", err)
	}
	return o
}

func TestNewSkillOwnershipValidatesItsFields(t *testing.T) {
	if _, err := NewSkillOwnership(enum.SkillRootNusashell, "release-notes", enum.SkillLevelAgent, "d1"); err != nil {
		t.Errorf("a valid row was refused: %v", err)
	}
	for _, c := range []struct {
		name      string
		root      enum.SkillRoot
		skillName string
		level     enum.SkillLevel
		digest    string
	}{
		{"a bad root", "elsewhere", "release-notes", enum.SkillLevelAgent, "d1"},
		{"an empty name", enum.SkillRootNusashell, "", enum.SkillLevelAgent, "d1"},
		{"a bad level", enum.SkillRootNusashell, "release-notes", "owner", "d1"},
		{"an empty digest", enum.SkillRootNusashell, "release-notes", enum.SkillLevelAgent, ""},
	} {
		if _, err := NewSkillOwnership(c.root, c.skillName, c.level, c.digest); err == nil {
			t.Errorf("%s was accepted", c.name)
		}
	}
}

func TestSkillOwnershipObserveFlipsOnlyOnARealChange(t *testing.T) {
	o := mustOwnership(t, enum.SkillRootNusashell, "release-notes", enum.SkillLevelAgent, "d1")
	if err := o.Observe("d1"); err != nil {
		t.Fatalf("the same digest was refused: %v", err)
	}
	if o.Level() != enum.SkillLevelAgent {
		t.Error("an unchanged file flipped the skill to the owner")
	}
	if err := o.Observe("d2"); err != nil {
		t.Fatalf("a changed digest was refused: %v", err)
	}
	if o.Level() != enum.SkillLevelUser {
		t.Error("a changed file did not make the skill the owner's")
	}
}

func TestSkillOwnershipObserveRefusesAMissingFile(t *testing.T) {
	o := mustOwnership(t, enum.SkillRootNusashell, "release-notes", enum.SkillLevelAgent, "d1")
	if err := o.Observe(""); !errors.Is(err, ErrSkillMissing) {
		t.Errorf("an empty disk digest: %v; want ErrSkillMissing", err)
	}
	if o.Level() != enum.SkillLevelAgent {
		t.Error("a missing file moved the row")
	}
}

func TestApplySkillOwnershipCreateWritesANewRow(t *testing.T) {
	p := acceptedProposal(t, "p1", ownershipCreateIn(), enum.SkillLevelUser)
	row, err := ApplySkillOwnership(nil, p, "dw")
	if err != nil {
		t.Fatalf("a create's row was refused: %v", err)
	}
	if row.Root() != enum.SkillRootNusashell || row.Name() != "release-notes" {
		t.Errorf("row = %s/%s, want nusashell/release-notes", row.Root(), row.Name())
	}
	if row.Level() != enum.SkillLevelUser {
		t.Errorf("level = %s, want the accepted user", row.Level())
	}
	if row.Digest() != "dw" {
		t.Errorf("digest = %q, want the written dw", row.Digest())
	}
}

func TestApplySkillOwnershipNeedsAnAcceptedProposal(t *testing.T) {
	pending := ownershipProposal(t, "p1", ownershipCreateIn())
	if _, err := ApplySkillOwnership(nil, pending, "dw"); err == nil {
		t.Error("a pending proposal wrote a row")
	}
	dismissed := ownershipProposal(t, "p2", ownershipCreateIn())
	if err := dismissed.Dismiss("not a skill", ownershipNow()); err != nil {
		t.Fatalf("dismiss fixture: %v", err)
	}
	if _, err := ApplySkillOwnership(nil, dismissed, "dw"); err == nil {
		t.Error("a dismissed proposal wrote a row")
	}
}

func TestApplySkillOwnershipCreateRefusesAnExistingRow(t *testing.T) {
	p := acceptedProposal(t, "p1", ownershipCreateIn(), enum.SkillLevelAgent)
	row := mustOwnership(t, enum.SkillRootNusashell, "release-notes", enum.SkillLevelAgent, "d0")
	if _, err := ApplySkillOwnership(row, p, "dw"); !errors.Is(err, ErrSkillExists) {
		t.Errorf("a create over an existing row: %v; want ErrSkillExists", err)
	}
}

func TestApplySkillOwnershipPatchKeepsTheAgentLevel(t *testing.T) {
	p := acceptedProposal(t, "p1", ownershipPatchIn(), enum.SkillLevelAgent)
	row := mustOwnership(t, enum.SkillRootNusashell, "release-notes", enum.SkillLevelAgent, "d1")
	got, err := ApplySkillOwnership(row, p, "dw")
	if err != nil {
		t.Fatalf("a patch's row was refused: %v", err)
	}
	if got.Level() != enum.SkillLevelAgent {
		t.Errorf("level = %s, want the unchanged agent", got.Level())
	}
	if got.Digest() != "dw" {
		t.Errorf("digest = %q, want the written dw", got.Digest())
	}
}

func TestApplySkillOwnershipPatchRefusesAStaleTarget(t *testing.T) {
	p := acceptedProposal(t, "p1", ownershipPatchIn(), enum.SkillLevelAgent)
	row := mustOwnership(t, enum.SkillRootNusashell, "release-notes", enum.SkillLevelAgent, "d2")
	if _, err := ApplySkillOwnership(row, p, "dw"); !errors.Is(err, ErrSkillChanged) {
		t.Errorf("a patch against a moved digest: %v; want ErrSkillChanged", err)
	}
}

func TestApplySkillOwnershipPatchReportsTheChangeBeforeTheOwner(t *testing.T) {
	// An owner's edit moves the digest and the level together; the answer the
	// owner needs is the change, so it is checked first.
	p := acceptedProposal(t, "p1", ownershipPatchIn(), enum.SkillLevelAgent)
	row := mustOwnership(t, enum.SkillRootNusashell, "release-notes", enum.SkillLevelUser, "d2")
	if _, err := ApplySkillOwnership(row, p, "dw"); !errors.Is(err, ErrSkillChanged) {
		t.Errorf("a patch against an owner-edited file: %v; want ErrSkillChanged", err)
	}
}

func TestApplySkillOwnershipPatchRefusesAnOwnerRow(t *testing.T) {
	p := acceptedProposal(t, "p1", ownershipPatchIn(), enum.SkillLevelAgent)
	row := mustOwnership(t, enum.SkillRootNusashell, "release-notes", enum.SkillLevelUser, "d1")
	if _, err := ApplySkillOwnership(row, p, "dw"); !errors.Is(err, ErrSkillOwnedByOwner) {
		t.Errorf("a patch against the owner's row at the target digest: %v; want ErrSkillOwnedByOwner", err)
	}
}

func TestApplySkillOwnershipPatchRefusesAMissingOrForeignRow(t *testing.T) {
	p := acceptedProposal(t, "p1", ownershipPatchIn(), enum.SkillLevelAgent)
	row := mustOwnership(t, enum.SkillRootNusashell, "other-notes", enum.SkillLevelAgent, "d1")
	if _, err := ApplySkillOwnership(row, p, "dw"); !errors.Is(err, ErrSkillNoRow) {
		t.Errorf("a patch against a row of another name: %v; want ErrSkillNoRow", err)
	}
	if _, err := ApplySkillOwnership(nil, p, "dw"); !errors.Is(err, ErrSkillNoRow) {
		t.Errorf("a patch with no row: %v; want ErrSkillNoRow", err)
	}
}

func TestMoveLevelRegrantMovesTheDigestToWhatTheOwnerSaw(t *testing.T) {
	row, err := NewSkillOwnership(enum.SkillRootNusashell, "release", enum.SkillLevelUser, "digest-before")
	if err != nil {
		t.Fatal(err)
	}
	if err := row.MoveLevel(enum.SkillLevelAgent, "digest-seen"); err != nil {
		t.Fatal(err)
	}
	if row.Level() != enum.SkillLevelAgent || row.Digest() != "digest-seen" {
		t.Fatalf("re-grant = %s %q, want agent at the seen digest", row.Level(), row.Digest())
	}
}

func TestMoveLevelToUserKeepsTheDigestAsHistory(t *testing.T) {
	row, err := NewSkillOwnership(enum.SkillRootNusashell, "release", enum.SkillLevelAgent, "digest-written")
	if err != nil {
		t.Fatal(err)
	}
	if err := row.MoveLevel(enum.SkillLevelUser, "digest-seen"); err != nil {
		t.Fatal(err)
	}
	if row.Level() != enum.SkillLevelUser || row.Digest() != "digest-written" {
		t.Fatalf("take-back = %s %q, want user with the digest unmoved", row.Level(), row.Digest())
	}
}

func TestMoveLevelRefusesAnEmptyDigestAndABadLevel(t *testing.T) {
	row, err := NewSkillOwnership(enum.SkillRootNusashell, "release", enum.SkillLevelAgent, "digest-written")
	if err != nil {
		t.Fatal(err)
	}
	if err := row.MoveLevel(enum.SkillLevelAgent, " "); err != ErrSkillMissing {
		t.Fatalf("empty digest err = %v, want ErrSkillMissing", err)
	}
	if err := row.MoveLevel("everyone", "digest-seen"); err == nil {
		t.Fatal("a level outside user, agent was accepted")
	}
	if row.Level() != enum.SkillLevelAgent || row.Digest() != "digest-written" {
		t.Fatal("a refused move moved the row")
	}
}
