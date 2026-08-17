package ci

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"nusashell/application"
	"nusashell/domain"
)

func TestSQLiteOnceTriggerSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	clock := &application.FrozenClock{T: time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)}
	bus := application.NewBus()

	svc, store, err := BuildAutomation(dir, bus, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	svc.Clock = clock
	svc.Auto.Clock = clock
	svc.Exec.Clock = clock
	at := clock.T.Add(time.Hour)
	w := &domain.WorkflowDefinition{
		ID: "once-mail", Name: "once-mail", Enabled: true,
		Triggers: []domain.Trigger{{ID: "t1", Kind: domain.TriggerOnce, At: &at, Timezone: "UTC"}},
		Jobs:     []domain.Job{{ID: "j", Steps: []domain.Step{{ID: "s", Run: "true"}}}},
	}
	if _, _, err := svc.SaveWorkflow(ctx, w); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	svc2, store2, err := BuildAutomation(dir, bus, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store2.Close()
	svc2.Clock = clock
	svc2.Auto.Clock = clock
	svc2.Exec.Clock = clock
	if _, err := os.Stat(filepath.Join(dir, "automation.db")); err != nil {
		t.Fatal(err)
	}
	clock.Advance(2 * time.Hour)
	if err := svc2.Auto.FireDue(ctx); err != nil {
		t.Fatal(err)
	}
	runs, err := svc2.Runs.List(ctx, application.RunFilter{WorkflowID: "once-mail"})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("want 1 run after reopen, got %d", len(runs))
	}
	clock.Advance(time.Hour)
	if err := svc2.Auto.FireDue(ctx); err != nil {
		t.Fatal(err)
	}
	runs, _ = svc2.Runs.List(ctx, application.RunFilter{WorkflowID: "once-mail"})
	if len(runs) != 1 {
		t.Fatalf("once trigger re-fired, got %d runs", len(runs))
	}
}
