package repoautomation

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	"github.com/jahrulnr/nusashell/infrastructure/repository/store"
	"github.com/jahrulnr/nusashell/migration"
)

var t0 = time.Date(2026, 11, 22, 8, 0, 0, 0, time.UTC)

func setup(t *testing.T) (*Repository, context.Context) {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "app.db"), migration.Files)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return New(db), context.Background()
}

// auto builds an enabled every-minute cron automation.
func auto(t *testing.T, name string, at time.Time) *aggregate.Automation {
	t.Helper()
	a, err := aggregate.NewAutomation("", name, "prompt", at)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SetRuntime("", "prov", "model"); err != nil {
		t.Fatal(err)
	}
	s, err := valueobject.NewCronSchedule("* * * * *", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SetSchedule(s, at); err != nil {
		t.Fatal(err)
	}
	return a
}

func run(t *testing.T, automationID string, at time.Time) *entity.AutomationRun {
	t.Helper()
	r, err := entity.NewAutomationRun(automationID, at, at)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func claim(t *testing.T, repo *Repository, ctx context.Context, a *aggregate.Automation, at time.Time) bool {
	t.Helper()
	a.Claim(at)
	ok, err := repo.Claim(ctx, a, run(t, a.ID(), at))
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func TestCreateGetList(t *testing.T) {
	repo, ctx := setup(t)
	a := auto(t, "Hourly", t0)
	if err := repo.Create(ctx, a); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, a.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got.Name() != "Hourly" || !got.Enabled() || got.Version() != 0 || !got.UpdatedAt().Equal(t0) {
		t.Fatalf("got %+v", got)
	}
	list, err := repo.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v, %v", list, err)
	}
	if err := repo.Create(ctx, a); apperror.As(err).StatusCode() != 409 {
		t.Fatalf("a duplicate id must be 409: %v", err)
	}
	if _, err := repo.Get(ctx, "au_missing"); apperror.As(err).StatusCode() != 404 {
		t.Fatalf("missing must be 404: %v", err)
	}
}

func TestOwnerEditSurvivesAClaim(t *testing.T) {
	repo, ctx := setup(t)
	a := auto(t, "Hourly", t0)
	if err := repo.Create(ctx, a); err != nil {
		t.Fatal(err)
	}
	// The edit form reads the row at t0; a firing claims it meanwhile.
	form, _ := repo.Get(ctx, a.ID())
	readAt := form.UpdatedAt()
	due, err := repo.Due(ctx, t0.Add(time.Minute))
	if err != nil || len(due) != 1 {
		t.Fatalf("due = %v, %v", due, err)
	}
	if !claim(t, repo, ctx, &due[0], t0.Add(time.Minute)) {
		t.Fatal("the claim should land")
	}
	// The claim bumped the version but not updated_at: a save refetched
	// on the client's readAt succeeds — the firing did not stale the form.
	fresh, err := repo.Get(ctx, a.ID())
	if err != nil {
		t.Fatal(err)
	}
	if !fresh.UpdatedAt().Equal(readAt) || fresh.Version() != 1 {
		t.Fatalf("claim must not look like an edit: updated=%v version=%d", fresh.UpdatedAt(), fresh.Version())
	}
	if err := fresh.Describe("Renamed", "prompt"); err != nil {
		t.Fatal(err)
	}
	fresh.Touch(t0.Add(2 * time.Minute))
	if err := repo.Update(ctx, fresh, readAt); err != nil {
		t.Fatalf("a save after a claim must succeed: %v", err)
	}
	got, _ := repo.Get(ctx, a.ID())
	if got.Name() != "Renamed" || got.Version() != 2 {
		t.Fatalf("got name=%q version=%d", got.Name(), got.Version())
	}
}

func TestStaleWriteLosesOnVersion(t *testing.T) {
	repo, ctx := setup(t)
	a := auto(t, "Hourly", t0)
	if err := repo.Create(ctx, a); err != nil {
		t.Fatal(err)
	}
	edit, _ := repo.Get(ctx, a.ID())
	firing, _ := repo.Get(ctx, a.ID())

	// The claim wins; an edit holding the older version loses even though
	// updated_at still matches its read.
	if !claim(t, repo, ctx, firing, t0.Add(time.Minute)) {
		t.Fatal("the claim should land")
	}
	readAt := edit.UpdatedAt()
	edit.Touch(t0.Add(time.Minute))
	if err := repo.Update(ctx, edit, readAt); apperror.As(err).StatusCode() != 409 {
		t.Fatalf("a stale-version edit must lose: %v", err)
	}

	// And the reverse: the edit wins, a claim based on the older version
	// writes nothing. The first run is ended so only the version guard
	// can refuse this claim.
	stored, _ := repo.Runs(ctx, a.ID(), 1)
	stored[0].Finish(enum.RunCompleted, "", t0.Add(90*time.Second))
	if err := repo.SaveRun(ctx, &stored[0]); err != nil {
		t.Fatal(err)
	}
	edit2, _ := repo.Get(ctx, a.ID())
	firing2, _ := repo.Get(ctx, a.ID())
	readAt = edit2.UpdatedAt()
	edit2.Touch(t0.Add(2 * time.Minute))
	if err := repo.Update(ctx, edit2, readAt); err != nil {
		t.Fatal(err)
	}
	firing2.Claim(t0.Add(2 * time.Minute))
	ok, err := repo.Claim(ctx, firing2, run(t, a.ID(), t0.Add(2*time.Minute)))
	if err != nil || ok {
		t.Fatalf("a stale-version claim must write nothing: ok=%v err=%v", ok, err)
	}
	runs, _ := repo.Runs(ctx, a.ID(), 10)
	if len(runs) != 1 { // only the first claim's run
		t.Fatalf("runs = %d", len(runs))
	}
}

func TestUpdateNotFound(t *testing.T) {
	repo, ctx := setup(t)
	a := auto(t, "Ghost", t0)
	a.Touch(t0)
	if err := repo.Update(ctx, a, t0); apperror.As(err).StatusCode() != 404 {
		t.Fatalf("updating a missing automation must be 404: %v", err)
	}
}

func TestDueExcludesDisabledExpiredAndRunning(t *testing.T) {
	repo, ctx := setup(t)
	at := t0.Add(time.Minute) // every-minute crons are due here
	seed := func(name string) *aggregate.Automation {
		a := auto(t, name, t0)
		if err := repo.Create(ctx, a); err != nil {
			t.Fatal(err)
		}
		return a
	}
	live := seed("Live")
	paused := seed("Paused")
	paused.SetEnabled(false, t0)
	if err := repo.Update(ctx, paused, t0); err != nil {
		t.Fatal(err)
	}
	expiring := seed("Expiring")
	if err := expiring.SetExpiry(t0.Add(30*time.Second), t0); err != nil {
		t.Fatal(err)
	}
	if err := repo.Update(ctx, expiring, t0); err != nil {
		t.Fatal(err)
	}
	running := seed("Running")
	if ok, err := repo.StartRun(ctx, run(t, running.ID(), t0)); err != nil || !ok {
		t.Fatalf("startrun: %v %v", ok, err)
	}

	due, err := repo.Due(ctx, at)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].ID() != live.ID() {
		names := []string{}
		for _, d := range due {
			names = append(names, d.Name())
		}
		t.Fatalf("due = %v", names)
	}

	// Once the run ends the automation is due again — the missed firing
	// stays due and collapses into one run.
	stored, _ := repo.Runs(ctx, running.ID(), 1)
	stored[0].Finish(enum.RunCompleted, "", at)
	if err := repo.SaveRun(ctx, &stored[0]); err != nil {
		t.Fatal(err)
	}
	due, err = repo.Due(ctx, at.Add(time.Second))
	if err != nil || len(due) != 2 {
		t.Fatalf("due after finish = %v, %v", due, err)
	}
}

func TestStartRunRefusesWhileRunning(t *testing.T) {
	repo, ctx := setup(t)
	a := auto(t, "Hourly", t0)
	if err := repo.Create(ctx, a); err != nil {
		t.Fatal(err)
	}
	ok, err := repo.StartRun(ctx, run(t, a.ID(), t0))
	if err != nil || !ok {
		t.Fatalf("first run must start: %v %v", ok, err)
	}
	ok, err = repo.StartRun(ctx, run(t, a.ID(), t0.Add(time.Second)))
	if err != nil || ok {
		t.Fatalf("a second run while one is going must be refused: %v %v", ok, err)
	}
	runs, _ := repo.Runs(ctx, a.ID(), 10)
	if len(runs) != 1 {
		t.Fatalf("runs = %d", len(runs))
	}
	stored, _ := repo.Runs(ctx, a.ID(), 1)
	stored[0].Finish(enum.RunCompleted, "", t0.Add(time.Minute))
	if err := repo.SaveRun(ctx, &stored[0]); err != nil {
		t.Fatal(err)
	}
	ok, err = repo.StartRun(ctx, run(t, a.ID(), t0.Add(2*time.Minute)))
	if err != nil || !ok {
		t.Fatalf("after the run ends a new one must start: %v %v", ok, err)
	}
}

func TestClaimRefusesWhileRunning(t *testing.T) {
	repo, ctx := setup(t)
	a := auto(t, "Hourly", t0)
	if err := repo.Create(ctx, a); err != nil {
		t.Fatal(err)
	}
	at := t0.Add(time.Minute)
	due, _ := repo.Due(ctx, at)
	if !claim(t, repo, ctx, &due[0], at) {
		t.Fatal("the claim should land")
	}
	// The first run is still going: a second claim writes nothing and
	// reports false — the firing stays due for after the run.
	later, _ := repo.Get(ctx, a.ID())
	later.Claim(at.Add(2 * time.Minute))
	ok, err := repo.Claim(ctx, later, run(t, a.ID(), at.Add(time.Minute)))
	if err != nil || ok {
		t.Fatalf("claim while running must be refused: %v %v", ok, err)
	}
	runs, _ := repo.Runs(ctx, a.ID(), 10)
	if len(runs) != 1 {
		t.Fatalf("runs = %d", len(runs))
	}
	got, _ := repo.Get(ctx, a.ID())
	if want := t0.Add(2 * time.Minute); !got.NextRunAt().Equal(want) {
		t.Fatalf("a refused claim must leave the firing due: next=%v want %v", got.NextRunAt(), want)
	}
}

func TestKeepConversation(t *testing.T) {
	repo, ctx := setup(t)
	a := auto(t, "Hourly", t0)
	if err := repo.Create(ctx, a); err != nil {
		t.Fatal(err)
	}
	ok, err := repo.KeepConversation(ctx, a.ID(), "c_1", a.Version())
	if err != nil || !ok {
		t.Fatalf("keep: %v %v", ok, err)
	}
	got, _ := repo.Get(ctx, a.ID())
	if got.ConversationID() != "c_1" || !got.UpdatedAt().Equal(t0) {
		t.Fatalf("keeping a thread must not be an edit: %q updated=%v", got.ConversationID(), got.UpdatedAt())
	}
	// A write based on a stale version loses — the edit's thread wins.
	ok, err = repo.KeepConversation(ctx, a.ID(), "c_2", a.Version())
	if err != nil || ok {
		t.Fatalf("a stale keep must lose: %v %v", ok, err)
	}
	if got, _ := repo.Get(ctx, a.ID()); got.ConversationID() != "c_1" {
		t.Fatalf("conv = %q", got.ConversationID())
	}
}

func TestPruneRuns(t *testing.T) {
	repo, ctx := setup(t)
	a := auto(t, "Hourly", t0)
	if err := repo.Create(ctx, a); err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		r := run(t, a.ID(), t0.Add(time.Duration(i)*time.Minute))
		r.Finish(enum.RunCompleted, "", t0.Add(time.Duration(i)*time.Minute+time.Second))
		if err := repo.SaveRun(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.PruneRuns(ctx, a.ID(), 2); err != nil {
		t.Fatal(err)
	}
	runs, err := repo.Runs(ctx, a.ID(), 10)
	if err != nil || len(runs) != 2 {
		t.Fatalf("runs = %d, %v", len(runs), err)
	}
	if !runs[0].StartedAt().Equal(t0.Add(4 * time.Minute)) {
		t.Fatalf("the newest runs must be kept: %v", runs[0].StartedAt())
	}
}

func TestDeleteCascadesRuns(t *testing.T) {
	repo, ctx := setup(t)
	a := auto(t, "Hourly", t0)
	if err := repo.Create(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, run(t, a.ID(), t0)); err != nil {
		t.Fatal(err)
	}
	if err := repo.Delete(ctx, a.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Get(ctx, a.ID()); apperror.As(err).StatusCode() != 404 {
		t.Fatalf("deleted must be 404: %v", err)
	}
	runs, err := repo.Runs(ctx, a.ID(), 10)
	if err != nil || len(runs) != 0 {
		t.Fatalf("runs after delete = %v, %v", runs, err)
	}
	if err := repo.Delete(ctx, a.ID()); apperror.As(err).StatusCode() != 404 {
		t.Fatalf("deleting twice must be 404: %v", err)
	}
}

func TestInterruptRuns(t *testing.T) {
	repo, ctx := setup(t)
	a := auto(t, "Hourly", t0)
	if err := repo.Create(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, run(t, a.ID(), t0)); err != nil {
		t.Fatal(err)
	}
	done := run(t, a.ID(), t0)
	done.Finish(enum.RunCompleted, "", t0.Add(time.Minute))
	if err := repo.SaveRun(ctx, done); err != nil {
		t.Fatal(err)
	}
	at := t0.Add(2 * time.Minute)
	if err := repo.InterruptRuns(ctx, at); err != nil {
		t.Fatal(err)
	}
	runs, err := repo.Runs(ctx, a.ID(), 10)
	if err != nil {
		t.Fatal(err)
	}
	var interrupted, completed int
	for _, r := range runs {
		switch r.Status() {
		case enum.RunInterrupted:
			interrupted++
			if !r.EndedAt().Equal(at) || r.Note() == "" {
				t.Fatalf("interrupted run = %+v", r)
			}
		case enum.RunCompleted:
			completed++
		}
	}
	if interrupted != 1 || completed != 1 {
		t.Fatalf("runs = %+v", runs)
	}
}
