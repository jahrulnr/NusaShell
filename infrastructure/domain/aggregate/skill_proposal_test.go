package aggregate

import (
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
)

var skillTestNow = time.Date(2026, 11, 26, 13, 0, 0, 0, time.UTC)

func skillCreateIn() SkillProposalInput {
	return SkillProposalInput{
		Op: enum.SkillProposalCreate, Kind: enum.SkillKindMethod, Name: "release-notes",
		Description: "Use when writing release notes from merged changes", Body: "## Steps\n1. read the log\n2. group by area",
		Evidence: "write release notes like the last one", EvidenceKind: enum.SkillEvidenceQuote,
	}
}

func skillPatchIn() SkillProposalInput {
	in := skillCreateIn()
	in.Op, in.TargetName, in.TargetDigest, in.TargetLevel = enum.SkillProposalPatch, "release-notes", "d1", enum.SkillLevelAgent
	return in
}

func TestSkillProposalCreateNamesNoTargetAndPatchNamesOne(t *testing.T) {
	if _, err := NewSkillProposal("sprop_1", skillCreateIn(), skillTestNow); err != nil {
		t.Fatal(err)
	}
	named := skillCreateIn()
	named.TargetName = "other"
	if _, err := NewSkillProposal("sprop_2", named, skillTestNow); err == nil {
		t.Error("a create that names a target was accepted")
	}
	if _, err := NewSkillProposal("sprop_3", skillPatchIn(), skillTestNow); err != nil {
		t.Fatal(err)
	}
	noDigest := skillPatchIn()
	noDigest.TargetDigest = ""
	if _, err := NewSkillProposal("sprop_4", noDigest, skillTestNow); err == nil {
		t.Error("a patch without its target digest was accepted")
	}
	noTarget := skillPatchIn()
	noTarget.TargetName = ""
	if _, err := NewSkillProposal("sprop_5", noTarget, skillTestNow); err == nil {
		t.Error("a patch without a target was accepted")
	}
}

func TestSkillProposalRefusesAnOpKindOrFieldItCannotDecide(t *testing.T) {
	for i, mutate := range []func(*SkillProposalInput){
		func(in *SkillProposalInput) { in.Op = "archive" },
		func(in *SkillProposalInput) { in.Kind = "skill" },
		func(in *SkillProposalInput) { in.Name = "   " },
		func(in *SkillProposalInput) { in.Description = "" },
		func(in *SkillProposalInput) { in.Body = "" },
		func(in *SkillProposalInput) { in.Evidence = "" },
		func(in *SkillProposalInput) { in.EvidenceKind = "" },
	} {
		in := skillCreateIn()
		mutate(&in)
		if _, err := NewSkillProposal("sprop_6", in, skillTestNow); err == nil {
			t.Errorf("case %d was accepted", i)
		}
	}
}

func TestSkillProposalRefusesAnEvidenceKindItCannotProve(t *testing.T) {
	unknown := skillCreateIn()
	unknown.EvidenceKind = "page"
	if _, err := NewSkillProposal("sprop_12", unknown, skillTestNow); err == nil {
		t.Error("an evidence kind outside quote, rewrite, repeat, call was accepted")
	}
	tool := skillCreateIn()
	tool.Kind = enum.SkillKindTool
	if _, err := NewSkillProposal("sprop_13", tool, skillTestNow); err == nil {
		t.Error("a tool skill resting on the owner's words was accepted")
	}
	tool.EvidenceKind, tool.Tool = enum.SkillEvidenceCall, "fetch_page"
	if _, err := NewSkillProposal("sprop_14", tool, skillTestNow); err != nil {
		t.Fatalf("a tool skill resting on a call was refused: %v", err)
	}
}

func TestSkillProposalRefusesADraftThatBreaksAShapeRule(t *testing.T) {
	in := skillCreateIn()
	in.Description = "Write release notes"
	if _, err := NewSkillProposal("sprop_15", in, skillTestNow); err == nil || !strings.Contains(err.Error(), "trigger") {
		t.Errorf("a description with no trigger: %v; want a refusal that names the trigger", err)
	}
}

func TestSkillProposalRefusesAToolSkillThatNamesNoTool(t *testing.T) {
	tool := skillCreateIn()
	tool.Kind, tool.EvidenceKind = enum.SkillKindTool, enum.SkillEvidenceCall
	if _, err := NewSkillProposal("sprop_20", tool, skillTestNow); err == nil {
		t.Error("a tool skill with no tool was accepted")
	}
	tool.Tool = "fetch_page"
	p, err := NewSkillProposal("sprop_21", tool, skillTestNow)
	if err != nil {
		t.Fatalf("a tool skill naming its tool was refused: %v", err)
	}
	if p.Tool() != "fetch_page" {
		t.Errorf("tool %q, want fetch_page", p.Tool())
	}
}

func TestSkillProposalExposesTheEvidenceKind(t *testing.T) {
	p, err := NewSkillProposal("sprop_16", skillCreateIn(), skillTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if p.EvidenceKind() != enum.SkillEvidenceQuote {
		t.Fatalf("evidence kind %q, want quote", p.EvidenceKind())
	}
}

func TestSkillProposalRestoresAStoredDecision(t *testing.T) {
	p, err := NewSkillProposal("sprop_17", skillCreateIn(), skillTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SetStored(enum.SkillProposalAccepted, "/learned/release-notes/SKILL.md", enum.SkillLevelUser, "", skillTestNow, skillTestNow); err != nil {
		t.Fatal(err)
	}
	if p.Status() != enum.SkillProposalAccepted || p.AcceptedPath() != "/learned/release-notes/SKILL.md" || p.AcceptedLevel() != enum.SkillLevelUser {
		t.Fatalf("restored accepted proposal: status %v, path %q, level %v", p.Status(), p.AcceptedPath(), p.AcceptedLevel())
	}
	d, err := NewSkillProposal("sprop_18", skillCreateIn(), skillTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.SetStored(enum.SkillProposalDismissed, "", "", "", skillTestNow, skillTestNow); err == nil {
		t.Error("a dismissed proposal with no reason was restored")
	}
	if err := d.SetStored(enum.SkillProposalAccepted, "", enum.SkillLevelAgent, "", skillTestNow, skillTestNow); err == nil {
		t.Error("an accepted proposal with no path was restored")
	}
	if err := d.SetStored("failed", "", "", "", skillTestNow, skillTestNow); err == nil {
		t.Error("a status outside pending, accepted, dismissed was restored")
	}
}

func TestSkillProposalStoredPatchStaysAtAgentLevel(t *testing.T) {
	// hydration may only produce states Accept could produce — a patch is
	// accepted at agent level, so a row carrying user level is corrupt
	p, err := NewSkillProposal("sprop_20", skillPatchIn(), skillTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SetStored(enum.SkillProposalAccepted, "/learned/release-notes/SKILL.md", enum.SkillLevelUser, "", skillTestNow, skillTestNow); err == nil {
		t.Error("a patch hydrated at the owner's level was restored")
	}
	if err := p.SetStored(enum.SkillProposalAccepted, "/learned/release-notes/SKILL.md", enum.SkillLevelAgent, "", skillTestNow, skillTestNow); err != nil {
		t.Errorf("a patch hydrated at agent level refused: %v", err)
	}
}

func TestSkillProposalRefusesAnInconsistentStoredDecision(t *testing.T) {
	stored := func(status enum.SkillProposalStatus, path string, level enum.SkillLevel, reason string, created, decided time.Time) error {
		p, err := NewSkillProposal("sprop_19", skillCreateIn(), skillTestNow)
		if err != nil {
			t.Fatal(err)
		}
		return p.SetStored(status, path, level, reason, created, decided)
	}
	for _, c := range []struct {
		name string
		err  error
		ok   bool
	}{
		{"a pending row with a path", stored(enum.SkillProposalPending, "/x/SKILL.md", "", "", skillTestNow, time.Time{}), false},
		{"a pending row with no decision", stored(enum.SkillProposalPending, "", "", "", skillTestNow, time.Time{}), true},
		{"an accepted row with a blank path", stored(enum.SkillProposalAccepted, "   ", enum.SkillLevelAgent, "", skillTestNow, skillTestNow), false},
		{"an accepted row with no decision time", stored(enum.SkillProposalAccepted, "/x/SKILL.md", enum.SkillLevelAgent, "", skillTestNow, time.Time{}), false},
		{"an accepted row that also carries a reason", stored(enum.SkillProposalAccepted, "/x/SKILL.md", enum.SkillLevelAgent, "why", skillTestNow, skillTestNow), false},
		{"a dismissed row with a path", stored(enum.SkillProposalDismissed, "/x/SKILL.md", "", "duplicate", skillTestNow, skillTestNow), false},
		{"a row with no creation time", stored(enum.SkillProposalPending, "", "", "", time.Time{}, time.Time{}), false},
	} {
		if got := c.err == nil; got != c.ok {
			t.Errorf("%s restored = %v, want %v (err %v)", c.name, got, c.ok, c.err)
		}
	}
}

func TestSkillProposalDecidesOnce(t *testing.T) {
	p, err := NewSkillProposal("sprop_7", skillCreateIn(), skillTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Accept("/learned/release-notes/SKILL.md", enum.SkillLevelAgent, skillTestNow); err != nil {
		t.Fatal(err)
	}
	if p.Status() != enum.SkillProposalAccepted || p.AcceptedPath() != "/learned/release-notes/SKILL.md" || p.AcceptedLevel() != enum.SkillLevelAgent {
		t.Fatalf("accepted proposal: status %v, path %q, level %v", p.Status(), p.AcceptedPath(), p.AcceptedLevel())
	}
	if err := p.Accept("/learned/other/SKILL.md", enum.SkillLevelUser, skillTestNow); err == nil {
		t.Error("a second accept was recorded")
	}
	if err := p.Dismiss("duplicate", skillTestNow); err == nil {
		t.Error("a dismissal after an accept was recorded")
	}

	q, _ := NewSkillProposal("sprop_8", skillCreateIn(), skillTestNow)
	if err := q.Dismiss("   ", skillTestNow); err == nil {
		t.Error("a dismissal without a reason was recorded")
	}
	if err := q.Dismiss("duplicate of release-notes", skillTestNow); err != nil {
		t.Fatal(err)
	}
	if q.Status() != enum.SkillProposalDismissed || q.DismissReason() != "duplicate of release-notes" {
		t.Fatalf("dismissed proposal: status %v, reason %q", q.Status(), q.DismissReason())
	}
	if err := q.Accept("/learned/x/SKILL.md", enum.SkillLevelAgent, skillTestNow); err == nil {
		t.Error("an accept after a dismissal was recorded")
	}
}

// The reason the pass writes for a retirement is the pass's to write: an owner's
// dismissal that opens with it would make the cooldown drop a name held for a
// decision the owner really made.
func TestSkillProposalDismissalMayNotOpenWithTheSupersededReason(t *testing.T) {
	p, err := NewSkillProposal("sprop_s1", skillCreateIn(), skillTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Dismiss("superseded: the owner says so", skillTestNow); err == nil {
		t.Fatal("an owner's dismissal took the reason the pass writes")
	}
	if p.Status() != enum.SkillProposalPending {
		t.Fatalf("a refused dismissal left the proposal %v", p.Status())
	}
	if err := p.Dismiss("the tests already run in ci", skillTestNow); err != nil {
		t.Fatal(err)
	}
}

// A retirement is a dismissal with the cause the pass found: it needs a cause and
// a time, and a decided proposal is not retired again.
func TestSkillProposalSupersedeDismissesWithTheCause(t *testing.T) {
	p, err := NewSkillProposal("sprop_s2", skillCreateIn(), skillTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Supersede("  ", skillTestNow); err == nil {
		t.Error("a retirement without a cause was recorded")
	}
	if err := p.Supersede("the target changed since it was proposed", time.Time{}); err == nil {
		t.Error("a retirement without a time was recorded")
	}
	cause := "the target changed since it was proposed"
	if err := p.Supersede(cause, skillTestNow); err != nil {
		t.Fatal(err)
	}
	if p.Status() != enum.SkillProposalDismissed || p.DismissReason() != SupersededReasonPrefix+cause || !p.DecidedAt().Equal(skillTestNow) {
		t.Fatalf("retired proposal: status %v, reason %q, decided %v", p.Status(), p.DismissReason(), p.DecidedAt())
	}
	if err := p.Supersede(cause, skillTestNow); err == nil {
		t.Error("a decided proposal was retired again")
	}
}

func TestSkillProposalAcceptNeedsAPathAndALevel(t *testing.T) {
	p, _ := NewSkillProposal("sprop_9", skillCreateIn(), skillTestNow)
	if err := p.Accept("  ", enum.SkillLevelAgent, skillTestNow); err == nil {
		t.Error("an accept without a path was recorded")
	}
	if err := p.Accept("/learned/x/SKILL.md", "owner", skillTestNow); err == nil {
		t.Error("an accept with an unknown level was recorded")
	}
	if p.Status() != enum.SkillProposalPending {
		t.Fatal("a refused accept moved the proposal")
	}
}

func TestSkillProposalChangesOnlyWhenItsTargetMovedOn(t *testing.T) {
	p, _ := NewSkillProposal("sprop_10", skillPatchIn(), skillTestNow)
	same, _ := NewSkillOwnership(enum.SkillRootNusashell, "release-notes", enum.SkillLevelAgent, "d1")
	moved, _ := NewSkillOwnership(enum.SkillRootNusashell, "release-notes", enum.SkillLevelAgent, "d2")
	other, _ := NewSkillOwnership(enum.SkillRootNusashell, "other", enum.SkillLevelAgent, "d1")
	if p.Changes(same) {
		t.Error("an unchanged target reads as changed")
	}
	if !p.Changes(moved) {
		t.Error("a target with another digest reads as unchanged")
	}
	if !p.Changes(other) {
		t.Error("another skill's row reads as the target")
	}
	if !p.Changes(nil) {
		t.Error("an absent target reads as unchanged")
	}
	owned, _ := NewSkillOwnership(enum.SkillRootNusashell, "release-notes", enum.SkillLevelUser, "d1")
	if !p.Changes(owned) {
		t.Error("a target the owner took over reads as unchanged")
	}
	c, _ := NewSkillProposal("sprop_11", skillCreateIn(), skillTestNow)
	if c.Changes(nil) {
		t.Error("a create has no target to change")
	}
}

func TestSkillProposalAcceptKeepsAPatchAtAgentLevel(t *testing.T) {
	p, err := NewSkillProposal("p1", skillPatchIn(), skillTestNow)
	if err != nil {
		t.Fatalf("patch fixture: %v", err)
	}
	if err := p.Accept("/root/skills/release-notes/SKILL.md", enum.SkillLevelUser, skillTestNow); err == nil {
		t.Error("a patch accepted at user level")
	}
	if err := p.Accept("/root/skills/release-notes/SKILL.md", enum.SkillLevelAgent, skillTestNow); err != nil {
		t.Errorf("a patch accepted at agent level was refused: %v", err)
	}
}

func TestSkillProposalDecisionNeedsATime(t *testing.T) {
	p, err := NewSkillProposal("p1", skillCreateIn(), skillTestNow)
	if err != nil {
		t.Fatalf("create fixture: %v", err)
	}
	if err := p.Accept("/root/skills/release-notes/SKILL.md", enum.SkillLevelAgent, time.Time{}); err == nil {
		t.Error("an accept with no time was recorded")
	}
	if err := p.Dismiss("not a skill", time.Time{}); err == nil {
		t.Error("a dismissal with no time was recorded")
	}
	if _, err := NewSkillProposal("p2", skillCreateIn(), time.Time{}); err == nil {
		t.Error("a proposal with no creation time was built")
	}
}
