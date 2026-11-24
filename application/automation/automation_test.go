package appautomation

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	appconversation "github.com/jahrulnr/nusashell/application/conversation"
	appjob "github.com/jahrulnr/nusashell/application/job"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	repoautomation "github.com/jahrulnr/nusashell/infrastructure/repository/automation"
	"github.com/jahrulnr/nusashell/infrastructure/repository/store"
	"github.com/jahrulnr/nusashell/migration"
)

// ptr points at v, for the optional Input.Enabled.
//
//go:fix inline
func ptr[T any](v T) *T { return new(v) }

type call struct {
	convID, provider, model, prompt string
	opts                            appjob.SendOptions
}

type fakeRunner struct {
	mu      sync.Mutex
	calls   []call
	err     error
	block   chan struct{} // when set, a run waits on it or on ctx
	started chan struct{} // a blocking run signals it began
}

func (r *fakeRunner) Run(ctx context.Context, convID, providerID, modelID, content string, _ []valueobject.Attachment, opts appjob.SendOptions, _ event.TurnEventHandler) (string, appconversation.SendResult, error) {
	r.mu.Lock()
	r.calls = append(r.calls, call{convID, providerID, modelID, content, opts})
	block, started, err := r.block, r.started, r.err
	r.mu.Unlock()
	if block != nil {
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-block:
			return convID, appconversation.SendResult{}, err
		case <-ctx.Done():
			return convID, appconversation.SendResult{}, ctx.Err()
		}
	}
	return convID, appconversation.SendResult{}, err
}

func (r *fakeRunner) snapshot() []call {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]call(nil), r.calls...)
}

type fakeRooms struct {
	mu      sync.Mutex
	known   map[string]bool
	titles  map[string]string
	infoErr map[string]error
}

func (r *fakeRooms) Info(_ context.Context, id string) (drepo.ConversationInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.infoErr[id]; err != nil {
		return drepo.ConversationInfo{}, err
	}
	if !r.known[id] {
		return drepo.ConversationInfo{}, apperror.NewNotFoundError("conversation "+id, nil)
	}
	return drepo.ConversationInfo{ID: id}, nil
}

func (r *fakeRooms) Annotate(_ context.Context, id string, a appconversation.Annotation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.known[id] = true
	if a.Title != nil {
		r.titles[id] = *a.Title
	}
	return nil
}

type fakeChats struct {
	mu   sync.Mutex
	sent []string
}

func (c *fakeChats) Send(_ context.Context, chat valueobject.ChatRef, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, chat.Key()+" "+text)
	return nil
}

func (c *fakeChats) SendFinal(_ context.Context, convID string, chat valueobject.ChatRef) error {
	return c.Send(context.Background(), chat, "final of "+convID)
}

type fakeAlerts struct {
	mu  sync.Mutex
	got []enum.RunStatus
}

func (f *fakeAlerts) AutomationRan(_ string, s enum.RunStatus, _ string) {
	f.mu.Lock()
	f.got = append(f.got, s)
	f.mu.Unlock()
}

func setup(t *testing.T) (*App, *fakeRunner, *fakeRooms, *fakeChats, *time.Time) {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "app.db"), migration.Files)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	runner := &fakeRunner{}
	rooms := &fakeRooms{known: map[string]bool{}, titles: map[string]string{}}
	chats := &fakeChats{}
	app := New(repoautomation.New(db), nil, runner, rooms, chats, &fakeAlerts{}, "UTC", nil)
	now := time.Date(2026, 11, 22, 8, 0, 0, 0, time.UTC)
	app.now = func() time.Time { return now }
	return app, runner, rooms, chats, &now
}

func TestScheduledRunsKeepTheirThread(t *testing.T) {
	app, runner, rooms, chats, now := setup(t)
	ctx := context.Background()
	v, err := app.Create(ctx, Input{Name: "Standup", Prompt: "What changed?", AgentID: "claude", Schedule: "cron", Cron: "0 9 * * *",
		Thread: "same", Notify: &Chat{Channel: "telegram", ChatID: "42"}, Enabled: new(true)})
	if err != nil {
		t.Fatal(err)
	}
	if v.NextRunAt == nil || !v.NextRunAt.Equal(time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("next = %v", v.NextRunAt)
	}
	app.Tick(ctx) // 08:00 — nothing due
	app.wg.Wait()
	if len(runner.snapshot()) != 0 {
		t.Fatal("ran before its time")
	}
	*now = time.Date(2026, 11, 22, 9, 0, 5, 0, time.UTC)
	app.Tick(ctx)
	app.Tick(ctx) // a second tick must not run it twice
	app.wg.Wait()
	calls := runner.snapshot()
	if len(calls) != 1 || calls[0].prompt != "What changed?" || calls[0].opts.Agent != "claude" {
		t.Fatalf("calls = %+v", calls)
	}
	if got := calls[0].opts.Origin; got.Kind != enum.TurnOriginAutomation || got.Name != "Standup" {
		t.Fatalf("origin = %+v, want the Standup automation", got)
	}
	first := calls[0].convID
	if rooms.titles[first] != "Automation · Standup" {
		t.Fatalf("title = %q", rooms.titles[first])
	}
	if len(chats.sent) != 1 || chats.sent[0] != "telegram:42: final of "+first {
		t.Fatalf("sent = %v", chats.sent)
	}
	*now = time.Date(2026, 11, 23, 9, 0, 1, 0, time.UTC)
	app.Tick(ctx)
	app.wg.Wait()
	if calls = runner.snapshot(); len(calls) != 2 || calls[1].convID != first {
		t.Fatalf("the second run must continue the kept thread: %+v", calls)
	}
	runs, err := app.Runs(ctx, v.ID)
	if err != nil || len(runs) != 2 || runs[0].Status != "completed" || runs[0].ConversationID != first {
		t.Fatalf("runs = %+v, %v", runs, err)
	}
}

func TestRunNowAndFailures(t *testing.T) {
	app, runner, _, _, now := setup(t)
	ctx := context.Background()
	v, err := app.Create(ctx, Input{Name: "Once", Prompt: "Hi", ProviderID: "openrouter", ModelID: "openrouter/auto", Schedule: "once",
		RunAt: now.Add(time.Hour), Thread: "new", Enabled: new(true)})
	if err != nil {
		t.Fatal(err)
	}
	runner.err = apperror.New(502, "upstream is down", nil)
	if _, err := app.RunNow(ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	app.wg.Wait()
	runs, _ := app.Runs(ctx, v.ID)
	if len(runs) != 1 || runs[0].Status != "failed" || runs[0].Note != "upstream is down" {
		t.Fatalf("runs = %+v", runs)
	}
	got, _ := app.Get(ctx, v.ID)
	if got.NextRunAt == nil {
		t.Fatal("Run now must not use up the schedule")
	}
	if c := runner.snapshot(); c[0].provider != "openrouter" || c[0].model != "openrouter/auto" || c[0].opts.Agent != "" {
		t.Fatalf("call = %+v", c[0])
	}
	if _, err := app.Update(ctx, v.ID, Input{Name: "x", Prompt: "y", AgentID: "a", Schedule: "once", RunAt: now.Add(time.Hour), Thread: "new"}, v.UpdatedAt.Add(-time.Second)); apperror.As(err).StatusCode() != 409 {
		t.Fatalf("a stale edit must be refused: %v", err)
	}
	if _, err := app.Create(ctx, Input{Name: "bad", Prompt: "y", AgentID: "a", Schedule: "cron", Cron: "every day", Thread: "new"}); apperror.As(err).StatusCode() != 400 {
		t.Fatalf("a bad cron must be 400: %v", err)
	}
	if err := app.TestNotify(ctx, v.ID); apperror.As(err).StatusCode() != 400 {
		t.Fatalf("no result chat: %v", err)
	}
}

func TestStopInterruptsAndRefusesNewRuns(t *testing.T) {
	app, runner, _, _, now := setup(t)
	ctx := context.Background()
	app.Start(ctx) // a.ctx becomes stoppable
	runner.block = make(chan struct{})
	runner.started = make(chan struct{}, 1)
	v, err := app.Create(ctx, Input{Name: "Blocked", Prompt: "Hi", ProviderID: "mock", ModelID: "m", Schedule: "cron",
		Cron: "0 9 * * *", Thread: "new", Enabled: new(true)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.RunNow(ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	<-runner.started
	app.Stop()
	runs, err := app.Runs(ctx, v.ID)
	if err != nil || len(runs) != 1 || runs[0].Status != "interrupted" {
		t.Fatalf("a run cut by shutdown must be interrupted: %+v, %v", runs, err)
	}
	if _, err := app.RunNow(ctx, v.ID); apperror.As(err).StatusCode() != 503 {
		t.Fatalf("RunNow after Stop must be 503: %v", err)
	}
	_ = now
}

func TestRunNowWhileRunningIsConflict(t *testing.T) {
	app, runner, _, _, _ := setup(t)
	ctx := context.Background()
	runner.block = make(chan struct{})
	runner.started = make(chan struct{}, 1)
	v, err := app.Create(ctx, Input{Name: "Blocked", Prompt: "Hi", AgentID: "fake", Schedule: "cron",
		Cron: "0 9 * * *", Thread: "new"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.RunNow(ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	<-runner.started
	if _, err := app.RunNow(ctx, v.ID); apperror.As(err).StatusCode() != 409 {
		t.Fatalf("a second run while one is going must be 409: %v", err)
	}
	runs, _ := app.Runs(ctx, v.ID)
	if len(runs) != 1 {
		t.Fatalf("runs = %+v", runs)
	}
	close(runner.block)
	app.wg.Wait()
}

func TestSameThreadRoomLookupFailure(t *testing.T) {
	app, runner, rooms, _, now := setup(t)
	ctx := context.Background()
	v, err := app.Create(ctx, Input{Name: "Standup", Prompt: "What changed?", AgentID: "fake", Schedule: "cron",
		Cron: "0 9 * * *", Thread: "same", Enabled: new(true)})
	if err != nil {
		t.Fatal(err)
	}
	// The first run mints and keeps the thread.
	if _, err := app.RunNow(ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	app.wg.Wait()
	got, _ := app.Get(ctx, v.ID)
	kept := got.ConversationID
	if kept == "" {
		t.Fatal("the run must keep its thread")
	}
	if !rooms.known[kept] {
		t.Fatal("the fake rooms must know the kept thread")
	}

	// A lookup failure that is not "gone" fails the run — it does not
	// open a new thread over a transient error.
	rooms.mu.Lock()
	rooms.infoErr = map[string]error{kept: apperror.New(503, "rooms are down", nil)}
	rooms.mu.Unlock()
	if _, err := app.RunNow(ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	app.wg.Wait()
	runs, _ := app.Runs(ctx, v.ID)
	var failed *RunView
	for i := range runs {
		if runs[i].Status == "failed" {
			failed = &runs[i]
		}
	}
	if len(runs) != 2 || failed == nil || failed.Note != "rooms are down" {
		t.Fatalf("runs = %+v", runs)
	}
	if calls := runner.snapshot(); len(calls) != 1 {
		t.Fatalf("a failed lookup must not start a turn: %+v", calls)
	}

	// A kept thread that is gone opens a new one.
	rooms.mu.Lock()
	rooms.infoErr = nil
	delete(rooms.known, kept)
	rooms.mu.Unlock()
	if _, err := app.RunNow(ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	app.wg.Wait()
	calls := runner.snapshot()
	if len(calls) != 2 || calls[1].convID == kept {
		t.Fatalf("a missing kept thread must open a new one: %+v", calls)
	}
	if got, _ := app.Get(ctx, v.ID); got.ConversationID != calls[1].convID {
		t.Fatalf("kept thread not updated: %+v", got)
	}
	_ = now
}

func TestCancelledAndMissingRoomRuns(t *testing.T) {
	app, runner, rooms, _, now := setup(t)
	ctx := context.Background()
	v, err := app.Create(ctx, Input{Name: "Once", Prompt: "Hi", ProviderID: "mock", ModelID: "m", Schedule: "once",
		RunAt: now.Add(time.Hour), Thread: "new"})
	if err != nil {
		t.Fatal(err)
	}
	// A turn canceled by its own cancel is recorded cancelled.
	runner.err = apperror.New(408, "turn canceled", nil)
	if _, err := app.RunNow(ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	app.wg.Wait()
	runs, _ := app.Runs(ctx, v.ID)
	if len(runs) != 1 || runs[0].Status != "cancelled" || runs[0].Note != "stopped" {
		t.Fatalf("runs = %+v", runs)
	}
	// A failing turn in a room that no longer exists leaves no link.
	for id := range rooms.known {
		delete(rooms.known, id)
	}
	runner.err = apperror.New(502, "upstream is down", nil)
	if _, err := app.RunNow(ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	app.wg.Wait()
	runs, _ = app.Runs(ctx, v.ID)
	var failed *RunView
	for i := range runs {
		if runs[i].Status == "failed" {
			failed = &runs[i]
		}
	}
	if len(runs) != 2 || failed == nil || failed.ConversationID != "" {
		t.Fatalf("a run in a deleted room must not link to it: %+v", runs)
	}
}

func TestUpdateOmittingEnabledKeepsTheState(t *testing.T) {
	app, _, _, _, _ := setup(t)
	ctx := context.Background()
	v, err := app.Create(ctx, Input{Name: "Hourly", Prompt: "Hi", AgentID: "fake", Schedule: "cron", Cron: "0 * * * *"})
	if err != nil {
		t.Fatal(err)
	}
	paused, err := app.SetEnabled(ctx, v.ID, false)
	if err != nil || paused.Enabled {
		t.Fatalf("pause: %+v %v", paused, err)
	}
	// A full edit that says nothing about enabled keeps it paused — it
	// must not silently resume the automation.
	in := Input{Name: "Hourly", Prompt: "Hi", AgentID: "fake", Schedule: "cron", Cron: "0 * * * *", Thread: "same"}
	got, err := app.Update(ctx, v.ID, in, paused.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled {
		t.Fatal("an edit without enabled must keep the automation paused")
	}
}
