package job

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// barrierRecorder builds steps that record their order and can hang. A cut
// step keeps running on its goroutine, so the order is shared between that
// goroutine and the test — guarded, or -race reports it.
type barrierRecorder struct {
	mu    sync.Mutex
	order []string
	hang  map[string]bool
	delay map[string]time.Duration
}

func (r *barrierRecorder) record(name string) {
	r.mu.Lock()
	r.order = append(r.order, name)
	r.mu.Unlock()
}

func (r *barrierRecorder) recorded() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.order...)
}

func (r *barrierRecorder) step(name string) func(context.Context) error {
	return func(ctx context.Context) error {
		r.record(name)
		if d := r.delay[name]; d > 0 {
			select {
			case <-time.After(d):
			case <-ctx.Done():
			}
		}
		if r.hang[name] {
			<-make(chan struct{}) // a step that ignores its deadline
		}
		return nil
	}
}

func (r *barrierRecorder) steps() BarrierSteps {
	return BarrierSteps{
		CloseAdmission: r.step("admission"),
		StopTurns:      r.step("turns"),
		FlushWriters:   r.step("writers"),
		DrainSockets:   r.step("sockets"),
		CloseStorage:   r.step("storage"),
	}
}

// The barrier is single-pass: a second signal does not restart the budget or
// re-run a phase, it waits for the pass already running and reports the same
// outcome. Restarting the budget on a double Ctrl-C is how a shutdown window
// gets spent twice.
func TestBarrierRunsOnceForTwoSignals(t *testing.T) {
	var runs atomic.Int32
	rec := &barrierRecorder{delay: map[string]time.Duration{"turns": 50 * time.Millisecond}}
	steps := rec.steps()
	stop := steps.StopTurns
	steps.StopTurns = func(ctx context.Context) error {
		runs.Add(1)
		return stop(ctx)
	}
	b := NewBarrier(steps)

	first := make(chan BarrierReport, 1)
	go func() { first <- b.Run(context.Background()) }()
	time.Sleep(10 * time.Millisecond) // let the first pass enter phase 1
	second := b.Run(context.Background())
	got := <-first

	if runs.Load() != 1 {
		t.Fatalf("turn phase ran %d times, want once", runs.Load())
	}
	if len(second.Phases) != len(got.Phases) || second.Elapsed != got.Elapsed {
		t.Fatalf("second call reported a different pass: %+v vs %+v", second, got)
	}
	if got := rec.recorded(); len(got) != 5 {
		t.Fatalf("steps ran %v, want each phase once", got)
	}
}

// A phase that ignores its deadline is abandoned at its budget and the next
// phase still runs: the process must leave inside the stop window even when a
// step misbehaves.
func TestBarrierCutsAWedgedPhase(t *testing.T) {
	rec := &barrierRecorder{hang: map[string]bool{"turns": true}}
	b := NewBarrier(rec.steps())
	// The plan's budgets are fixed and phase 1 is 12 s — cut it down for the
	// test without touching the shipped promise.
	b.plan[1].Budget = 50 * time.Millisecond

	started := time.Now()
	report := b.Run(context.Background())
	elapsed := time.Since(started)

	if elapsed > 2*time.Second {
		t.Fatalf("a wedged phase held the barrier for %v", elapsed)
	}
	if !report.Phases[1].Cut {
		t.Fatalf("turn phase = %+v, want it cut at its deadline", report.Phases[1])
	}
	if got := rec.recorded(); len(got) != 5 || got[4] != "storage" {
		t.Fatalf("steps after the cut = %v, want the rest of the plan", got)
	}
}

// The budget is a promise: 25 s inside a 30 s stop window, with the phases
// that write history finished inside 17.5 s so a stale 20 s unit still keeps
// the record.
func TestBarrierBudgetPerPhase(t *testing.T) {
	b := NewBarrier(BarrierSteps{})
	var total time.Duration
	for _, phase := range b.Plan() {
		total += phase.Budget
		if phase.Budget <= 0 {
			t.Fatalf("phase %q has no budget", phase.Name)
		}
	}
	if total > BarrierBudget {
		t.Fatalf("phases sum to %v, budget %v", total, BarrierBudget)
	}
	var record time.Duration
	for _, phase := range b.Plan()[:3] {
		record += phase.Budget
	}
	if record > BarrierRecordBudget {
		t.Fatalf("phases 0–2 sum to %v, want at most %v", record, BarrierRecordBudget)
	}
	if got := b.Plan()[1].Budget; got < b.Plan()[0].Budget || got < b.Plan()[3].Budget {
		t.Fatalf("the turn phase gets %v — the largest budget belongs to persistence", got)
	}
}

// Persistence comes before sockets: history cannot be replayed after a kill,
// while a socket reconnects and resyncs.
func TestBarrierStopsTurnsBeforeSockets(t *testing.T) {
	rec := &barrierRecorder{}
	b := NewBarrier(rec.steps())
	b.Run(context.Background())
	want := []string{"admission", "turns", "writers", "sockets", "storage"}
	got := rec.recorded()
	if len(got) != len(want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// A failing phase is reported, never fatal: the barrier still closes storage.
func TestBarrierNotesAFailedPhaseAndContinues(t *testing.T) {
	rec := &barrierRecorder{}
	steps := rec.steps()
	steps.FlushWriters = func(context.Context) error { return errors.New("recordings stuck") }
	b := NewBarrier(steps)
	report := b.Run(context.Background())
	if report.Phases[2].Err == nil {
		t.Fatalf("writer phase = %+v, want the error reported", report.Phases[2])
	}
	if got := rec.recorded(); got[len(got)-1] != "storage" {
		t.Fatalf("order = %v, want storage last", got)
	}
}
