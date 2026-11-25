package appconversation

import (
	"context"
	"sync"
	"testing"
	"time"

	appjob "github.com/jahrulnr/nusashell/application/job"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
)

// A plain job that holds the room's worker without claiming it — what a queued
// turn waits behind.
type holdingJob struct {
	appjob.Base
	started chan struct{}
	free    chan struct{}
	once    sync.Once
}

func newHoldingJob() *holdingJob {
	j := &holdingJob{started: make(chan struct{}), free: make(chan struct{})}
	j.Result = make(chan error, 1)
	return j
}

func (j *holdingJob) Execute(context.Context) error {
	j.once.Do(func() { close(j.started) })
	<-j.free
	return nil
}

func (j *holdingJob) Interrupt() {}

// Work executing in the room is visible; work that finished is not.
func TestTurnActiveFollowsTheExecution(t *testing.T) {
	app, rt, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
	ctx := t.Context()
	if app.TurnActive("c_work") {
		t.Fatal("a room with nothing running reports work")
	}
	release := busyTurn(t, app, rt, "c_work")
	if !app.TurnActive("c_work") {
		t.Fatal("a running turn is not reported as work")
	}
	release()
	waitIdle(t, app, "c_work")
	if app.TurnActive("c_work") {
		t.Fatal("a finished turn still reports work")
	}
	_ = ctx
}

// A job waiting in the queue has not started: the room must not claim to be
// working, or a surface says "Working" to a room that is doing nothing.
func TestTurnActiveIgnoresAQueuedJob(t *testing.T) {
	app, _, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
	ctx := t.Context()

	hold := newHoldingJob()
	app.post("c_queue", hold)
	select {
	case <-hold.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the holding job never started")
	}
	// A turn queued behind it waits; nothing is executing but the holding job,
	// which claims nothing.
	sent := make(chan error, 1)
	go func() {
		_, err := app.Send(ctx, "c_queue", "", "", "later", nil, appjob.SendOptions{Agent: "fake"}, nil)
		sent <- err
	}()
	// The claim is about a job that is really waiting: wait for the queue to
	// fill, or this assertion would pass because nothing had been posted yet.
	waitFor(t, "the queued turn reaching the queue", func() bool {
		return app.QueueDepth("c_queue") == 1
	})
	if app.TurnActive("c_queue") {
		t.Fatal("a queued turn reported the room as working before it started")
	}
	close(hold.free)
	select {
	case err := <-sent:
		if err != nil {
			t.Fatalf("the queued turn failed: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the queued turn never ran after the worker was freed")
	}
	if app.TurnActive("c_queue") {
		t.Fatal("the finished turn still reports work")
	}
}

// A fold is work with no stream: the room is busy, even though no turn can be
// watched or stopped.
func TestTurnActiveWhileAFoldRuns(t *testing.T) {
	svc := &blockingCompactSvc{compactSvc: &compactSvc{}, entered: make(chan struct{}), free: make(chan struct{})}
	app, _ := compactApp(t, svc, 0)
	ctx := t.Context()
	for _, content := range []string{"first", "second"} {
		if _, err := app.Send(ctx, "room", "gw1", "m1", content, nil, appjob.SendOptions{}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if app.TurnActive("room") {
		t.Fatal("an idle room reports work")
	}
	folded := make(chan error, 1)
	go func() { folded <- app.Compact(ctx, "room") }()
	select {
	case <-svc.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the fold never reached the provider")
	}
	if !app.TurnActive("room") {
		t.Fatal("a running fold is not reported as work")
	}
	close(svc.free)
	select {
	case err := <-folded:
		if err != nil {
			t.Fatalf("the fold failed: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the fold never finished")
	}
	if app.TurnActive("room") {
		t.Fatal("the finished fold still reports work")
	}
}
