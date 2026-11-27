package skill

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	reposkill "github.com/jahrulnr/nusashell/infrastructure/repository/skill"
)

// The queue is the door out. The list names each pending proposal and whether
// the owner can still accept it, by the same open rule the page labels with.
// Accept writes the skill. Dismiss without a reason is refused, because that
// reason is how the owner teaches the loop. A second decision is a 409.
func TestTheOwnerListsPendingProposalsAndDecidesThem(t *testing.T) {
	ctx := context.Background()
	nusashell := t.TempDir()
	r, db := skillsRouter(t, t.TempDir(), nusashell, t.TempDir())
	writeSkillFile(t, nusashell, "release-notes", "Use when writing release notes", "## Steps\n1. read the log")
	deploy := writeSkillFile(t, nusashell, "deploy-day", "Use when deploying", "## Steps\n1. check the calendar")
	row, err := aggregate.NewSkillOwnership(enum.SkillRootNusashell, "deploy-day", enum.SkillLevelAgent, valueobject.SkillDigest(deploy))
	if err != nil {
		t.Fatal(err)
	}
	if err := reposkill.NewOwnership(db).Save(ctx, row, ""); err != nil {
		t.Fatal(err)
	}
	props := reposkill.NewProposals(db)
	add := func(p *aggregate.SkillProposal) {
		t.Helper()
		if err := props.Add(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	fresh, err := aggregate.NewSkillProposal("sprop_open", aggregate.SkillProposalInput{
		Op: enum.SkillProposalCreate, Kind: enum.SkillKindMethod,
		Name: "fresh-idea", Description: "Use when starting a fresh idea", Tool: "Shell",
		Body: "## Steps\n1. write it down\n2. share it", Evidence: "start from the idea",
		EvidenceKind: enum.SkillEvidenceQuote, EvidenceAt: time.Date(2026, 11, 26, 9, 30, 0, 0, time.UTC),
		Reason: "the owner asked for this twice",
	}, time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	add(fresh)
	patch, err := aggregate.NewSkillProposal("sprop_patch", aggregate.SkillProposalInput{
		Op: enum.SkillProposalPatch, Kind: enum.SkillKindMethod,
		Name: "deploy-day", Description: "Use when deploying",
		Body: "## Steps\n1. check the calendar\n2. tell the room", Evidence: "start from the idea",
		EvidenceKind: enum.SkillEvidenceQuote, Reason: "the steps drifted",
		TargetName: "deploy-day", TargetDigest: valueobject.SkillDigest(deploy), TargetLevel: enum.SkillLevelAgent,
	}, time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	add(patch)
	taken, err := aggregate.NewSkillProposal("sprop_taken", aggregate.SkillProposalInput{
		Op: enum.SkillProposalCreate, Kind: enum.SkillKindMethod,
		Name: "release-notes", Description: "Use when writing release notes",
		Body: "## Steps\n1. read the log\n2. group by area", Evidence: "write release notes like the last one",
		EvidenceKind: enum.SkillEvidenceQuote, Reason: "a second copy",
	}, time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	add(taken)

	byID := map[string]map[string]any{}
	for _, row := range proposalRows(t, get(t, r, "/api/skill-proposals")) {
		byID[row["id"].(string)] = row
	}
	if len(byID) != 3 {
		t.Fatalf("queue = %v, want the three pending proposals", byID)
	}
	if byID["sprop_open"]["op"] != "create" || byID["sprop_open"]["name"] != "fresh-idea" || byID["sprop_open"]["reason"] != "the owner asked for this twice" || byID["sprop_open"]["open"] != true || byID["sprop_open"]["target"] != "" {
		t.Fatalf("open create = %v", byID["sprop_open"])
	}
	// The wire carries what the owner reads: the description, the tool, the
	// creation time, and the evidence with its own time when the turn had one.
	if byID["sprop_open"]["description"] != "Use when starting a fresh idea" || byID["sprop_open"]["tool"] != "Shell" || !sameInstant(byID["sprop_open"]["createdAt"], time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)) || byID["sprop_open"]["staleCause"] != "" {
		t.Fatalf("open create's wire fields = %v", byID["sprop_open"])
	}
	if ev, _ := byID["sprop_open"]["evidence"].(map[string]any); ev["kind"] != "quote" || ev["quote"] != "start from the idea" || !sameInstant(ev["date"], time.Date(2026, 11, 26, 9, 30, 0, 0, time.UTC)) {
		t.Fatalf("open create's evidence = %v, want kind quote with the turn's time", byID["sprop_open"]["evidence"])
	}
	if byID["sprop_patch"]["op"] != "patch" || byID["sprop_patch"]["kind"] != "method" || byID["sprop_patch"]["target"] != "deploy-day" || byID["sprop_patch"]["open"] != true {
		t.Fatalf("open patch = %v", byID["sprop_patch"])
	}
	if ev, _ := byID["sprop_patch"]["evidence"].(map[string]any); ev["date"] != nil {
		t.Fatalf("patch evidence without a turn time: date = %v, want null", ev["date"])
	}
	if byID["sprop_taken"]["open"] != false || byID["sprop_taken"]["staleCause"] == "" {
		t.Fatalf("taken create = %v, want open false with a stale cause: the name is already a skill", byID["sprop_taken"])
	}

	rec := post(t, r, "/api/skill-proposals/sprop_taken/decide", `{"action":"accept"}`)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "nusashell") {
		t.Fatalf("accepting a taken name answered %d: %s", rec.Code, rec.Body)
	}
	if _, err := props.Get(ctx, "sprop_taken"); err != nil {
		t.Fatal(err)
	}

	rec = post(t, r, "/api/skill-proposals/sprop_open/decide", `{"action":"accept","level":"user"}`)
	if rec.Code != 204 {
		t.Fatalf("accept answered %d: %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(filepath.Join(nusashell, "fresh-idea", "SKILL.md")); err != nil {
		t.Fatalf("accepted skill was not written: %v", err)
	}
	owned, err := reposkill.NewOwnership(db).Get(ctx, enum.SkillRootNusashell, "fresh-idea")
	if err != nil {
		t.Fatal(err)
	}
	if owned.Level() != enum.SkillLevelUser {
		t.Fatalf("level = %s, want user", owned.Level())
	}
	rec = post(t, r, "/api/skill-proposals/sprop_open/decide", `{"action":"accept"}`)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "already decided") {
		t.Fatalf("second accept answered %d: %s", rec.Code, rec.Body)
	}

	rec = post(t, r, "/api/skill-proposals/sprop_patch/decide", `{"action":"dismiss"}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "a dismissal says why") {
		t.Fatalf("dismiss without a reason answered %d: %s", rec.Code, rec.Body)
	}
	rec = post(t, r, "/api/skill-proposals/sprop_patch/decide", `{"action":"dismiss","reason":"  "}`)
	if rec.Code != 400 {
		t.Fatalf("dismiss with a blank reason answered %d: %s", rec.Code, rec.Body)
	}
	if got, err := props.Get(ctx, "sprop_patch"); err != nil || got.Status() != enum.SkillProposalPending {
		t.Fatalf("a refused dismiss changed the proposal: %v %v", got, err)
	}
	rec = post(t, r, "/api/skill-proposals/sprop_patch/decide", `{"action":"dismiss","reason":"the steps are fine"}`)
	if rec.Code != 204 {
		t.Fatalf("dismiss answered %d: %s", rec.Code, rec.Body)
	}

	rec = post(t, r, "/api/skill-proposals/sprop_taken/decide", `{"action":"archive"}`)
	if rec.Code != 400 {
		t.Fatalf("unknown action answered %d: %s", rec.Code, rec.Body)
	}
	left := proposalRows(t, get(t, r, "/api/skill-proposals"))
	if len(left) != 1 || left[0]["id"] != "sprop_taken" {
		t.Fatalf("queue after the decisions = %v, want only the taken name, still pending", left)
	}

	// A skill name is not a decision. The decide route sits under the queue's
	// own prefix, so POST /api/skills/<name>/decide has no route at all.
	rec = post(t, r, "/api/skills/fresh-idea/decide", `{"action":"dismiss","reason":"no"}`)
	if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "no such skill proposal") {
		t.Fatalf("deciding by skill name answered %d: %s", rec.Code, rec.Body)
	}
}

// sameInstant reports whether the wire time v is the instant want. The server
// writes times in its local offset, so the check is by instant, not by text.
func sameInstant(v any, want time.Time) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	at, err := time.Parse(time.RFC3339, s)
	return err == nil && at.Equal(want)
}

func proposalRows(t *testing.T, rec *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	if rec.Code != 200 {
		t.Fatalf("the queue answered %d: %s", rec.Code, rec.Body)
	}
	var doc struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("queue is not JSON: %v — %s", err, rec.Body)
	}
	if doc.Data == nil {
		t.Fatal("queue data is null, want a list")
	}
	return doc.Data
}
