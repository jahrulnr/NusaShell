package domain

import "time"

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
	Workspace     string
	Definition    WorkflowDefinition
	Jobs          []JobRun
	CreatedAt     time.Time
	StartedAt     *time.Time
	FinishedAt    *time.Time
	WakeAt        *time.Time
	PipelineHash  string
	RequestedBy   string // ui | agent | schedule | event | manual
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
