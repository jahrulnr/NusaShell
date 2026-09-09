package automation

import (
	"context"
	"fmt"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
)

// Concurrency ownership (identity-scoped locks):
//
//   - AutomationScheduler.startFromTrigger renders concurrency.key per event
//     (domain.ResolveConcurrencyKey) and calls ExecutionScheduler.startUnderConcurrency.
//   - ExecutionScheduler owns RunLockStore acquire/release for the run lifetime
//     and the process-local FIFO queue waiters per rendered key.
//   - Agent-step conversation reuse relies on this serialization when the
//     workflow uses skip/queue/replace; there is no separate busy-fail guard.

const (
	defaultConcurrencyQueueCap  = 10
	defaultConcurrencyQueueWait = 30 * time.Minute
	queueOverflowReason         = "automation.queue.overflow"
	queueWaitTimeoutReason      = "automation.queue.wait_timeout"
	queueWaitCancelledReason    = "automation.queue.wait_cancelled"
)

// concurrencyHeld tracks which rendered key a running workflow holds.
type concurrencyHeld struct {
	key   string
	locks RunLockStore
}

// concurrencyWaiter is one FIFO entry waiting for a rendered key to free.
type concurrencyWaiter struct {
	runID  string
	ready  chan struct{} // closed when this waiter becomes the holder
	cancel chan struct{} // closed when the waiter is abandoned (timeout/cancel)
}

// trackConcurrencyLock records that runID holds key in locks until the run
// becomes terminal. Call only after a successful Acquire.
func (s *ExecutionScheduler) trackConcurrencyLock(runID, key string, locks RunLockStore) {
	if s == nil || runID == "" || key == "" || locks == nil {
		return
	}
	s.lockMu.Lock()
	defer s.lockMu.Unlock()
	if s.heldLocks == nil {
		s.heldLocks = map[string]concurrencyHeld{}
	}
	s.heldLocks[runID] = concurrencyHeld{key: key, locks: locks}
}

// detachConcurrencyLock releases the lock for runID without waking waiters.
// Used by replace so the replacing run can acquire before any queued waiter.
func (s *ExecutionScheduler) detachConcurrencyLock(ctx context.Context, runID string) {
	if s == nil || runID == "" {
		return
	}
	s.lockMu.Lock()
	held, ok := s.heldLocks[runID]
	if ok {
		delete(s.heldLocks, runID)
	}
	s.lockMu.Unlock()
	if !ok || held.locks == nil {
		return
	}
	_ = held.locks.Release(ctx, held.key, runID)
}

// releaseConcurrencyLock frees the lock held by runID (if any) and wakes the
// next non-cancelled FIFO waiter for that key. Safe to call multiple times.
func (s *ExecutionScheduler) releaseConcurrencyLock(ctx context.Context, runID string) {
	if s == nil || runID == "" {
		return
	}
	s.lockMu.Lock()
	held, ok := s.heldLocks[runID]
	if ok {
		delete(s.heldLocks, runID)
	}
	s.lockMu.Unlock()
	if !ok {
		return
	}
	if held.locks != nil {
		_ = held.locks.Release(ctx, held.key, runID)
	}
	s.wakeNextWaiter(held.key)
}

// wakeNextWaiter closes ready on the first non-cancelled waiter for key.
func (s *ExecutionScheduler) wakeNextWaiter(key string) {
	if s == nil || key == "" {
		return
	}
	for {
		s.lockMu.Lock()
		q := s.queueByKey[key]
		if len(q) == 0 {
			s.lockMu.Unlock()
			return
		}
		next := q[0]
		s.queueByKey[key] = q[1:]
		if len(s.queueByKey[key]) == 0 {
			delete(s.queueByKey, key)
		}
		s.lockMu.Unlock()
		select {
		case <-next.cancel:
			continue
		default:
			close(next.ready)
			return
		}
	}
}

func (s *ExecutionScheduler) queueLenLocked(key string) int {
	return len(s.queueByKey[key])
}

func (s *ExecutionScheduler) enqueueWaiterLocked(key string, w *concurrencyWaiter) {
	if s.queueByKey == nil {
		s.queueByKey = map[string][]*concurrencyWaiter{}
	}
	s.queueByKey[key] = append(s.queueByKey[key], w)
}

func (s *ExecutionScheduler) removeWaiterLocked(key, runID string) {
	q := s.queueByKey[key]
	if len(q) == 0 {
		return
	}
	out := q[:0]
	for _, w := range q {
		if w.runID == runID {
			continue
		}
		out = append(out, w)
	}
	if len(out) == 0 {
		delete(s.queueByKey, key)
		return
	}
	s.queueByKey[key] = out
}

// concurrencyQueueWaitTimeout prefers the largest job timeout on the
// definition; otherwise 30 minutes.
func concurrencyQueueWaitTimeout(w *domain.WorkflowDefinition) time.Duration {
	var max time.Duration
	if w != nil {
		for _, j := range w.Jobs {
			if j.Timeout > max {
				max = j.Timeout
			}
		}
	}
	if max > 0 {
		return max
	}
	return defaultConcurrencyQueueWait
}

// startUnderConcurrency acquires the rendered key (or queues) then starts the
// run asynchronously. For allow, or when locks are unset, it starts immediately.
func (s *ExecutionScheduler) startUnderConcurrency(ctx context.Context, run *domain.WorkflowRun, key string, policy domain.ConcurrencyPolicy, locks RunLockStore) error {
	if s == nil {
		return fmt.Errorf("execution scheduler not configured")
	}
	if run == nil {
		return fmt.Errorf("workflow run is empty")
	}
	if policy == domain.ConcurrencyAllow || locks == nil || key == "" {
		return s.StartRunAsync(ctx, run)
	}

	active, ok, err := locks.Active(ctx, key)
	if err != nil {
		return &runStartFailure{err: fmt.Errorf("inspect concurrency lock %q: %w", key, err)}
	}
	if ok {
		// Self-heal durable locks left behind after a crash: a terminal or
		// missing run no longer owns the key.
		if holder, gerr := s.Runs.Get(ctx, active); gerr != nil || holder == nil || holder.Status.IsTerminal() {
			_ = locks.Release(ctx, key, active)
			ok = false
		}
	}
	if ok {
		switch policy {
		case domain.ConcurrencySkip:
			return nil
		case domain.ConcurrencyReplace:
			s.detachConcurrencyLock(ctx, active)
			if err := s.Cancel(ctx, active); err != nil {
				return &runStartFailure{err: fmt.Errorf("cancel active run %q: %w", active, err)}
			}
			// Cancel may have already Released via a race; ensure key is free.
			_ = locks.Release(ctx, key, active)
		case domain.ConcurrencyQueue:
			return s.enqueueRun(ctx, run, key, locks)
		}
	}
	return s.acquireAndStart(ctx, run, key, locks)
}

func (s *ExecutionScheduler) acquireAndStart(ctx context.Context, run *domain.WorkflowRun, key string, locks RunLockStore) error {
	if err := locks.Acquire(ctx, key, run.ID); err != nil {
		return &runStartFailure{err: fmt.Errorf("acquire concurrency lock %q: %w", key, err)}
	}
	s.trackConcurrencyLock(run.ID, key, locks)
	var startErr error
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				startErr = &runStartFailure{created: true, err: fmt.Errorf("start workflow run panicked: %v", recovered)}
			}
		}()
		startErr = s.StartRunAsync(context.WithoutCancel(ctx), run)
	}()
	if startErr != nil {
		s.releaseConcurrencyLock(context.WithoutCancel(ctx), run.ID)
		return startErr
	}
	return nil
}

// enqueueRun parks a new run behind the active holder of key. Overflow drops
// the new run as skipped with automation.queue.overflow. Waiters are woken
// FIFO when the holder releases.
func (s *ExecutionScheduler) enqueueRun(ctx context.Context, run *domain.WorkflowRun, key string, locks RunLockStore) error {
	s.lockMu.Lock()
	if s.queueLenLocked(key) >= defaultConcurrencyQueueCap {
		s.lockMu.Unlock()
		return s.finalizeSkippedRun(ctx, run, queueOverflowReason)
	}
	waiter := &concurrencyWaiter{
		runID:  run.ID,
		ready:  make(chan struct{}),
		cancel: make(chan struct{}),
	}
	s.enqueueWaiterLocked(key, waiter)
	s.lockMu.Unlock()

	// A queued run is NOT running: storing it as running lets periodic
	// Tick passes start its jobs without holding the key. Mark it queued.
	now := s.now()
	run.StartRun(now)
	run.Status = domain.StatusQueued
	if err := s.Runs.Create(ctx, run); err != nil {
		s.lockMu.Lock()
		s.removeWaiterLocked(key, run.ID)
		s.lockMu.Unlock()
		close(waiter.cancel)
		return fmt.Errorf("create workflow run: %w", &runStartFailure{err: err})
	}
	s.emit(contracts.EventAutomationRunCreated, map[string]any{
		"run_id": run.ID, "workflow_id": run.WorkflowID, "status": run.Status, "queued": true,
	})

	timeout := concurrencyQueueWaitTimeout(&run.Definition)
	waitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	s.goSafe("automation-queue-wait", func() {
		defer cancel()
		s.awaitQueueSlot(waitCtx, run, key, locks, waiter)
	})
	return nil
}

func (s *ExecutionScheduler) awaitQueueSlot(ctx context.Context, run *domain.WorkflowRun, key string, locks RunLockStore, waiter *concurrencyWaiter) {
	select {
	case <-waiter.ready:
		if err := locks.Acquire(context.Background(), key, run.ID); err != nil {
			_ = s.finalizeSkippedRun(context.Background(), run, fmt.Sprintf("automation.queue.acquire_failed: %v", err))
			s.wakeNextWaiter(key)
			return
		}
		s.trackConcurrencyLock(run.ID, key, locks)
		s.goSafe("automation-run", func() { s.runTickAsync(run.ID) })
	case <-ctx.Done():
		close(waiter.cancel)
		s.lockMu.Lock()
		s.removeWaiterLocked(key, run.ID)
		s.lockMu.Unlock()
		reason := queueWaitTimeoutReason
		if ctx.Err() == context.Canceled {
			reason = queueWaitCancelledReason
		}
		_ = s.finalizeSkippedRun(context.Background(), run, reason)
	}
}

func (s *ExecutionScheduler) finalizeSkippedRun(ctx context.Context, run *domain.WorkflowRun, reason string) error {
	if s == nil || run == nil || s.Runs == nil {
		return fmt.Errorf("run store not configured")
	}
	now := s.now()
	existing, err := s.Runs.Get(ctx, run.ID)
	if err != nil || existing == nil {
		run.SkipRun(now, reason)
		if cerr := s.Runs.Create(ctx, run); cerr != nil {
			return &runStartFailure{err: cerr}
		}
		s.emit(contracts.EventAutomationRunCreated, map[string]any{
			"run_id": run.ID, "workflow_id": run.WorkflowID, "status": run.Status, "reason": reason,
		})
	} else if !existing.Status.IsTerminal() {
		existing.SkipRun(now, reason)
		if uerr := s.Runs.Update(ctx, existing); uerr != nil {
			return uerr
		}
		run = existing
	}
	s.emit("automation.queue.skipped", map[string]any{
		"run_id": run.ID, "workflow_id": run.WorkflowID, "reason": reason,
	})
	return nil
}

// isQueuedRun reports whether runID is still waiting in an in-memory queue.
func (s *ExecutionScheduler) isQueuedRun(runID string) bool {
	s.lockMu.Lock()
	defer s.lockMu.Unlock()
	for _, q := range s.queueByKey {
		for _, w := range q {
			if w.runID == runID {
				return true
			}
		}
	}
	return false
}

// abandonQueuedRun cancels an in-memory queue waiter for runID (used by
// Cancel when the run has not yet acquired the lock).
func (s *ExecutionScheduler) abandonQueuedRun(runID string) {
	if s == nil || runID == "" {
		return
	}
	s.lockMu.Lock()
	defer s.lockMu.Unlock()
	for key, q := range s.queueByKey {
		for _, w := range q {
			if w.runID != runID {
				continue
			}
			select {
			case <-w.cancel:
			default:
				close(w.cancel)
			}
			s.removeWaiterLocked(key, runID)
			return
		}
	}
}
