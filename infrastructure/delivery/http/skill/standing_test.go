package skill

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	reposkill "github.com/jahrulnr/nusashell/infrastructure/repository/skill"
)

// A learned skill the owner edited by hand reads as the owner's on the page and
// is flagged changed, and reading the page writes nothing: the row it found is
// the row it leaves. The pending count covers every proposal, not the rows' sums.
func TestSkillsPageReportsAHandEditWithoutWriting(t *testing.T) {
	ctx := context.Background()
	nusashell := t.TempDir()
	r, db := skillsRouter(t, t.TempDir(), nusashell, t.TempDir())
	release := writeSkillFile(t, nusashell, "release-notes", "Use when writing release-notes", "## Steps\n1. read the log")
	deploy := writeSkillFile(t, nusashell, "deploy-day", "Use when deploying", "## Steps\n1. check the calendar")
	for name, content := range map[string]string{"release-notes": release, "deploy-day": deploy} {
		row, err := aggregate.NewSkillOwnership(enum.SkillRootNusashell, name, enum.SkillLevelAgent, valueobject.SkillDigest(content))
		if err != nil {
			t.Fatal(err)
		}
		if err := reposkill.NewOwnership(db).Save(ctx, row, ""); err != nil {
			t.Fatal(err)
		}
	}
	// The owner edits release-notes by hand: the bytes change, the row does not.
	writeSkillFile(t, nusashell, "release-notes", "Use when writing release-notes", "## Steps\n1. read the log\n2. the owner's own step")

	// One proposal names a skill that is not live: it still waits on the owner.
	p, err := aggregate.NewSkillProposal("sprop_pending", aggregate.SkillProposalInput{
		Op: enum.SkillProposalCreate, Kind: enum.SkillKindMethod,
		Name: "fresh-idea", Description: "Use when starting a fresh idea",
		Body: "## Steps\n1. write it down\n2. share it", Evidence: "start from the idea",
		EvidenceKind: enum.SkillEvidenceQuote,
	}, time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if err := reposkill.NewProposals(db).Add(ctx, p); err != nil {
		t.Fatal(err)
	}

	before, err := reposkill.NewOwnership(db).Get(ctx, enum.SkillRootNusashell, "release-notes")
	if err != nil {
		t.Fatal(err)
	}
	rec := get(t, r, "/api/skills")
	rows := rowsOf(t, rec)
	edited := rowByName(t, rows, "release-notes")
	if edited["level"] != "user" || edited["changed"] != true || edited["tracked"] != true {
		t.Errorf("an edited agent skill reads %v, want the owner's level, changed and tracked", edited)
	}
	intact := rowByName(t, rows, "deploy-day")
	if intact["level"] != "agent" || intact["changed"] != false || intact["tracked"] != true {
		t.Errorf("an intact agent skill reads %v, want agent, unchanged and tracked", intact)
	}
	// The page's counts are keys of their own: a count that is absent would read
	// as zero, so the pointer says whether the key came at all.
	var page struct {
		Pending      int  `json:"pending"`
		PendingStale *int `json:"pendingStale"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("the page is not JSON: %v", err)
	}
	if page.Pending != 1 {
		t.Errorf("pending = %d, want the one proposal the owner decides", page.Pending)
	}
	// The one proposal names a skill that is not live: nothing it targets has
	// moved on, so it is open and no stale label is set on it.
	if page.PendingStale == nil || *page.PendingStale != 0 {
		t.Errorf("pendingStale = %v, want the key present and 0 for an open proposal", page.PendingStale)
	}
	if _, ok := edited["pendingStale"]; !ok {
		t.Errorf("the row for release-notes carries no pendingStale key: %v", edited)
	}
	get(t, r, "/api/skills") // a second read must change nothing either
	after, err := reposkill.NewOwnership(db).Get(ctx, enum.SkillRootNusashell, "release-notes")
	if err != nil {
		t.Fatal(err)
	}
	if after.Level() != before.Level() || after.Digest() != before.Digest() || after.Level() != enum.SkillLevelAgent {
		t.Errorf("the page wrote the row: %s at %s, was %s at %s", after.Level(), after.Digest(), before.Level(), before.Digest())
	}
}
