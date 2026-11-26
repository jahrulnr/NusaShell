package aggregate

import (
	"strings"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
)

func skillDraftOK() SkillProposalInput {
	return SkillProposalInput{
		Op: enum.SkillProposalCreate, Kind: enum.SkillKindMethod, Name: "release-notes",
		Description: "Use when writing release notes from merged changes",
		Body:        "## Steps\n1. read the log\n2. group by area",
		Evidence:    "write release notes like the last one", EvidenceKind: enum.SkillEvidenceQuote,
	}
}

func brokenRules(in SkillProposalInput) map[SkillRule]bool {
	got := map[SkillRule]bool{}
	for _, v := range CheckSkillDraft(in) {
		got[v.Rule] = true
	}
	return got
}

func TestSkillDraftThatFollowsEveryRuleIsAdmitted(t *testing.T) {
	if vs := CheckSkillDraft(skillDraftOK()); len(vs) != 0 {
		t.Fatalf("a valid method was refused: %+v", vs)
	}
}

func TestSkillRuleNameClassAcceptsOnlyClassLevelNames(t *testing.T) {
	for _, c := range []struct {
		name string
		ok   bool
	}{
		{"release-notes", true}, {"go-1-22", true}, {"x", true}, {strings.Repeat("a", SkillNameMax), true},
		{"", false}, {"Release-Notes", false}, {"release notes", false}, {"-release", false},
		{"release-", false}, {"release--notes", false}, {"release_notes", false}, {"1release", false},
		{strings.Repeat("a", SkillNameMax+1), false},
	} {
		in := skillDraftOK()
		in.Name = c.name
		if got := !brokenRules(in)[SkillRuleNameClass]; got != c.ok {
			t.Errorf("name-class(%q) passes = %v, want %v", c.name, got, c.ok)
		}
	}
}

func TestSkillRuleNameIncidentRefusesOneSessionsFingerprint(t *testing.T) {
	for _, c := range []struct {
		name string
		ok   bool
	}{
		{"release-notes", true}, {"go-1-22", true}, {"errors-catalog", true}, {"error-handling", true},
		{"panic-recovery", true}, {"timeout-tuning", true}, {"http-2", true},
		{"sha-256", true}, {"utf-16", true}, {"node-22", true}, {"postgres-16", true},
		// lowercase ticket shapes read as versions by design; the owner chose the uppercase form
		{"jira-12", true},
		{"fix-2026-11-26", false}, {"AB-1234", false}, {"exit-status-1", false}, {"status-500", false},
		// lowercase http never matches the ticket-key or error-code shapes; the owner
		// chose the uppercase ticket form, so http-404 reads as a version-like name
		{"http-404", true},
	} {
		in := skillDraftOK()
		in.Name = c.name
		if got := !brokenRules(in)[SkillRuleNameIncident]; got != c.ok {
			t.Errorf("name-incident(%q) passes = %v, want %v", c.name, got, c.ok)
		}
	}
}

func TestSkillRuleDescriptionLengthCapsAt160Characters(t *testing.T) {
	in := skillDraftOK()
	in.Description = "Use when " + strings.Repeat("a", SkillDescriptionMax-len("Use when "))
	if brokenRules(in)[SkillRuleDescriptionLength] {
		t.Error("a 160-character description was refused")
	}
	in.Description += "a"
	if !brokenRules(in)[SkillRuleDescriptionLength] {
		t.Error("a 161-character description was accepted")
	}
	in.Description = "Use when " + strings.Repeat("é", SkillDescriptionMax-len("Use when "))
	if brokenRules(in)[SkillRuleDescriptionLength] {
		t.Error("160 multibyte characters were refused: the cap counts characters, not bytes")
	}
}

func TestSkillRuleDescriptionTriggerNeedsAWholeUseWhenPhrase(t *testing.T) {
	for _, c := range []struct {
		desc string
		ok   bool
	}{
		{"Use when writing release notes", true},
		{"Write release notes. USE WHEN the log grows", true},
		{"Write release notes from merged changes", false},
		{"Writes release notes when asked", false},
		{"Reuse when the template matches", false},
		{"Misuse when inputs are odd", false},
		{"", false},
	} {
		in := skillDraftOK()
		in.Description = c.desc
		if got := !brokenRules(in)[SkillRuleDescriptionTrigger]; got != c.ok {
			t.Errorf("trigger(%q) passes = %v, want %v", c.desc, got, c.ok)
		}
	}
}

func TestSkillRuleOpShapeMatchesTheOpAndItsTarget(t *testing.T) {
	create := skillDraftOK()
	named := skillDraftOK()
	named.TargetName = "release-notes"
	patch := skillDraftOK()
	patch.Op, patch.TargetName, patch.TargetDigest, patch.TargetLevel = enum.SkillProposalPatch, "release-notes", "d1", enum.SkillLevelAgent
	renamed := patch
	renamed.Name = "release-log"
	noDigest := patch
	noDigest.TargetDigest = ""
	ownerLevel := patch
	ownerLevel.TargetLevel = enum.SkillLevelUser
	for _, c := range []struct {
		name string
		in   SkillProposalInput
		ok   bool
	}{
		{"a create with no target", create, true},
		{"a create that names a target", named, false},
		{"a patch that rewrites its own name", patch, true},
		{"a patch that renames the skill it rewrites", renamed, false},
		{"a patch with no digest", noDigest, false},
		{"a patch of a skill the owner took over", ownerLevel, false},
	} {
		if got := !brokenRules(c.in)[SkillRuleOpShape]; got != c.ok {
			t.Errorf("%s passes = %v, want %v", c.name, got, c.ok)
		}
	}
}

func TestSkillRuleBodySizeCapsAt100KiB(t *testing.T) {
	head := "## Steps\n1. read the log\n2. group by area\n"
	in := skillDraftOK()
	in.Body = head + strings.Repeat("a", SkillBodyMax-len(head))
	if brokenRules(in)[SkillRuleBodySize] {
		t.Error("a body of exactly 100 KB was refused")
	}
	in.Body += "a"
	if !brokenRules(in)[SkillRuleBodySize] {
		t.Error("a body one byte over 100 KB was accepted")
	}
}

func TestSkillRuleBodyShapeNeedsTwoSectionsOrTwoSteps(t *testing.T) {
	for _, c := range []struct {
		body string
		ok   bool
	}{
		{"## Steps\nfirst\n## Checks\nsecond", true},
		{"1. read the log\n2. group by area", true},
		{"## Only one\nsome text", false},
		{"plain text with no structure", false},
	} {
		in := skillDraftOK()
		in.Body = c.body
		if got := !brokenRules(in)[SkillRuleBodyShape]; got != c.ok {
			t.Errorf("body-shape(%q) passes = %v, want %v", c.body, got, c.ok)
		}
	}
}

func TestSkillRuleEvidenceKindMatchesTheSkillKind(t *testing.T) {
	for _, c := range []struct {
		kind enum.SkillKind
		ev   enum.SkillEvidenceKind
		ok   bool
	}{
		{enum.SkillKindTool, enum.SkillEvidenceCall, true},
		{enum.SkillKindTool, enum.SkillEvidenceQuote, false},
		{enum.SkillKindTool, enum.SkillEvidenceRewrite, false},
		{enum.SkillKindTool, enum.SkillEvidenceRepeat, false},
		{enum.SkillKindMethod, enum.SkillEvidenceQuote, true},
		{enum.SkillKindMethod, enum.SkillEvidenceRewrite, true},
		{enum.SkillKindMethod, enum.SkillEvidenceRepeat, true},
		{enum.SkillKindMethod, enum.SkillEvidenceCall, false},
	} {
		in := skillDraftOK()
		in.Kind, in.EvidenceKind = c.kind, c.ev
		if got := !brokenRules(in)[SkillRuleEvidenceKind]; got != c.ok {
			t.Errorf("%s resting on %s passes = %v, want %v", c.kind, c.ev, got, c.ok)
		}
	}
	in := skillDraftOK()
	in.Kind = ""
	if brokenRules(in)[SkillRuleEvidenceKind] {
		t.Error("an unknown kind reported an evidence rule; the caller reports the kind")
	}
}

func TestSkillRuleEvidenceNeedsItsTextOnceProven(t *testing.T) {
	in := skillDraftOK()
	in.Evidence = "   "
	if !brokenRules(in)[SkillRuleEvidenceKind] {
		t.Error("a proven citation with no text was admitted")
	}
	in = skillDraftOK()
	in.EvidenceKind, in.Evidence = "", ""
	if brokenRules(in)[SkillRuleEvidenceKind] {
		t.Error("an unproven citation reported a text rule; the caller reports the citation")
	}
}

func TestSkillRuleToolNameNeedsTheToolAToolSkillIsAbout(t *testing.T) {
	in := skillDraftOK()
	in.Kind, in.EvidenceKind, in.Tool = enum.SkillKindTool, enum.SkillEvidenceCall, ""
	if !brokenRules(in)[SkillRuleToolName] {
		t.Error("a tool skill that names no tool was admitted")
	}
	in.Tool = "fetch_page"
	if brokenRules(in)[SkillRuleToolName] {
		t.Error("a tool skill naming its tool was refused")
	}
	if brokenRules(skillDraftOK())[SkillRuleToolName] {
		t.Error("a method reported a tool rule")
	}
}

func TestSkillDraftReportsEveryRuleItBreaks(t *testing.T) {
	in := skillDraftOK()
	in.Name = "Release Notes"
	in.Description = "Write release notes " + strings.Repeat("x", 150)
	in.Body = "## Only one\nplain"
	in.Kind, in.EvidenceKind = enum.SkillKindTool, enum.SkillEvidenceQuote
	in.Tool = "fetch_page"
	want := map[SkillRule]bool{
		SkillRuleNameClass: true, SkillRuleDescriptionLength: true, SkillRuleDescriptionTrigger: true,
		SkillRuleBodyShape: true, SkillRuleEvidenceKind: true,
	}
	got := brokenRules(in)
	for r := range want {
		if !got[r] {
			t.Errorf("rule %s not reported", r)
		}
	}
	for r := range got {
		if !want[r] {
			t.Errorf("rule %s reported but not broken", r)
		}
	}
}

func TestHasDateOrTicketFindsDatesAndTicketNumbers(t *testing.T) {
	for _, c := range []struct {
		text string
		want bool
	}{
		{"shipped on 2026-11-26", true}, {"see AB-1234", true}, {"fixed in JIRA-12", true}, {"closes #1234", true},
		{"plain text", false}, {"go 1.22 and go-1-22", false}, {"sha-256 and utf-16", false}, {"node-22 and postgres-16", false},
	} {
		if got := HasDateOrTicket(c.text); got != c.want {
			t.Errorf("HasDateOrTicket(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

func TestAFifteenKBMethodBodyIsAdmitted(t *testing.T) {
	in := skillDraftOK()
	in.Body = strings.Repeat("## Step\n1. read the log, then group it by area\n", 320)
	if len(in.Body) < 14*1024 || len(in.Body) > 16*1024 {
		t.Fatalf("fixture is %d bytes, want about 15 KB", len(in.Body))
	}
	if vs := CheckSkillDraft(in); len(vs) != 0 {
		t.Fatalf("a 15 KB method was refused: %+v", vs)
	}
}

func TestSkillRuleQuoteNeedsThreeWords(t *testing.T) {
	for _, c := range []struct {
		quote string
		ok    bool
	}{
		{"release", false},
		{"release notes", false},
		{"write release notes", true},
		{"ship: the notes!", true},
	} {
		in := skillDraftOK()
		in.Evidence = c.quote
		if got := !brokenRules(in)[SkillRuleQuoteWords]; got != c.ok {
			t.Errorf("quote-words(%q) passes = %v, want %v", c.quote, got, c.ok)
		}
	}
}

func TestSkillRuleBlankQuoteIsOnlyTheMissingEvidence(t *testing.T) {
	in := skillDraftOK()
	in.Evidence = "  "
	broken := brokenRules(in)
	if !broken[SkillRuleEvidenceKind] {
		t.Error("a blank citation was not reported as missing evidence")
	}
	if broken[SkillRuleQuoteWords] {
		t.Error("a blank citation was also reported as a short quote: one cause, one violation")
	}
}

func TestSkillRuleDescriptionIncidentRefusesOneSessionsFingerprint(t *testing.T) {
	in := skillDraftOK()
	in.Description = "Use when writing release notes for JIRA-12"
	if !brokenRules(in)[SkillRuleDescriptionIncident] {
		t.Error("a ticket number in the description passes")
	}
	in = skillDraftOK()
	if brokenRules(in)[SkillRuleDescriptionIncident] {
		t.Error("a plain description was refused")
	}
}
