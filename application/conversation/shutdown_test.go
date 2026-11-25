package appconversation

import (
	"context"
	"strings"
	"testing"
	"time"

	appjob "github.com/jahrulnr/nusashell/application/job"
	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	"github.com/jahrulnr/nusashell/infrastructure/worker"
)

// wedgeJob runs until released and ignores its context — the worker that
// never comes back, which the shutdown must abandon instead of waiting for.
type wedgeJob struct {
	appjob.Base
	started chan struct{}
	release chan struct{}
	once    bool
}

func newWedgeJob() *wedgeJob {
	j := &wedgeJob{started: make(chan struct{}), release: make(chan struct{})}
	j.Result = make(chan error, 1)
	return j
}

func (j *wedgeJob) Execute(context.Context) error {
	if !j.once {
		j.once = true
		close(j.started)
	}
	<-j.release
	return nil
}

func (j *wedgeJob) Interrupt() {}

// A worker that will not stop is abandoned and named instead of holding the
// process: its room keeps the last persisted step, and the shutdown returns
// inside its grace.
func TestShutdownAbandonsAWedgedWorker(t *testing.T) {
	app, _ := fixture(t, okHandler)
	wedge := newWedgeJob()
	t.Cleanup(func() { close(wedge.release) })
	app.post("wedge-room", wedge)
	select {
	case <-wedge.started:
	case <-time.After(2 * time.Second):
		t.Fatal("the wedged job never started")
	}

	app.stopGrace = 50 * time.Millisecond
	started := time.Now()
	if err := app.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("shutdown waited %v for a wedged worker, want the grace", elapsed)
	}

	// Admission stays closed: the process is on its way out.
	if _, err := app.Send(t.Context(), "wedge-room", "gw1", "m1", "after", nil, appjob.SendOptions{}, nil); err == nil {
		t.Fatal("a post after shutdown must be refused")
	}
}

// A queued job is refused with 503 when the shutdown drains the queue — phase
// 1 behaviour; persisting its user line is the later phase's job.
func TestQueuedJobAnswers503OnShutdown(t *testing.T) {
	app, _ := fixture(t, okHandler)
	running := newWedgeJob()
	t.Cleanup(func() { close(running.release) })
	app.post("queued-room", running)
	select {
	case <-running.started:
	case <-time.After(2 * time.Second):
		t.Fatal("the running job never started")
	}

	queued := newWedgeJob()
	app.post("queued-room", queued)
	app.stopGrace = 100 * time.Millisecond

	done := make(chan error, 1)
	go func() { done <- app.Shutdown(context.Background()) }()

	select {
	case err := <-queued.Result:
		if err == nil || apperror.As(err).StatusCode() != 503 {
			t.Fatalf("queued job answered %v, want 503", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the queued job was never answered")
	}
	if err := <-done; err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

// CloseAdmission is the barrier's first phase: it refuses new work while the
// running one keeps going, so nothing starts once shutdown does.
func TestCloseAdmissionRefusesNewWorkAndKeepsTheRunningOne(t *testing.T) {
	app, _ := fixture(t, okHandler)
	running := newWedgeJob()
	t.Cleanup(func() { close(running.release) })
	app.post("admission-room", running)
	select {
	case <-running.started:
	case <-time.After(2 * time.Second):
		t.Fatal("the running job never started")
	}

	app.CloseAdmission()
	if _, err := app.Send(t.Context(), "admission-room", "gw1", "m1", "after", nil, appjob.SendOptions{}, nil); err == nil {
		t.Fatal("a post after CloseAdmission must be refused")
	}
	select {
	case <-running.release:
		t.Fatal("unreachable")
	default:
	}
	// The running job is untouched by admission closing.
	select {
	case <-running.started:
	default:
		t.Fatal("the running job was disturbed by closing admission")
	}
}

// TerminateChildren is the writers phase: the shell sessions and agent
// runtimes die there, not with the turn.
func TestTerminateChildrenStopsShellsAndAgents(t *testing.T) {
	app, _ := fixture(t, okHandler)
	shells := &fakeShellStore{}
	agents := &fakeAgentRuntime{}
	app.deps.Terminals = shells
	app.deps.AgentRuntime = agents

	if err := app.TerminateChildren(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !shells.terminated {
		t.Fatal("shell sessions were not terminated")
	}
	if !agents.shutdown {
		t.Fatal("agent runtime was not shut down")
	}
}

// fakeShellStore records TerminateAll without spawning anything.
type fakeShellStore struct {
	dservice.TerminalSessions
	terminated bool
}

func (s *fakeShellStore) TerminateAll() { s.terminated = true }

// fakeAgentRuntime records Shutdown.
type fakeAgentRuntime struct {
	dservice.AgentRuntime
	shutdown bool
}

func (a *fakeAgentRuntime) Shutdown(context.Context) error {
	a.shutdown = true
	return nil
}

var _ worker.Job = (*wedgeJob)(nil)

// Three accepted messages become three turns in arrival order: the worker's
// FIFO is the queue, so nothing is merged and no second turn runs beside the
// live one.
func TestQueuedSendsRunAsTurnsInArrivalOrder(t *testing.T) {
	app, _ := fixture(t, okHandler)
	running := newWedgeJob()
	released := false
	release := func() {
		if !released {
			released = true
			close(running.release)
		}
	}
	t.Cleanup(release)
	app.post("queue-room", running)
	select {
	case <-running.started:
	case <-time.After(2 * time.Second):
		t.Fatal("the blocking job never started")
	}

	for i, text := range []string{"one", "two", "three"} {
		position, err := app.Enqueue(t.Context(), "queue-room", "gw1", "m1", text, appjob.SendOptions{})
		if err != nil {
			t.Fatalf("enqueue %q: %v", text, err)
		}
		if position != i+1 {
			t.Fatalf("position for %q = %d, want %d", text, position, i+1)
		}
	}
	if depth := app.QueueDepth("queue-room"); depth != 3 {
		t.Fatalf("queue depth = %d, want 3", depth)
	}

	release()
	deadline := time.Now().Add(5 * time.Second)
	for app.QueueDepth("queue-room") != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("queue never drained (depth %d)", app.QueueDepth("queue-room"))
		}
		time.Sleep(10 * time.Millisecond)
	}

	// The queue reaching zero means the last job started, not that it wrote:
	// wait for its reply to land before reading the room.
	var loaded *aggregate.Conversation
	for {
		var err error
		loaded, err = app.deps.Convs.Load(t.Context(), "queue-room")
		if err != nil {
			t.Fatal(err)
		}
		replies := 0
		for _, m := range loaded.Messages() {
			if m.Role() == enum.RoleAssistant {
				replies++
			}
		}
		if replies == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d replies landed before the deadline", replies)
		}
		time.Sleep(10 * time.Millisecond)
	}
	var users, replies []string
	for _, m := range loaded.Messages() {
		switch m.Role() {
		case enum.RoleUser:
			users = append(users, m.Content())
		case enum.RoleAssistant:
			replies = append(replies, m.Content())
		}
	}
	if got := strings.Join(users, ","); got != "one,two,three" {
		t.Fatalf("user lines = %q, want arrival order", got)
	}
	if len(replies) != 3 {
		t.Fatalf("replies = %d, want one per message", len(replies))
	}
}
