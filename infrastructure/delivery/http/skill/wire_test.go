package skill

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	reposkill "github.com/jahrulnr/nusashell/infrastructure/repository/skill"
)

const (
	wireLoop = "the loop wrote this skill's file, but the decision was not recorded"
	wireRow  = "the skill's file changed since its row was written"
)

// The skill row names the loop's leftover with the same sentence the proposal
// uses, and a hand edit with the row sentence. Dropping changedCause, or
// writing it only when true, fails: an intact row still carries the key.
func TestChangedCauseNamesTheLoopsUnrecordedWrite(t *testing.T) {
	ctx := context.Background()
	home, builtin := t.TempDir(), t.TempDir()
	nusashell := t.TempDir()
	r, db := skillsRouter(t, home, nusashell, builtin)
	old := writeSkillFile(t, nusashell, "release-notes", "Use when writing release notes", "## Steps\n1. read the log")
	oldDigest := valueobject.SkillDigest(old)
	row, err := aggregate.NewSkillOwnership(enum.SkillRootNusashell, "release-notes", enum.SkillLevelAgent, oldDigest)
	if err != nil {
		t.Fatal(err)
	}
	if err := reposkill.NewOwnership(db).Save(ctx, row, ""); err != nil {
		t.Fatal(err)
	}
	doc := repository.SkillDocument{
		Name: "release-notes", Description: "Use when writing the notes",
		Body: "## Steps\n1. read the log\n2. group by area",
	}
	catalog := reposkill.NewLocal(home, nusashell, builtin)
	if _, err := catalog.Write(ctx, enum.SkillRootNusashell, doc, oldDigest); err != nil {
		t.Fatal(err)
	}
	p, err := aggregate.NewSkillProposal("sprop_loop", aggregate.SkillProposalInput{
		Op: enum.SkillProposalPatch, Kind: enum.SkillKindMethod,
		Name: "release-notes", Description: doc.Description, Body: doc.Body,
		Evidence: "rewrite the steps", EvidenceKind: enum.SkillEvidenceQuote, Reason: "the steps drifted",
		TargetName: "release-notes", TargetDigest: oldDigest, TargetLevel: enum.SkillLevelAgent,
	}, time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if err := reposkill.NewProposals(db).Add(ctx, p); err != nil {
		t.Fatal(err)
	}

	got := rowByName(t, rowsOf(t, get(t, r, "/api/skills")), "release-notes")
	if got["changed"] != true || got["changedCause"] != wireLoop {
		t.Fatalf("the loop's leftover reads %v, want changed with %q", got["changedCause"], wireLoop)
	}
	if got["changed"] == true && got["changedCause"] == "" || got["changed"] == false && got["changedCause"] != "" {
		t.Fatal("changed and changedCause disagree")
	}
}

// The catalog-budget 409 carries the integer. An unset budget and every other
// 409 omit the key. A client reads overage, not the comma in the sentence.
func TestCatalogBudgetOverageIsAnInteger(t *testing.T) {
	ctx := context.Background()
	builtin := t.TempDir()
	const otherDesc = 1500
	writeSkillFile(t, builtin, "other", strings.Repeat("d", otherDesc), "## Steps\n1. one")
	const name = "notes"
	const desc = "Use when writing notes"
	const want = 1234
	total := len(name) + len(desc) + len("other") + otherDesc
	r, db := skillsRouterBudget(t, t.TempDir(), t.TempDir(), builtin, total-want)
	p, err := aggregate.NewSkillProposal("sprop_over", aggregate.SkillProposalInput{
		Op: enum.SkillProposalCreate, Kind: enum.SkillKindMethod,
		Name: name, Description: desc, Body: "## Steps\n1. write it down\n2. share it",
		Evidence: "start from the idea", EvidenceKind: enum.SkillEvidenceQuote, Reason: "the owner asked",
	}, time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if err := reposkill.NewProposals(db).Add(ctx, p); err != nil {
		t.Fatal(err)
	}
	rec := post(t, r, "/api/skill-proposals/sprop_over/decide", `{"action":"accept"}`)
	fields := problemFields(t, rec)
	if rec.Code != 409 || rawCode(t, fields, "code") != `"invalid_argument"` {
		t.Fatalf("over budget = %d %s", rec.Code, rec.Body)
	}
	if string(fields["overage"]) != "1234" {
		t.Fatalf("overage = %s, want the integer 1234", fields["overage"])
	}
	if !strings.Contains(string(fields["detail"]), "1,234") {
		t.Fatalf("detail = %s, want the comma sentence kept", fields["detail"])
	}
}

func TestUnsetBudgetAndOtherConflictsOmitOverage(t *testing.T) {
	ctx := context.Background()
	nusashell := t.TempDir()
	unset, db := skillsRouterBudget(t, t.TempDir(), nusashell, t.TempDir(), 0)
	add := func(id, name, desc string) {
		t.Helper()
		p, err := aggregate.NewSkillProposal(id, aggregate.SkillProposalInput{
			Op: enum.SkillProposalCreate, Kind: enum.SkillKindMethod,
			Name: name, Description: desc, Body: "## Steps\n1. write it down\n2. share it",
			Evidence: "start from the idea", EvidenceKind: enum.SkillEvidenceQuote, Reason: "the owner asked",
		}, time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC))
		if err != nil {
			t.Fatal(err)
		}
		if err := reposkill.NewProposals(db).Add(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	add("sprop_unset", "notes", "Use when writing notes")
	rec := post(t, unset, "/api/skill-proposals/sprop_unset/decide", `{"action":"accept"}`)
	fields := problemFields(t, rec)
	if rec.Code != 409 || !strings.Contains(string(fields["detail"]), "no catalog budget") {
		t.Fatalf("unset budget = %d %s", rec.Code, rec.Body)
	}
	if _, ok := fields["overage"]; ok {
		t.Fatalf("an unset budget wrote overage: %s", rec.Body)
	}

	taken, db2 := skillsRouter(t, t.TempDir(), nusashell, t.TempDir())
	writeSkillFile(t, nusashell, "release-notes", "Use when writing release notes", "## Steps\n1. read the log")
	p, err := aggregate.NewSkillProposal("sprop_taken", aggregate.SkillProposalInput{
		Op: enum.SkillProposalCreate, Kind: enum.SkillKindMethod,
		Name: "release-notes", Description: "Use when writing release notes",
		Body: "## Steps\n1. read the log\n2. group by area", Evidence: "write the release notes",
		EvidenceKind: enum.SkillEvidenceQuote, Reason: "a second copy",
	}, time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if err := reposkill.NewProposals(db2).Add(ctx, p); err != nil {
		t.Fatal(err)
	}
	rec = post(t, taken, "/api/skill-proposals/sprop_taken/decide", `{"action":"accept"}`)
	fields = problemFields(t, rec)
	if rec.Code != 409 {
		t.Fatalf("a taken name = %d %s, want 409", rec.Code, rec.Body)
	}
	if _, ok := fields["overage"]; ok {
		t.Fatalf("a taken-name 409 wrote overage: %s", rec.Body)
	}
}

func TestMissingSkillAndProposalStayNotFound(t *testing.T) {
	r, _ := skillsRouter(t, t.TempDir(), t.TempDir(), t.TempDir())
	rec := post(t, r, "/api/skills/ghost-skill/level", `{"level":"user","digest":"d1"}`)
	fields := problemFields(t, rec)
	if rec.Code != 404 || rawCode(t, fields, "code") != `"not_found"` {
		t.Fatalf("a missing skill = %d %s, want 404 not_found", rec.Code, rec.Body)
	}
	rec = post(t, r, "/api/skill-proposals/sprop_missing/decide", `{"action":"dismiss","reason":"no"}`)
	fields = problemFields(t, rec)
	if rec.Code != 404 || rawCode(t, fields, "code") != `"not_found"` {
		t.Fatalf("a missing proposal = %d %s, want 404 not_found", rec.Code, rec.Body)
	}
}

func problemFields(t *testing.T, rec *httptest.ResponseRecorder) map[string]json.RawMessage {
	t.Helper()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("the answer is not JSON: %v — %s", err, rec.Body)
	}
	return doc
}

func rawCode(t *testing.T, fields map[string]json.RawMessage, key string) string {
	t.Helper()
	raw, ok := fields[key]
	if !ok {
		t.Fatalf("problem has no %s: %v", key, fields)
	}
	return string(raw)
}
