package domain

import "time"

// Scheduler policy constants.
//
// These govern the execution scheduler's concurrency and lease/heartbeat
// cadence. The scheduler itself lives in the application layer; the
// policy constants live here so the rules are visible at the layer that
// owns the workflow run model.
const (
	// JobLease is the duration a job run is leased to an executor before
	// it is considered stale and eligible for re-claim.
	JobLease = 30 * time.Second
	// JobHeartbeat is the interval at which an executor must refresh a
	// job run's lease to keep it claimed.
	JobHeartbeat = 10 * time.Second
	// MaxParallelJobs is the default maximum number of jobs the
	// scheduler runs concurrently for one workflow run.
	MaxParallelJobs = 4
	// MaxFanout is the maximum fan-out for parallel job dispatch.
	MaxFanout = 32
)

// WakeWaitingRun transitions a waiting run back to queued after its
// WakeAt has passed: the run, its waiting jobs, and their waiting steps
// all move to StatusQueued. WakeAt is cleared. Terminal statuses
// (success, failed, etc.) are never touched. This is a pure function on
// *WorkflowRun with no I/O.
func WakeWaitingRun(run *WorkflowRun) {
	if run == nil || run.Status != StatusWaiting {
		return
	}
	run.Status = StatusQueued
	run.WakeAt = nil
	for i := range run.Jobs {
		if run.Jobs[i].Status == StatusWaiting {
			run.Jobs[i].Status = StatusQueued
			for j := range run.Jobs[i].Steps {
				if run.Jobs[i].Steps[j].Status == StatusWaiting {
					run.Jobs[i].Steps[j].Status = StatusQueued
				}
			}
		}
	}
}

// StartRun transitions a newly created run to queued and stamps CreatedAt.
// A run already in queued keeps its status (idempotent for re-Start).
// Terminal runs are not restarted.
func (r *WorkflowRun) StartRun(now time.Time) {
	if r == nil || r.Status.IsTerminal() {
		return
	}
	if r.Status == "" {
		r.Status = StatusQueued
	}
	r.CreatedAt = now
}

// BeginRunning transitions the run to running and stamps StartedAt on the
// first transition. A waiting run is not disturbed (a step parked it).
func (r *WorkflowRun) BeginRunning(now time.Time) {
	if r == nil || r.Status == StatusWaiting {
		return
	}
	r.Status = StatusRunning
	if r.StartedAt == nil {
		r.StartedAt = &now
	}
}

// ParkWait transitions the run to waiting and records the wake time.
func (r *WorkflowRun) ParkWait(wakeAt time.Time) {
	if r == nil {
		return
	}
	r.Status = StatusWaiting
	r.WakeAt = &wakeAt
}

// ParkBlocked transitions the run to blocked with a reason.
func (r *WorkflowRun) ParkBlocked(reason string) {
	if r == nil {
		return
	}
	r.Status = StatusBlocked
	r.BlockedReason = reason
}

// FailDAG transitions the run to failed with FinishedAt, used when the
// workflow DAG has structural issues.
func (r *WorkflowRun) FailDAG(now time.Time) {
	if r == nil {
		return
	}
	r.Status = StatusFailed
	r.FinishedAt = &now
}

// Finalize transitions the run to a terminal state based on the job
// summary. Sets FinishedAt. If the run is waiting or blocked, or any job
// is still running/queued/waiting, the run is not finalized.
func (r *WorkflowRun) Finalize(now time.Time, sum RunSummary) {
	if r == nil {
		return
	}
	if r.Status == StatusWaiting || r.Status == StatusBlocked {
		return
	}
	if sum.Running > 0 || sum.Queued > 0 || sum.Waiting > 0 {
		return
	}
	r.FinishedAt = &now
	switch {
	case sum.Failed > 0:
		r.Status = StatusFailed
	case sum.Blocked > 0 && sum.Success+sum.Skipped < sum.Total:
		r.Status = StatusFailed
	default:
		r.Status = StatusSuccess
	}
}

// Cancel transitions the run to cancelled, cancels all active jobs, and
// stamps FinishedAt. Terminal runs are not re-cancelled.
func (r *WorkflowRun) Cancel(now time.Time) {
	if r == nil || r.Status.IsTerminal() {
		return
	}
	r.Status = StatusCancelled
	r.FinishedAt = &now
	for i := range r.Jobs {
		if r.Jobs[i].Status.IsActive() {
			r.Jobs[i].Status = StatusCancelled
			r.Jobs[i].FinishedAt = &now
		}
	}
}

// --- JobRun state machine ---

// Skip transitions the job to skipped and stamps FinishedAt.
func (j *JobRun) Skip(now time.Time) {
	if j == nil {
		return
	}
	j.Status = StatusSkipped
	j.FinishedAt = &now
}

// BeginRunning transitions the job to running and stamps StartedAt.
func (j *JobRun) BeginRunning(now time.Time) {
	if j == nil {
		return
	}
	j.Status = StatusRunning
	j.StartedAt = &now
}

// Succeed transitions the job to success, records outputs, and stamps
// FinishedAt.
func (j *JobRun) Succeed(outputs map[string]any, now time.Time) {
	if j == nil {
		return
	}
	j.Status = StatusSuccess
	j.Outputs = outputs
	j.FinishedAt = &now
}

// ParkWait transitions the job to waiting (a step parked for wait_until).
func (j *JobRun) ParkWait() {
	if j == nil {
		return
	}
	j.Status = StatusWaiting
}

// Fail transitions the job to failed with a reason and stamps FinishedAt.
func (j *JobRun) Fail(reason string, now time.Time) {
	if j == nil {
		return
	}
	j.Status = StatusFailed
	j.FailureReason = reason
	j.FinishedAt = &now
}

// ParkBlocked transitions the job to blocked with a reason (upstream
// failure).
func (j *JobRun) ParkBlocked(reason string) {
	if j == nil {
		return
	}
	j.Status = StatusBlocked
	j.BlockedReason = reason
}

// EnsureStep returns the StepRun for the given step definition, appending
// a new queued StepRun if the step has not been seen yet. The returned
// pointer is safe to mutate in place.
func (j *JobRun) EnsureStep(step Step) *StepRun {
	if j == nil {
		return nil
	}
	for i := range j.Steps {
		if j.Steps[i].StepID == step.ID {
			return &j.Steps[i]
		}
	}
	j.Steps = append(j.Steps, StepRun{
		ID:     NewID("step"),
		StepID: step.ID,
		Name:   step.Name,
		Status: StatusQueued,
	})
	return &j.Steps[len(j.Steps)-1]
}

// --- StepRun state machine ---

// BeginRunning transitions the step to running and stamps StartedAt.
func (s *StepRun) BeginRunning(now time.Time) {
	if s == nil {
		return
	}
	s.Status = StatusRunning
	s.StartedAt = &now
}

// Fail transitions the step to failed with exit code and error, and
// stamps FinishedAt.
func (s *StepRun) Fail(exitCode int, errMsg string, now time.Time) {
	if s == nil {
		return
	}
	s.Status = StatusFailed
	s.ExitCode = exitCode
	s.Error = errMsg
	s.FinishedAt = &now
}

// Succeed transitions the step to success with exit code and stamps
// FinishedAt.
func (s *StepRun) Succeed(exitCode int, now time.Time) {
	if s == nil {
		return
	}
	s.Status = StatusSuccess
	s.ExitCode = exitCode
	s.FinishedAt = &now
}

// ParkWait transitions the step to waiting and records the wake time.
func (s *StepRun) ParkWait(wakeAt time.Time) {
	if s == nil {
		return
	}
	s.Status = StatusWaiting
	s.WakeAt = &wakeAt
}

// RunStatus is the lifecycle of a workflow run, job, or step.
type RunStatus string

const (
	StatusPending   RunStatus = "pending"
	StatusQueued    RunStatus = "queued"
	StatusBlocked   RunStatus = "blocked"
	StatusWaiting   RunStatus = "waiting"
	StatusRunning   RunStatus = "running"
	StatusSuccess   RunStatus = "success"
	StatusFailed    RunStatus = "failed"
	StatusCancelled RunStatus = "cancelled"
	StatusSkipped   RunStatus = "skipped"
	StatusExpired   RunStatus = "expired"
)

// IsTerminal reports whether the status will not change without an
// explicit retry.
func (s RunStatus) IsTerminal() bool {
	switch s {
	case StatusSuccess, StatusFailed, StatusCancelled, StatusSkipped, StatusExpired:
		return true
	default:
		return false
	}
}

// IsActive reports whether work is in flight or queued.
func (s RunStatus) IsActive() bool {
	switch s {
	case StatusPending, StatusQueued, StatusRunning, StatusWaiting:
		return true
	default:
		return false
	}
}

// WorkflowRun is an immutable snapshot of a definition plus runtime
// metadata. Never execute from a mutable file after the run has started.
type WorkflowRun struct {
	ID            string
	WorkflowID    string
	Name          string
	Status        RunStatus
	BlockedReason string
	TriggerID     string
	EventID       string
	// Event is the normalized event payload that triggered this run (when
	// the trigger kind is event). Nil for schedule, manual, and ui-driven
	// runs. Used by agent steps to render ${event.<attr>} placeholders in
	// the YAML prompt; not persisted to disk — restart re-derives it from
	// the event store when needed.
	Event        *Event
	Workspace    string
	Definition   WorkflowDefinition
	Jobs         []JobRun
	CreatedAt    time.Time
	StartedAt    *time.Time
	FinishedAt   *time.Time
	WakeAt       *time.Time
	PipelineHash string
	RequestedBy  string // ui | agent | schedule | event | manual
}

// PipelineRun is the CI-facing name for a workflow run.
type PipelineRun = WorkflowRun

// JobRun is the runtime state of one job in a run.
type JobRun struct {
	ID            string
	JobID         string
	Name          string
	Status        RunStatus
	BlockedReason string
	RunnerID      string
	Executor      string
	ExitCode      int
	FailureReason string
	QueuedAt      *time.Time
	StartedAt     *time.Time
	FinishedAt    *time.Time
	HeartbeatAt   *time.Time
	LeaseUntil    *time.Time
	Outputs       map[string]any
	Steps         []StepRun
}

// StepRun is the runtime state of one step.
type StepRun struct {
	ID             string
	StepID         string
	Name           string
	Status         RunStatus
	ExitCode       int
	Error          string
	Output         string
	ConversationID string
	StartedAt      *time.Time
	FinishedAt     *time.Time
	WakeAt         *time.Time
}

// JobRunByID returns the job run for a definition job id.
func (r *WorkflowRun) JobRunByID(jobID string) *JobRun {
	if r == nil {
		return nil
	}
	for i := range r.Jobs {
		if r.Jobs[i].JobID == jobID {
			return &r.Jobs[i]
		}
	}
	return nil
}

// Summary counts jobs by status.
func (r *WorkflowRun) Summary() RunSummary {
	var s RunSummary
	if r == nil {
		return s
	}
	for _, j := range r.Jobs {
		s.Total++
		switch j.Status {
		case StatusSuccess:
			s.Success++
		case StatusFailed:
			s.Failed++
		case StatusRunning:
			s.Running++
		case StatusQueued, StatusPending:
			s.Queued++
		case StatusBlocked:
			s.Blocked++
		case StatusWaiting:
			s.Waiting++
		case StatusCancelled:
			s.Cancelled++
		case StatusSkipped:
			s.Skipped++
		}
	}
	return s
}

// RunSummary is the compact agent/UI rollup of a run.
type RunSummary struct {
	Total     int `json:"total"`
	Success   int `json:"success"`
	Failed    int `json:"failed"`
	Running   int `json:"running"`
	Queued    int `json:"queued"`
	Blocked   int `json:"blocked"`
	Waiting   int `json:"waiting"`
	Cancelled int `json:"cancelled"`
	Skipped   int `json:"skipped"`
}

// FailedJobs returns jobs in failed status.
func (r *WorkflowRun) FailedJobs() []JobRun {
	if r == nil {
		return nil
	}
	var out []JobRun
	for _, j := range r.Jobs {
		if j.Status == StatusFailed {
			out = append(out, j)
		}
	}
	return out
}

// CanTransition reports whether a run/job/step may move from -> to.
func CanTransition(from, to RunStatus) bool {
	if from == to {
		return true
	}
	switch from {
	case StatusPending:
		return to == StatusQueued || to == StatusBlocked || to == StatusCancelled || to == StatusWaiting
	case StatusQueued:
		return to == StatusRunning || to == StatusBlocked || to == StatusCancelled || to == StatusSkipped || to == StatusWaiting
	case StatusBlocked:
		return to == StatusQueued || to == StatusPending || to == StatusCancelled || to == StatusFailed
	case StatusWaiting:
		return to == StatusQueued || to == StatusRunning || to == StatusCancelled || to == StatusExpired
	case StatusRunning:
		return to == StatusSuccess || to == StatusFailed || to == StatusCancelled || to == StatusWaiting || to == StatusBlocked
	case StatusFailed, StatusCancelled:
		return to == StatusQueued || to == StatusPending // retry
	default:
		return false
	}
}
