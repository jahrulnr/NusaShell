package domain

import "time"

// LogChunk is one append-only log fragment.
type LogChunk struct {
	RunID     string
	JobID     string
	StepID    string
	Sequence  uint64
	Timestamp time.Time
	Stream    string // stdout, stderr, system
	Text      string
}

// Artifact is a durable job output.
type Artifact struct {
	ID        string
	RunID     string
	JobID     string
	Name      string
	Paths     []string
	Size      int64
	Checksum  string
	CreatedAt time.Time
	ExpiresAt time.Time
	Path      string
}

// CacheEntry is a reusable dependency archive.
type CacheEntry struct {
	Namespace string
	Key       string
	Size      int64
	LastHit   time.Time
	LastWrite time.Time
	Platform  string
}

// RunnerStatus is runner liveness.
type RunnerStatus string

const (
	RunnerOnline  RunnerStatus = "online"
	RunnerOffline RunnerStatus = "offline"
	RunnerPaused  RunnerStatus = "paused"
)

// Runner is scheduler-visible capacity, not an executor implementation.
type Runner struct {
	ID           string
	Name         string
	Labels       []string
	Executor     string
	Status       RunnerStatus
	MaxParallel  int
	Running      int
	Capabilities []string
}

// TriggerRecord is the persisted once/every/when row.
type TriggerRecord struct {
	ID         string
	WorkflowID string
	Kind       TriggerKind
	Status     ScheduleStatus
	Spec       Trigger
}
