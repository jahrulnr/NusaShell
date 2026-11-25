package job

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// The shutdown budget: 25 s inside a 30 s stop window, split by phase and
// front-loaded so the phases that write history finish inside even a stale
// 20 s unit — a machine that has not reinstalled the service yet loses the
// drain, never the record. The reserve absorbs the parts that cannot be
// timed exactly (process scheduling, a slow fsync).
const (
	barrierAdmissionBudget = 500 * time.Millisecond
	// The largest phase: a turn unwinds by killing its tool, writing the
	// head plus the restart suffix, and persisting the reply.
	barrierTurnsBudget = 12 * time.Second
	// Recordings, shells, browsers, agents, MCP servers — everything that
	// holds a file or a process open.
	barrierWritersBudget = 5 * time.Second
	// Terminal frames, connections drained, listener closed.
	barrierSocketsBudget = 3 * time.Second
	barrierStorageBudget = time.Second
	// BarrierBudget is the whole promise; phases 0–2 stay inside
	// BarrierRecordBudget, the part a stale 20 s unit still honours.
	BarrierBudget       = 25 * time.Second
	BarrierRecordBudget = 17500 * time.Millisecond
)

// BarrierSteps are the implementations of the one shutdown order. The barrier
// owns when each runs and how long it may take; the composition only supplies
// what each step does. A nil step is skipped — a process without that part
// has nothing to stop there.
type BarrierSteps struct {
	// CloseAdmission stops taking work: new sends answer 503, alerts are
	// muted, the automation ticker stops. Nothing may start once the
	// barrier runs.
	CloseAdmission func(ctx context.Context) error
	// StopTurns terminates every room worker with the restart cause and lets
	// the turns unwind — a tool is killed, its head and restart suffix
	// written, the reply sealed interrupted. A worker that will not stop in
	// the budget is abandoned and named.
	StopTurns func(ctx context.Context) error
	// FlushWriters releases everything still holding a file or a process:
	// recordings, shell sessions, browsers, agents, MCP servers.
	FlushWriters func(ctx context.Context) error
	// DrainSockets lands the terminal frames, drains connections and closes
	// the listener. A client that misses one reconnects and resyncs.
	DrainSockets func(ctx context.Context) error
	// CloseStorage closes the database and the log, last: a step that fails
	// here leaves a stale marker, not a lost history.
	CloseStorage func(ctx context.Context) error
}

// BarrierPhase is one step of the plan: the name the log reports, its budget,
// and the work. Exposed through Barrier.Plan so a test (and an operator) can
// read the promise instead of inferring it.
type BarrierPhase struct {
	Name   string
	Budget time.Duration
	run    func(ctx context.Context) error
}

// BarrierReport is what one pass did: per phase, how long it took and whether
// it was cut at its deadline.
type BarrierReport struct {
	Phases  []BarrierPhaseResult
	Elapsed time.Duration
}

// BarrierPhaseResult is one phase's outcome. Cut means the deadline fired and
// the phase was abandoned — its work may still be running and will die with
// the process.
type BarrierPhaseResult struct {
	Name    string
	Budget  time.Duration
	Elapsed time.Duration
	Err     error
	Cut     bool
}

// Barrier is the process's one shutdown sequence. It runs once, in one order,
// for both paths — a signal and an update restart — so no second order can
// drift from it. A concurrent or later call waits for the running pass and
// returns its report: a second signal neither restarts the budget nor aborts
// the sequence, because the only hard stop left is the supervisor's SIGKILL
// and the budget is built to finish before it.
type Barrier struct {
	// The steps live in plan: each phase holds the one function it runs, so
	// there is no second copy of them to fall out of step with it.
	plan []BarrierPhase

	once sync.Once
	done chan struct{}
	run  BarrierReport
	log  *slog.Logger
}

// NewBarrier binds the steps and fixes the plan. The order, and each phase's
// budget, live here — not at a call site.
func NewBarrier(steps BarrierSteps) *Barrier {
	b := &Barrier{done: make(chan struct{}), log: slog.Default()}
	b.plan = []BarrierPhase{
		{Name: "admission", Budget: barrierAdmissionBudget, run: steps.CloseAdmission},
		{Name: "turns", Budget: barrierTurnsBudget, run: steps.StopTurns},
		{Name: "writers", Budget: barrierWritersBudget, run: steps.FlushWriters},
		{Name: "sockets", Budget: barrierSocketsBudget, run: steps.DrainSockets},
		{Name: "storage", Budget: barrierStorageBudget, run: steps.CloseStorage},
	}
	return b
}

// Plan reports the fixed order and budgets. A test reads the promise from
// here; the composition never assembles it.
func (b *Barrier) Plan() []BarrierPhase {
	out := make([]BarrierPhase, len(b.plan))
	copy(out, b.plan)
	return out
}

// Run executes the sequence once and reports what each phase did. Every phase
// gets its own deadline and is cut at it — a step that ignores its context
// cannot hold the process past its budget, which is the property that keeps a
// restart inside the supervisor's window.
func (b *Barrier) Run(ctx context.Context) BarrierReport {
	b.once.Do(func() {
		started := time.Now()
		report := BarrierReport{Phases: make([]BarrierPhaseResult, 0, len(b.plan))}
		for _, phase := range b.plan {
			result := b.runPhase(ctx, phase)
			report.Phases = append(report.Phases, result)
			b.log.Info("shutdown phase",
				"phase", result.Name, "elapsed", result.Elapsed.Round(time.Millisecond),
				"budget", result.Budget, "cut", result.Cut, "err", result.Err)
		}
		report.Elapsed = time.Since(started)
		b.run = report
		b.log.Info("shutdown complete", "elapsed", report.Elapsed.Round(time.Millisecond))
		close(b.done)
	})
	<-b.done
	return b.run
}

// runPhase runs one step under its budget. The step runs on its own goroutine
// so a step that never returns is abandoned at the deadline instead of
// holding the process — the same rule every wait inside a step follows.
func (b *Barrier) runPhase(ctx context.Context, phase BarrierPhase) BarrierPhaseResult {
	started := time.Now()
	result := BarrierPhaseResult{Name: phase.Name, Budget: phase.Budget}
	if phase.run == nil {
		return result
	}
	// The phase deadline derives from the caller's ctx (an update job's
	// lifetime) but never from its cancellation: a canceling caller must not
	// cut a phase short mid-write.
	phaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), phase.Budget)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("phase panicked: %v", r)
			}
		}()
		done <- phase.run(phaseCtx)
	}()
	select {
	case err := <-done:
		result.Err = err
	case <-phaseCtx.Done():
		result.Cut = true
		result.Err = phaseCtx.Err()
	}
	result.Elapsed = time.Since(started)
	return result
}
