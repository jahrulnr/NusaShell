package application

import "sync"

// taskMemoryBreakerThreshold is the number of consecutive embed/search
// failures after which the async semantic lane is disabled until a
// success or process restart. Single-user, in-memory, per-process.
const taskMemoryBreakerThreshold = 3

// taskMemoryBreaker is a simple consecutive-failure circuit breaker for the
// async task-memory semantic lane. After taskMemoryBreakerThreshold
// consecutive failures, Tripped() returns true and the lane skips work.
// A single success resets the counter. State is in-memory only — a restart
// clears it. Not persisted, not per-model: single-user.
type taskMemoryBreaker struct {
	mu        sync.Mutex
	failures  int
	threshold int
}

// newTaskMemoryBreaker creates a breaker with the default threshold.
func newTaskMemoryBreaker() *taskMemoryBreaker {
	return &taskMemoryBreaker{threshold: taskMemoryBreakerThreshold}
}

// Tripped reports whether the breaker has tripped (lane should skip).
func (b *taskMemoryBreaker) Tripped() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.failures >= b.threshold
}

// RecordFailure increments the consecutive-failure counter.
func (b *taskMemoryBreaker) RecordFailure() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
}

// RecordSuccess resets the consecutive-failure counter to zero.
func (b *taskMemoryBreaker) RecordSuccess() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
}
