package skill

import (
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
)

var (
	validateTurnAt  = time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	validateQuote   = "write release notes like the last one"
	validateLearned = LiveSkill{Root: enum.SkillRootNusashell, Name: "release-notes-old", Description: "Keep release notes short", Digest: "d1", Level: enum.SkillLevelAgent}
)

func validateDraft() Draft {
	return Draft{
		Op: "create", Kind: "method", Name: "release-notes",
		Description: "Use when writing release notes from merged changes",
		Body:        "## Steps\n1. read the log\n2. group by area",
		Cite:        Citation{Kind: "quote", Quote: validateQuote},
	}
}

func validateFacts(owner string) Facts {
	return Facts{OwnerLines: []OwnerLine{{Turn: "t1", At: validateTurnAt, Text: owner}}}
}

func validateCheck(d Draft, f Facts, live ...LiveSkill) Check {
	return Check{Draft: d, Facts: f, Live: live, Budget: 1 << 20}
}

func validateReported(r Result) map[Rule]bool {
	got := map[Rule]bool{}
	for _, v := range r.Violations {
		got[v.Rule] = true
	}
	return got
}

func validateMessage(r Result, rule Rule) string {
	for _, v := range r.Violations {
		if v.Rule == rule {
			return v.Message
		}
	}
	return ""
}

// A citation's evidence time is the time of what it names: the owner's line it
// quotes, the latest turn a repeat cites, the turn a rewrite happened in, or the
// model call it names. A quote that matches no line has no time, and an injected
// call is never evidence, so it has none either.
func TestCitedAtIsTheTimeOfWhatItNames(t *testing.T) {
	at := func(h int) time.Time { return time.Date(2026, 11, 26, h, 0, 0, 0, time.UTC) }
	f := Facts{
		OwnerLines: []OwnerLine{
			{Turn: "msg_a", At: at(9), Text: "start from the idea. then share it"},
			{Turn: "msg_b", At: at(11), Text: "write the same notes as last time"},
		},
		TurnAt: at(12),
	}
	if got := citedAt(Citation{Quote: "start from the idea"}, enum.SkillEvidenceQuote, f); !got.Equal(at(9)) {
		t.Errorf("quote: %v, want the line it quotes at %v", got, at(9))
	}
	if got := citedAt(Citation{Turns: []string{"msg_a", "msg_b"}}, enum.SkillEvidenceRepeat, f); !got.Equal(at(11)) {
		t.Errorf("repeat: %v, want the latest cited turn at %v", got, at(11))
	}
	if got := citedAt(Citation{}, enum.SkillEvidenceRewrite, f); !got.Equal(at(12)) {
		t.Errorf("rewrite: %v, want the turn at %v", got, at(12))
	}
	call := validateCall(t, "call_1", "ok", false)
	fc := f
	fc.Calls = []entity.ToolCall{call}
	if got := citedAt(Citation{Call: "call_1"}, enum.SkillEvidenceCall, fc); got.IsZero() || !got.Equal(call.CallAt()) {
		t.Errorf("call: %v, want the call's own time %v", got, call.CallAt())
	}
	fc.Calls = []entity.ToolCall{validateInjected(t, "call_2", "x")}
	if got := citedAt(Citation{Call: "call_2"}, enum.SkillEvidenceCall, fc); !got.IsZero() {
		t.Errorf("an injected call supplied a time: %v", got)
	}
	if got := citedAt(Citation{Quote: "words the owner never said"}, enum.SkillEvidenceQuote, f); !got.IsZero() {
		t.Errorf("a quote with no line supplied a time: %v", got)
	}
}

func validateCall(t *testing.T, id, output string, failed bool) entity.ToolCall {
	t.Helper()
	tc, err := entity.NewToolCall(id, "fetch_page", `{"url":"https://example.com"}`, enum.TooltypeOrigin)
	if err != nil {
		t.Fatal(err)
	}
	tc.SetOutput(output)
	tc.SetFailed(failed)
	return tc
}

func validatePendingCall(t *testing.T, id string) entity.ToolCall {
	t.Helper()
	tc, err := entity.NewToolCall(id, "fetch_page", `{"url":"https://example.com"}`, enum.TooltypeOrigin)
	if err != nil {
		t.Fatal(err)
	}
	return tc
}

func validateInjected(t *testing.T, id, output string) entity.ToolCall {
	t.Helper()
	tc, err := entity.NewToolCall(id, "fetch_page", `{"path":"."}`, enum.TooltypeInjection)
	if err != nil {
		t.Fatal(err)
	}
	tc.SetOutput(output)
	return tc
}

func TestAGoodDraftWithAnOwnerQuoteIsAdmitted(t *testing.T) {
	if r := Validate(validateCheck(validateDraft(), validateFacts(validateQuote))); len(r.Violations) != 0 {
		t.Fatalf("a valid draft was refused: %+v", r.Violations)
	}
}

func TestOpRuleRefusesAnOpOutsideCreateAndPatch(t *testing.T) {
	d := validateDraft()
	d.Op = "archive"
	if !validateReported(Validate(validateCheck(d, validateFacts(validateQuote))))[RuleOp] {
		t.Error("op archive was admitted")
	}
}

func TestKindRulePointsAPreferenceAtMemory(t *testing.T) {
	d := validateDraft()
	d.Kind = "preference"
	if m := validateMessage(Validate(validateCheck(d, validateFacts(validateQuote))), RuleKind); !strings.Contains(m, "memory") {
		t.Errorf("preference message %q does not point at memory", m)
	}
}

func TestKindRuleRefusesAnUnknownKind(t *testing.T) {
	d := validateDraft()
	d.Kind = "skill"
	r := Validate(validateCheck(d, validateFacts(validateQuote)))
	if !validateReported(r)[RuleKind] || strings.Contains(validateMessage(r, RuleKind), "memory") {
		t.Errorf("unknown kind: %+v; want a kind violation that does not mention memory", r.Violations)
	}
}

func TestOpTargetNeedsALearnedSkillToPatch(t *testing.T) {
	global := LiveSkill{Root: enum.SkillRootGlobal, Name: "parse-go-modules", Description: "Parse go module graphs", Level: enum.SkillLevelAgent}
	for _, c := range []struct {
		name   string
		target string
		live   []LiveSkill
		ok     bool
	}{
		{"a learned skill", "release-notes-old", []LiveSkill{validateLearned}, true},
		{"a global skill", "parse-go-modules", []LiveSkill{global}, false},
		{"an unknown name", "missing", []LiveSkill{validateLearned}, false},
		{"no target", "", []LiveSkill{validateLearned}, false},
	} {
		d := validateDraft()
		d.Op, d.Target, d.Name = "patch", c.target, c.target
		if got := !validateReported(Validate(validateCheck(d, validateFacts(validateQuote), c.live...)))[RuleOpTarget]; got != c.ok {
			t.Errorf("%s: admitted = %v, want %v", c.name, got, c.ok)
		}
	}
}

func TestOwnerLevelSkillIsNotPatchedByTheLoop(t *testing.T) {
	shape := Rule(aggregate.SkillRuleOpShape)
	d := validateDraft()
	d.Op, d.Target, d.Name = "patch", "release-notes-old", "release-notes-old"
	owned := validateLearned
	owned.Level = enum.SkillLevelUser
	r := Validate(validateCheck(d, validateFacts(validateQuote), owned))
	if !validateReported(r)[shape] || !strings.Contains(validateMessage(r, shape), "agent-level") {
		t.Errorf("owner-level patch: %+v; want the agent-level rule", r.Violations)
	}
	if validateReported(Validate(validateCheck(d, validateFacts(validateQuote), validateLearned)))[shape] {
		t.Error("an agent-level patch was refused by the level rule")
	}
}

func TestOpShapeRefusesAPatchThatRenamesItsTargetAndATargetOnACreate(t *testing.T) {
	shape := Rule(aggregate.SkillRuleOpShape)
	renamed := validateDraft()
	renamed.Op, renamed.Target, renamed.Name = "patch", "release-notes-old", "release-log"
	if !validateReported(Validate(validateCheck(renamed, validateFacts(validateQuote), validateLearned)))[shape] {
		t.Error("a patch that renames the skill it rewrites was admitted")
	}
	same := renamed
	same.Name = "release-notes-old"
	if validateReported(Validate(validateCheck(same, validateFacts(validateQuote), validateLearned)))[shape] {
		t.Error("a patch that keeps its target's name was refused")
	}
	onCreate := validateDraft()
	onCreate.Target = "release-notes-old"
	if !validateReported(Validate(validateCheck(onCreate, validateFacts(validateQuote), validateLearned)))[shape] {
		t.Error("a create that names a target was admitted")
	}
}

func TestNameTakenNamesTheRootThatHoldsTheName(t *testing.T) {
	d := validateDraft()
	d.Name = "parse-go-modules"
	live := LiveSkill{Root: enum.SkillRootGlobal, Name: "parse-go-modules", Description: "Parse go module graphs"}
	m := validateMessage(Validate(validateCheck(d, validateFacts(validateQuote), live)), RuleNameTaken)
	if !strings.Contains(m, "global") || !strings.Contains(m, "rename the proposal or improve that skill by hand") {
		t.Errorf("taken name: %q; want the root and the hand-edit offer", m)
	}
}

func TestNameTakenDoesNotApplyToAPatchOfItself(t *testing.T) {
	d := validateDraft()
	d.Op, d.Name, d.Target = "patch", "release-notes-old", "release-notes-old"
	if validateReported(Validate(validateCheck(d, validateFacts(validateQuote), validateLearned)))[RuleNameTaken] {
		t.Error("a patch was refused for naming the skill it rewrites")
	}
}

func TestOverlapNamesTheClosestLiveSkill(t *testing.T) {
	live := LiveSkill{Root: enum.SkillRootGlobal, Name: "release-log", Description: "Use when writing release notes from merged changes"}
	r := Validate(validateCheck(validateDraft(), validateFacts(validateQuote), live))
	if !validateReported(r)[RuleOverlap] || !strings.Contains(validateMessage(r, RuleOverlap), "release-log") {
		t.Errorf("overlap: %+v; want the closest live name", r.Violations)
	}
}

func TestOverlapRejectsANearDuplicateAtEightTenths(t *testing.T) {
	d := validateDraft()
	d.Description = "Use when debugging go panic traces"
	live := LiveSkill{Root: enum.SkillRootGlobal, Name: "go-panic-debug", Description: "Use when debugging go panic traces logs"}
	if r := Validate(validateCheck(d, validateFacts(validateQuote), live)); !validateReported(r)[RuleOverlap] {
		t.Errorf("a description at 0.8 overlap was admitted: %+v", r.Violations)
	}
}

func TestOverlapNotesAPartialOverlapInsteadOfRejecting(t *testing.T) {
	live := LiveSkill{Root: enum.SkillRootGlobal, Name: "release-log", Description: "Use when writing release notes from merged pull requests"}
	r := Validate(validateCheck(validateDraft(), validateFacts(validateQuote), live))
	if len(r.Violations) != 0 {
		t.Fatalf("a partial overlap refused the draft: %+v", r.Violations)
	}
	if len(r.Notes) != 1 || !strings.Contains(r.Notes[0], "release-log") {
		t.Errorf("notes %v; want one note naming the partly overlapping skill", r.Notes)
	}
}

func TestOverlapIsNotCountedAgainstThePatchTarget(t *testing.T) {
	d := validateDraft()
	d.Op, d.Target, d.Name = "patch", "release-log", "release-log"
	live := LiveSkill{Root: enum.SkillRootNusashell, Name: "release-log", Description: "Use when writing release notes from merged changes", Digest: "d1", Level: enum.SkillLevelAgent}
	if validateReported(Validate(validateCheck(d, validateFacts(validateQuote), live)))[RuleOverlap] {
		t.Error("a patch overlapped with the skill it rewrites")
	}
}

func TestOverlapIgnoresTheSharedTriggerAndStopWords(t *testing.T) {
	d := validateDraft()
	d.Name, d.Description = "go-panic-triage", "Use when debugging a Go panic"
	live := LiveSkill{Root: enum.SkillRootGlobal, Name: "go-test-writing", Description: "Use when writing a Go test"}
	if r := Validate(validateCheck(d, validateFacts(validateQuote), live)); len(r.Violations) != 0 || len(r.Notes) != 0 {
		t.Errorf("two skills with different classes were flagged: %+v, notes %v", r.Violations, r.Notes)
	}
}

func TestCatalogBudgetCountsEveryLiveNameAndDescription(t *testing.T) {
	d := validateDraft()
	total := len(validateLearned.Name) + len(validateLearned.Description) + len(d.Name) + len(d.Description)
	c := validateCheck(d, validateFacts(validateQuote), validateLearned)
	c.Budget = total
	if validateReported(Validate(c))[RuleBudget] {
		t.Error("a catalog exactly at budget was refused")
	}
	c.Budget = total - 1
	if !validateReported(Validate(c))[RuleBudget] {
		t.Error("a catalog one byte over budget was admitted")
	}
}

func TestCatalogBudgetAPatchDoesNotCountItsTarget(t *testing.T) {
	d := validateDraft()
	d.Op, d.Target = "patch", validateLearned.Name
	// The catalog after the patch holds the other live skills plus the rewrite;
	// the target's current bytes leave with it, so they must not count. Were they
	// counted, the exact-budget case below would be over by their length.
	other := LiveSkill{Root: enum.SkillRootNusashell, Name: "other-skill", Description: "Use when doing other things"}
	total := len(other.Name) + len(other.Description) + len(d.Name) + len(d.Description)
	c := validateCheck(d, validateFacts(validateQuote), validateLearned, other)
	c.Budget = total
	if validateReported(Validate(c))[RuleBudget] {
		t.Error("a patch exactly at budget was refused: the target was counted")
	}
	c.Budget = total - 1
	if !validateReported(Validate(c))[RuleBudget] {
		t.Error("a patch one byte over budget was admitted")
	}
}

func TestCatalogBudgetMustBeSet(t *testing.T) {
	c := validateCheck(validateDraft(), validateFacts(validateQuote))
	c.Budget = 0
	if m := validateMessage(Validate(c), RuleBudget); !strings.Contains(m, "no catalog budget") {
		t.Errorf("zero budget: %q; want the unset-budget message", m)
	}
}

func TestEvidenceQuoteMustBeAnOwnerLineOfThisThread(t *testing.T) {
	r := Validate(validateCheck(validateDraft(), validateFacts("something the owner never wrote")))
	if !validateReported(r)[RuleEvidenceCited] || !strings.Contains(validateMessage(r, RuleEvidenceCited), "owner line") {
		t.Errorf("quote outside the owner's lines: %+v", r.Violations)
	}
}

func TestEvidenceQuoteNeedsThreeWholeWordsOfAnOwnerLine(t *testing.T) {
	owner := "please write release notes like the last one, thanks"
	for _, c := range []struct {
		quote string
		ok    bool
	}{
		{"e", false},
		{"release", false},
		{"release notes", false},
		{"write release note", false},
		{"write release notes", true},
		{"write release notes like the last one", true},
	} {
		d := validateDraft()
		d.Cite = Citation{Kind: "quote", Quote: c.quote}
		// Admission is what matters: a short quote that matches is refused by the
		// domain's quote-words rule, one that does not match by the citation rule.
		if got := len(Validate(validateCheck(d, validateFacts(owner))).Violations) == 0; got != c.ok {
			t.Errorf("quote %q admitted = %v, want %v", c.quote, got, c.ok)
		}
	}
}

func TestEvidenceQuoteCountsWordsNotCombiningMarks(t *testing.T) {
	owner := "कृपया प्रस्तुत करें"
	for _, c := range []struct {
		quote string
		ok    bool
	}{
		{"प्रस्तुत", false},
		{"कृपया प्रस्तुत करें", true},
	} {
		d := validateDraft()
		d.Cite = Citation{Kind: "quote", Quote: c.quote}
		if got := len(Validate(validateCheck(d, validateFacts(owner))).Violations) == 0; got != c.ok {
			t.Errorf("quote %q admitted = %v, want %v", c.quote, got, c.ok)
		}
	}
}

func TestEvidenceQuoteMatchesAcrossPunctuationAndLineBreaks(t *testing.T) {
	r := Validate(validateCheck(validateDraft(), validateFacts("please write release  notes\nlike the last one, thanks")))
	if validateReported(r)[RuleEvidenceCited] {
		t.Errorf("a quote across a line break was refused: %q", validateMessage(r, RuleEvidenceCited))
	}
}

func TestEvidenceCallNeedsAWorkingModelCallWithItsResult(t *testing.T) {
	for _, c := range []struct {
		name  string
		calls []entity.ToolCall
		ok    bool
	}{
		{"a working call", []entity.ToolCall{validateCall(t, "c1", "ok", false)}, true},
		{"a call that completed with an empty result", []entity.ToolCall{validateCall(t, "c1", "", false)}, true},
		{"a failed call", []entity.ToolCall{validateCall(t, "c1", "boom", true)}, false},
		{"a call with no recorded result", []entity.ToolCall{validatePendingCall(t, "c1")}, false},
		{"a call of another turn", []entity.ToolCall{validateCall(t, "c9", "ok", false)}, false},
		{"an injected entry with a result", []entity.ToolCall{validateInjected(t, "c1", "ok")}, false},
	} {
		d := validateDraft()
		d.Kind, d.Cite = "tool", Citation{Kind: "call", Call: "c1", Tool: "fetch_page"}
		if got := !validateReported(Validate(validateCheck(d, Facts{Calls: c.calls})))[RuleEvidenceCited]; got != c.ok {
			t.Errorf("%s: admitted = %v, want %v", c.name, got, c.ok)
		}
	}
}

func TestEvidenceCallNamesTheToolItUsed(t *testing.T) {
	calls := []entity.ToolCall{validateCall(t, "c1", "ok", false)}
	for _, c := range []struct {
		name string
		tool string
		ok   bool
	}{
		{"the tool the call used", "fetch_page", true},
		{"no tool", "", false},
		{"a tool the call did not use", "search", false},
	} {
		d := validateDraft()
		d.Kind, d.Cite = "tool", Citation{Kind: "call", Call: "c1", Tool: c.tool}
		if got := !validateReported(Validate(validateCheck(d, Facts{Calls: calls})))[RuleEvidenceCited]; got != c.ok {
			t.Errorf("%s: admitted = %v, want %v", c.name, got, c.ok)
		}
	}
}

func TestEvidenceRewriteNeedsACapturedRewrite(t *testing.T) {
	d := validateDraft()
	d.Cite = Citation{Kind: "rewrite"}
	if validateReported(Validate(validateCheck(d, Facts{Rewrite: true})))[RuleEvidenceCited] {
		t.Error("a captured rewrite was refused")
	}
	if !validateReported(Validate(validateCheck(d, Facts{})))[RuleEvidenceCited] {
		t.Error("a rewrite that was never captured was admitted")
	}
}

func TestEvidenceRepeatNeedsTwoDistinctOwnerTurnsWithinThirtyDays(t *testing.T) {
	at := func(days int) time.Time { return validateTurnAt.AddDate(0, 0, days) }
	f := Facts{OwnerLines: []OwnerLine{
		{Turn: "t1", At: at(0), Text: "first", OpensTurn: true},
		{Turn: "t2", At: at(29), Text: "second", OpensTurn: true},
		{Turn: "t3", At: at(31), Text: "third", OpensTurn: true},
		{Turn: "t4", At: at(0), Text: "a steer of the first turn"},
	}}
	for _, c := range []struct {
		name  string
		turns []string
		ok    bool
	}{
		{"two turns 29 days apart, with no shared quote", []string{"t1", "t2"}, true},
		{"two turns 31 days apart", []string{"t1", "t3"}, false},
		{"the same turn twice", []string{"t1", "t1"}, false},
		{"one turn", []string{"t1"}, false},
		{"a turn with no owner line", []string{"t1", "t9"}, false},
		{"an opener and its own steer are one occasion", []string{"t1", "t4"}, false},
	} {
		d := validateDraft()
		d.Cite = Citation{Kind: "repeat", Turns: c.turns}
		if got := !validateReported(Validate(validateCheck(d, f)))[RuleEvidenceCited]; got != c.ok {
			t.Errorf("%s: admitted = %v, want %v", c.name, got, c.ok)
		}
	}
}

func TestEvidenceRepeatHoldsFarFromTheEpoch(t *testing.T) {
	// Year 1 is the zero time, which the zero-time rule refuses first; year 1000 is far enough from 2026 to saturate Time.Sub.
	ancient := time.Date(1000, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name         string
		first, later time.Time
	}{
		{"an ancient turn cited first", ancient, validateTurnAt},
		{"an ancient turn cited second", validateTurnAt, ancient},
	} {
		f := Facts{OwnerLines: []OwnerLine{{Turn: "t1", At: c.first, Text: "a", OpensTurn: true}, {Turn: "t2", At: c.later, Text: "b", OpensTurn: true}}}
		d := validateDraft()
		d.Cite = Citation{Kind: "repeat", Turns: []string{"t1", "t2"}}
		if !validateReported(Validate(validateCheck(d, f)))[RuleEvidenceCited] {
			t.Errorf("%s: two turns years apart were admitted as close together", c.name)
		}
	}
}

func TestEvidenceRepeatRefusesOwnerTurnsWithoutTimes(t *testing.T) {
	f := Facts{OwnerLines: []OwnerLine{{Turn: "t1", Text: "first", OpensTurn: true}, {Turn: "t2", Text: "second", OpensTurn: true}}}
	d := validateDraft()
	d.Cite = Citation{Kind: "repeat", Turns: []string{"t1", "t2"}}
	if !validateReported(Validate(validateCheck(d, f)))[RuleEvidenceCited] {
		t.Error("two owner turns with no times were admitted as close together")
	}
}

func TestEvidenceKindMustBeOneOfTheFour(t *testing.T) {
	d := validateDraft()
	d.Cite = Citation{Kind: "assistant", Quote: validateQuote}
	if !validateReported(Validate(validateCheck(d, validateFacts(validateQuote))))[RuleEvidenceCited] {
		t.Error("a citation of assistant text was admitted")
	}
}

func TestAToolSkillRestsOnACallAndAMethodNeverOnOne(t *testing.T) {
	rule := Rule(aggregate.SkillRuleEvidenceKind)
	tool := validateDraft()
	tool.Kind = "tool"
	if !validateReported(Validate(validateCheck(tool, validateFacts(validateQuote))))[rule] {
		t.Error("a tool skill resting on the owner's words was admitted")
	}
	workingTool := validateDraft()
	workingTool.Kind, workingTool.Cite = "tool", Citation{Kind: "call", Call: "c1", Tool: "fetch_page"}
	if r := Validate(validateCheck(workingTool, Facts{Calls: []entity.ToolCall{validateCall(t, "c1", "ok", false)}})); len(r.Violations) != 0 {
		t.Errorf("a tool skill on a working call was refused: %+v", r.Violations)
	}
	method := validateDraft()
	method.Cite = Citation{Kind: "call", Call: "c1", Tool: "fetch_page"}
	if !validateReported(Validate(validateCheck(method, Facts{Calls: []entity.ToolCall{validateCall(t, "c1", "ok", false)}})))[rule] {
		t.Error("a method resting on a call was admitted")
	}
}

func TestValidateReportsEveryRuleADraftBreaks(t *testing.T) {
	d := validateDraft()
	d.Op = "archive"
	d.Kind = "preference"
	d.Name = "Release Notes"
	d.Description = "Write release notes " + strings.Repeat("x", 150)
	d.Body = "## Only one\nplain"
	d.Cite = Citation{Kind: "quote", Quote: "never said"}
	want := map[Rule]bool{
		RuleOp: true, RuleKind: true, RuleEvidenceCited: true,
		Rule(aggregate.SkillRuleNameClass):          true,
		Rule(aggregate.SkillRuleDescriptionLength):  true,
		Rule(aggregate.SkillRuleDescriptionTrigger): true,
		Rule(aggregate.SkillRuleBodyShape):          true,
	}
	got := validateReported(Validate(validateCheck(d, validateFacts(validateQuote))))
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

func TestAFifteenKBMethodDraftIsAdmitted(t *testing.T) {
	d := validateDraft()
	d.Body = strings.Repeat("## Step\n1. read the log, then group it by area\n", 320)
	if r := Validate(validateCheck(d, validateFacts(validateQuote))); len(r.Violations) != 0 {
		t.Fatalf("a 15 KB method was refused: %+v", r.Violations)
	}
}

func TestABodyDateIsNotedNotRejected(t *testing.T) {
	d := validateDraft()
	d.Body = "## Steps\n1. read the log, shipped 2026-11-26\n2. group by area"
	r := Validate(validateCheck(d, validateFacts(validateQuote)))
	if len(r.Violations) != 0 || len(r.Notes) != 1 {
		t.Errorf("violations %+v, notes %v; want no violation and one note", r.Violations, r.Notes)
	}
}

func TestABodyTicketIsNotedNotRejected(t *testing.T) {
	d := validateDraft()
	d.Body = "## Steps\n1. read the log, fixed in JIRA-12\n2. group by area"
	r := Validate(validateCheck(d, validateFacts(validateQuote)))
	if len(r.Violations) != 0 || len(r.Notes) != 1 {
		t.Errorf("violations %+v, notes %v; want no violation and one note", r.Violations, r.Notes)
	}
}

func TestOverlapNotesAtExactlyHalf(t *testing.T) {
	d := validateDraft()
	d.Description = "Use when debugging panic"
	live := LiveSkill{Root: enum.SkillRootGlobal, Name: "panic-logs", Description: "Use when debugging panic traces logs"}
	r := Validate(validateCheck(d, validateFacts(validateQuote), live))
	if validateReported(r)[RuleOverlap] || len(r.Notes) != 1 {
		t.Errorf("an overlap of exactly 0.5 gave violations %+v and notes %v; want a note only", r.Violations, r.Notes)
	}
}

func TestEvidenceRepeatAcceptsExactlyThirtyDays(t *testing.T) {
	f := Facts{OwnerLines: []OwnerLine{
		{Turn: "t1", At: validateTurnAt, Text: "first", OpensTurn: true},
		{Turn: "t2", At: validateTurnAt.AddDate(0, 0, 30), Text: "second", OpensTurn: true},
	}}
	d := validateDraft()
	d.Cite = Citation{Kind: "repeat", Turns: []string{"t1", "t2"}}
	if validateReported(Validate(validateCheck(d, f)))[RuleEvidenceCited] {
		t.Error("two owner turns exactly 30 days apart were refused")
	}
}

func TestGenericErrorWordsAreAValidClassName(t *testing.T) {
	d := validateDraft()
	d.Name = "error-handling"
	if validateReported(Validate(validateCheck(d, validateFacts(validateQuote))))[Rule(aggregate.SkillRuleNameIncident)] {
		t.Error("a class name about error handling was refused as an incident")
	}
}

func TestEvidenceQuoteCitingNoWordsIsRefused(t *testing.T) {
	d := validateDraft()
	d.Cite = Citation{Kind: "quote", Quote: "!!!"}
	r := Validate(validateCheck(d, validateFacts("please write release notes like the last one")))
	if !validateReported(r)[RuleEvidenceCited] {
		t.Errorf("a quote of no words: %+v; want evidence-cited", r.Violations)
	}
}

func TestPatchTargetUnknownReportsExactlyOneViolation(t *testing.T) {
	d := validateDraft()
	d.Op, d.Target = "patch", "missing"
	r := Validate(validateCheck(d, validateFacts(validateQuote), validateLearned))
	if len(r.Violations) != 1 || !validateReported(r)[RuleOpTarget] {
		t.Errorf("an unknown patch target: %+v; want only op-target", r.Violations)
	}
}

func TestPatchTargetIsTheLearnedRowWhenAGlobalSkillSharesItsName(t *testing.T) {
	global := LiveSkill{Root: enum.SkillRootGlobal, Name: "release-notes-old", Description: "Keep release notes short", Digest: "dg", Level: enum.SkillLevelAgent}
	learned := validateLearned
	d := validateDraft()
	d.Op, d.Target, d.Name = "patch", "release-notes-old", "release-notes-old"
	if r := Validate(validateCheck(d, validateFacts(validateQuote), global, learned)); len(r.Violations) != 0 {
		t.Errorf("a patch of the learned skill beside a same-named global one: %+v", r.Violations)
	}
}

func TestGlobalSkillSharingTheTargetsNameStillCountsForOverlap(t *testing.T) {
	learned := validateLearned
	global := LiveSkill{Root: enum.SkillRootGlobal, Name: "release-notes-old", Description: "Use when writing release notes from merged changes"}
	d := validateDraft()
	d.Op, d.Target, d.Name = "patch", "release-notes-old", "release-notes-old"
	r := Validate(validateCheck(d, validateFacts(validateQuote), learned, global))
	if !validateReported(r)[RuleOverlap] {
		t.Errorf("the global skill sharing the target's name was skipped: %+v", r.Violations)
	}
}

func TestJaccardCountsSharedWords(t *testing.T) {
	a := map[string]bool{"release": true, "notes": true}
	b := map[string]bool{"release": true, "notes": true, "merged": true, "changes": true}
	if got := jaccard(a, b); got != 0.5 {
		t.Errorf("jaccard = %v, want 0.5", got)
	}
	if got := jaccard(a, map[string]bool{}); got != 0 {
		t.Errorf("jaccard with an empty set = %v, want 0", got)
	}
}

func TestEvidenceQuoteCannotSpliceAcrossASentenceEnd(t *testing.T) {
	d := validateDraft()
	d.Cite = Citation{Kind: "quote", Quote: "never write release"}
	r := Validate(validateCheck(d, validateFacts("never write. release notes are bad")))
	if !validateReported(r)[RuleEvidenceCited] {
		t.Errorf("a quote spliced across a sentence end: %+v; want evidence-cited", r.Violations)
	}
	if r := Validate(validateCheck(d, validateFacts("never write release notes, ever"))); len(r.Violations) != 0 {
		t.Errorf("the same words inside one sentence: %+v", r.Violations)
	}
}

func TestProposalRequiresAnAdmittedValidation(t *testing.T) {
	if _, err := (Result{}).Proposal("sprop_test", validateTurnAt); err == nil {
		t.Error("a zero-value Result produced a proposal")
	}
	d := validateDraft()
	d.Name = "1badname"
	r := Validate(validateCheck(d, validateFacts(validateQuote)))
	if len(r.Violations) == 0 {
		t.Fatal("the fixture should carry a violation")
	}
	if _, err := r.Proposal("sprop_test", validateTurnAt); err == nil {
		t.Error("a Result with violations produced a proposal")
	}
}

func TestProposalAdmittedCarriesTheValidatedShape(t *testing.T) {
	r := Validate(validateCheck(validateDraft(), validateFacts(validateQuote)))
	if len(r.Violations) != 0 {
		t.Fatalf("the fixture should admit: %+v", r.Violations)
	}
	p, err := r.Proposal("sprop_test", validateTurnAt)
	if err != nil {
		t.Fatalf("an admitted Result: %v", err)
	}
	if p.Status() != enum.SkillProposalPending {
		t.Errorf("a new proposal is not pending: %s", p.Status())
	}
	if p.Name() != validateDraft().Name || p.Description() != validateDraft().Description {
		t.Errorf("the proposal does not carry the validated draft: %q %q", p.Name(), p.Description())
	}
	if p.EvidenceKind() != enum.SkillEvidenceQuote {
		t.Errorf("the proposal does not carry the proven evidence: %s", p.EvidenceKind())
	}
}

// A create over a name the loop still holds a row for is refused though its file
// is gone: accept refuses that create too, so the validator refuses it now.
func TestACreateOverAnOrphanRowIsRefused(t *testing.T) {
	c := validateCheck(validateDraft(), validateFacts(validateQuote))
	c.Owned = []string{"release-notes"}
	r := Validate(c)
	if !validateReported(r)[RuleOrphanRow] {
		t.Fatalf("a create over an orphan row was admitted: %+v", r.Violations)
	}
	if m := validateMessage(r, RuleOrphanRow); !strings.Contains(m, "still owned by the loop") || !strings.Contains(m, "no longer reads as a skill") {
		t.Errorf("the orphan message %q does not state the real state", m)
	}
}

// Under a live skill of the same name the refusal is name-taken, and only that.
// The orphan rule speaks for a name that is free on disk, and a row for another
// name refuses nothing.
func TestAnOrphanRowRefusesOnlyItsOwnFreeName(t *testing.T) {
	live := LiveSkill{Root: enum.SkillRootNusashell, Name: "release-notes", Description: "Keep release notes short", Digest: "d1", Level: enum.SkillLevelAgent}
	c := validateCheck(validateDraft(), validateFacts(validateQuote), live)
	c.Owned = []string{"release-notes"}
	if got := validateReported(Validate(c)); !got[RuleNameTaken] || got[RuleOrphanRow] {
		t.Errorf("a live name with a row reported %v; want name-taken and no orphan-row", got)
	}
	c = validateCheck(validateDraft(), validateFacts(validateQuote))
	c.Owned = []string{"deploy-day"}
	if r := Validate(c); len(r.Violations) != 0 {
		t.Errorf("a row for another name refused the create: %+v", r.Violations)
	}
}
