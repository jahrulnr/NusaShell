package skill

import (
	"context"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/repository"
)

// The page reads who may change each learned skill from the rule the candidate
// pass also reads: a row whose digest no longer matches the file is void. The
// reading writes nothing, and the page counts every proposal waiting on the
// owner, whichever skill it names.
func TestListReportsTheEffectiveLevelAndWritesNothing(t *testing.T) {
	w, catalog, _, _, deps := listSetup()
	catalog.skills = []repository.Skill{
		{Name: "release-notes", Description: "Use when writing release notes", Source: "nusashell", Path: "/n/release-notes/SKILL.md", Digest: "rd2"},
		{Name: "deploy-day", Description: "Use when deploying", Source: "nusashell", Path: "/n/deploy-day/SKILL.md", Digest: "dd1"},
		{Name: "fresh-skill", Description: "Use when starting fresh", Source: "nusashell", Path: "/n/fresh-skill/SKILL.md", Digest: "fs1"},
	}
	// release-notes was written at rd1 and the owner has since edited it to rd2,
	// so its agent row no longer describes the file. deploy-day's row still does.
	edited, err := aggregate.NewSkillOwnership(enum.SkillRootNusashell, "release-notes", enum.SkillLevelAgent, "rd1")
	if err != nil {
		t.Fatal(err)
	}
	intact, err := aggregate.NewSkillOwnership(enum.SkillRootNusashell, "deploy-day", enum.SkillLevelAgent, "dd1")
	if err != nil {
		t.Fatal(err)
	}
	w.rows["nusashell/release-notes"] = edited
	w.rows["nusashell/deploy-day"] = intact
	w.proposals["p1"] = listProposal(t, "p1", "release-notes")
	w.proposals["p2"] = listProposal(t, "p2", "fresh-idea")

	page, err := List(context.Background(), deps, "/ws")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	byName := map[string]SkillRow{}
	for _, r := range page.Rows {
		byName[r.Name] = r
	}
	if r := byName["release-notes"]; r.Level != "user" || !r.Tracked || !r.Changed || r.Pending != 1 {
		t.Errorf("an edited agent skill reads %+v, want the owner's level, tracked and changed", r)
	}
	if r := byName["deploy-day"]; r.Level != "agent" || !r.Tracked || r.Changed {
		t.Errorf("an intact agent skill reads %+v, want agent, tracked, unchanged", r)
	}
	if r := byName["fresh-skill"]; r.Level != "user" || r.Tracked || r.Changed {
		t.Errorf("a learned skill with no row reads %+v, want the owner's, untracked", r)
	}
	if page.Pending != 2 {
		t.Errorf("pending = %d, want every proposal the owner decides: 2, the one for fresh-idea included", page.Pending)
	}
	// The reading wrote nothing: the row the page found is the row it leaves.
	if got := w.rows["nusashell/release-notes"]; got.Level() != enum.SkillLevelAgent || got.Digest() != "rd1" {
		t.Errorf("the page rewrote the release-notes row to %s at %s", got.Level(), got.Digest())
	}
}

// candidatePatchWorld is a learned skill at agent level, written at rowDigest,
// that sits on disk at diskDigest.
func candidatePatchWorld(t *testing.T, rowDigest, diskDigest string) (*decideWorld, *candidateGen) {
	t.Helper()
	w := &decideWorld{proposals: map[string]*aggregate.SkillProposal{}, rows: map[string]*aggregate.SkillOwnership{}, files: map[string]string{}}
	row, err := aggregate.NewSkillOwnership(enum.SkillRootNusashell, "pre-commit-tests", enum.SkillLevelAgent, rowDigest)
	if err != nil {
		t.Fatal(err)
	}
	w.rows["nusashell/pre-commit-tests"] = row
	w.catalog = []repository.Skill{{
		Name: "pre-commit-tests", Description: "Use when committing code, to verify the tests first",
		Source: "nusashell", Path: "/n/pre-commit-tests/SKILL.md", Digest: diskDigest,
	}}
	return w, &candidateGen{reply: candidatePatchReply()}
}

func candidatePatchReply() string {
	return `{"ops": [{
		"op": "patch", "kind": "method", "name": "pre-commit-tests", "target": "pre-commit-tests",
		"description": "Use when committing code, to verify the tests first",
		"body": "## Steps\n1. run the tests\n2. commit only when they pass",
		"reason": "the owner states this rule explicitly",
		"evidence": {"kind": "quote", "quote": "run the tests before you commit"}
	}]}`
}

// A patch of a skill the owner edited by hand never becomes a proposal. The row
// says agent, but the bytes on disk are not the bytes the row was written for,
// so the pass judges the target as the owner's and the validator refuses it.
// Nothing is queued that the owner could never accept.
func TestCandidateNeverProposesAPatchForAHandEditedSkill(t *testing.T) {
	w, gen := candidatePatchWorld(t, "rd1", "rd2")
	outcome := Candidate(context.Background(), candidateDeps(w, gen), candidateInput(candidateRoom(t, candidateOwnerLine)))
	if outcome != CandidateRejected {
		t.Errorf("outcome %s, want rejected — the target is the owner's now", outcome)
	}
	if len(w.proposals) != 0 {
		t.Errorf("a patch of a hand-edited skill became %d pending proposal(s); the owner could never accept it", len(w.proposals))
	}
}

// The control for the test above: the same draft over bytes the row still
// describes is a proposal, so the refusal comes from the edit and not from the
// draft.
func TestCandidateProposesAPatchForAnUnchangedAgentSkill(t *testing.T) {
	w, gen := candidatePatchWorld(t, "rd1", "rd1")
	outcome := Candidate(context.Background(), candidateDeps(w, gen), candidateInput(candidateRoom(t, candidateOwnerLine)))
	if outcome != CandidateStored {
		t.Fatalf("outcome %s, want stored — the row still describes these bytes", outcome)
	}
	if len(w.proposals) != 1 {
		t.Fatalf("proposals = %d, want the one patch", len(w.proposals))
	}
	for _, p := range w.proposals {
		if p.Op() != enum.SkillProposalPatch || p.TargetName() != "pre-commit-tests" || p.TargetDigest() != "rd1" {
			t.Errorf("stored %s of %q at %q, want a patch of pre-commit-tests at rd1", p.Op(), p.TargetName(), p.TargetDigest())
		}
	}
}
