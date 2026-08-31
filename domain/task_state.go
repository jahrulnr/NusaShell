package domain

import "time"

// TaskState is the shared lifecycle bookkeeping of every long-running
// entity: a stable ID, a typed status, start/finish timestamps, and an
// error message. Embedded into AcpRun, WorkflowRun, JobRun, and StepRun
// so the four families implement one lifecycle shape instead of four
// parallel field sets. Status stays typed per family (RunStatus vs
// AcpRunStatus) via the S parameter; terminal/live predicates stay on
// those types.
type TaskState[S ~string] struct {
	ID         string
	Status     S
	StartedAt  time.Time
	FinishedAt time.Time
	Error      string
}
