package application

import "testing"

func TestTaskMemoryBreakerTripsAfterNFailures(t *testing.T) {
	b := &taskMemoryBreaker{threshold: 3}

	if b.Tripped() {
		t.Fatal("breaker must not be tripped with zero failures")
	}

	b.RecordFailure()
	b.RecordFailure()
	if b.Tripped() {
		t.Fatal("breaker must not be tripped after 2 failures (threshold 3)")
	}

	b.RecordFailure()
	if !b.Tripped() {
		t.Fatal("breaker must be tripped after 3 consecutive failures")
	}

	b.RecordFailure()
	if !b.Tripped() {
		t.Fatal("breaker must stay tripped after additional failures")
	}
}

func TestTaskMemoryBreakerSuccessResets(t *testing.T) {
	b := &taskMemoryBreaker{threshold: 3}

	b.RecordFailure()
	b.RecordFailure()
	b.RecordFailure()
	if !b.Tripped() {
		t.Fatal("breaker must be tripped after 3 failures")
	}

	b.RecordSuccess()
	if b.Tripped() {
		t.Fatal("breaker must reset after a success")
	}

	// After reset, it takes threshold failures again to trip.
	b.RecordFailure()
	b.RecordFailure()
	if b.Tripped() {
		t.Fatal("breaker must not be tripped after 2 failures post-reset")
	}
}

func TestTaskMemoryBreakerDefaultThreshold(t *testing.T) {
	b := newTaskMemoryBreaker()
	if b.threshold != taskMemoryBreakerThreshold {
		t.Fatalf("default threshold = %d, want %d", b.threshold, taskMemoryBreakerThreshold)
	}
}
