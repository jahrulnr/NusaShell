package contracts

import "encoding/json"

const (
	MethodCIRunsStart     = "ci.runs.start"
	MethodCIRunsList      = "ci.runs.list"
	MethodCIRunsGet       = "ci.runs.get"
	MethodCIRunsCancel    = "ci.runs.cancel"
	MethodCIRunsSteer     = "ci.runs.steer"
	MethodCIRunsRetry     = "ci.runs.retry"
	MethodCIJobsGet       = "ci.jobs.get"
	MethodCIJobsLogs      = "ci.jobs.logs"
	MethodCIJobsCancel    = "ci.jobs.cancel"
	MethodCIArtifactsList = "ci.artifacts.list"
	MethodCIRunnersList   = "ci.runners.list"
	MethodCICacheList     = "ci.cache.list"
	MethodCICacheClear    = "ci.cache.clear"

	MethodCIList            = "ci.list"
	MethodCIGet             = "ci.get"
	MethodCISave            = "ci.save"
	MethodCIDelete          = "ci.delete"
	MethodCIEnable          = "ci.enable"
	MethodCIDisable         = "ci.disable"
	MethodCIRun             = "ci.run"
	MethodCIValidate        = "ci.validate"
	MethodCIEvents          = "ci.events"
	MethodCIIngest          = "ci.ingest"
	MethodCIDependents      = "ci.dependents"
	MethodCISchedules       = "ci.schedules"
	MethodCICapabilities    = "ci.capabilities"
	MethodCIProviderDisable = "ci.provider.disable"

	EventCIRunCreated    = "ci.run.created"
	EventCIRunStarted    = "ci.run.started"
	EventCIRunCompleted  = "ci.run.completed"
	EventCIRunFailed     = "ci.run.failed"
	EventCIRunCancelled  = "ci.run.cancelled"
	EventCIRunWaiting    = "ci.run.waiting"
	EventCIRunBlocked    = "ci.run.blocked"
	EventCIJobQueued     = "ci.job.queued"
	EventCIJobStarted    = "ci.job.started"
	EventCIJobCompleted  = "ci.job.completed"
	EventCIJobFailed     = "ci.job.failed"
	EventCIJobCancelled  = "ci.job.cancelled"
	EventCIJobSkipped    = "ci.job.skipped"
	EventCIStepStarted   = "ci.step.started"
	EventCIStepOutput    = "ci.step.output"
	EventCIStepCompleted = "ci.step.completed"
	EventCIStepFailed    = "ci.step.failed"
	EventCIEvent         = "ci.event"
)

type TriggerDTO struct {
	ID       string `json:"id,omitempty"`
	Kind     string `json:"kind"`
	Family   string `json:"family,omitempty"`
	At       string `json:"at,omitempty"`
	Cron     string `json:"cron,omitempty"`
	Interval string `json:"interval,omitempty"`
	Timezone string `json:"timezone,omitempty"`
	Event    string `json:"event,omitempty"`
}

type StepDTO struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
	Run  string `json:"run,omitempty"`
	Uses string `json:"uses,omitempty"`
}

type JobDTO struct {
	ID    string    `json:"id"`
	Name  string    `json:"name,omitempty"`
	Needs []string  `json:"needs,omitempty"`
	Steps []StepDTO `json:"steps,omitempty"`
}

type CIWorkflowDTO struct {
	ID            string       `json:"id"`
	Name          string       `json:"name"`
	Enabled       bool         `json:"enabled"`
	Availability  string       `json:"availability,omitempty"`
	BlockedReason string       `json:"blocked_reason,omitempty"`
	Triggers      []TriggerDTO `json:"triggers,omitempty"`
	Jobs          []JobDTO     `json:"jobs,omitempty"`
	Capabilities  []string     `json:"capabilities,omitempty"`
	UpdatedAt     string       `json:"updated_at,omitempty"`
}

type CIStepDTO struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	Status    string `json:"status"`
	ExitCode  int    `json:"exit_code,omitempty"`
	Error     string `json:"error,omitempty"`
	Output    string `json:"output,omitempty"`
	StartedAt string `json:"started_at,omitempty"`
}

type CIJobDTO struct {
	ID        string      `json:"id"`
	Name      string      `json:"name,omitempty"`
	Status    string      `json:"status"`
	ExitCode  int         `json:"exit_code,omitempty"`
	Error     string      `json:"error,omitempty"`
	StartedAt string      `json:"started_at,omitempty"`
	Steps     []CIStepDTO `json:"steps,omitempty"`
}

type CIRunSummaryDTO struct {
	Success int `json:"success"`
	Failed  int `json:"failed"`
	Running int `json:"running"`
	Queued  int `json:"queued"`
	Blocked int `json:"blocked"`
	Waiting int `json:"waiting"`
}

type CIRunDTO struct {
	ID            string          `json:"id"`
	WorkflowID    string          `json:"workflow_id"`
	Name          string          `json:"name,omitempty"`
	Status        string          `json:"status"`
	BlockedReason string          `json:"blocked_reason,omitempty"`
	TriggerID     string          `json:"trigger_id,omitempty"`
	Workspace     string          `json:"workspace,omitempty"`
	RequestedBy   string          `json:"requested_by,omitempty"`
	WakeAt        string          `json:"wake_at,omitempty"`
	CreatedAt     string          `json:"created_at,omitempty"`
	FinishedAt    string          `json:"finished_at,omitempty"`
	Jobs          []CIJobDTO      `json:"jobs,omitempty"`
	Summary       CIRunSummaryDTO `json:"summary"`
}

type ValidationIssueDTO struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Level   string `json:"level,omitempty"`
}

type ValidationDTO struct {
	Verdict      string               `json:"verdict"`
	Syntax       string               `json:"syntax"`
	Capabilities string               `json:"capabilities"`
	Providers    string               `json:"providers"`
	ProviderID   string               `json:"provider,omitempty"`
	Issues       []ValidationIssueDTO `json:"issues,omitempty"`
}

type CIWorkspaceRequest struct {
	Workspace string   `json:"workspace,omitempty"`
	YAML      string   `json:"yaml,omitempty"`
	Jobs      []string `json:"jobs,omitempty"`
}

// CIRunStartRequest starts a workflow run by workflow id.
type CIRunStartRequest struct {
	ID    string `json:"id"`
	Async bool   `json:"async,omitempty"`
}

type CIRunIDRequest struct {
	ID        string `json:"id"`
	Workspace string `json:"workspace,omitempty"`
}

type CIRunSteerRequest struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type CILogsRequest struct {
	RunID string `json:"run_id"`
	JobID string `json:"job_id"`
	After uint64 `json:"after,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

type CILogsResult struct {
	Chunks []json.RawMessage `json:"chunks"`
}

type CIWorkflowSaveRequest struct {
	ID      string `json:"id,omitempty"`
	YAML    string `json:"yaml,omitempty"`
	Enabled *bool  `json:"enabled,omitempty"`
	Name    string `json:"name,omitempty"`
}

type CIWorkflowIDRequest struct {
	ID string `json:"id"`
}

type CIIngestRequest struct {
	ID         string         `json:"id,omitempty"`
	Type       string         `json:"type"`
	Source     string         `json:"source,omitempty"`
	Subject    string         `json:"subject,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

type CapabilityDTO struct {
	Capability string `json:"capability"`
	Provider   string `json:"provider"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
}

type CIWorkflowListResult struct {
	Workflows []CIWorkflowDTO `json:"workflows"`
}

type CIRunListResult struct {
	Runs []CIRunDTO `json:"runs"`
}

type ScheduleDTO struct {
	ID         string `json:"id"`
	WorkflowID string `json:"workflow_id"`
	Kind       string `json:"kind"`
	NextRunAt  string `json:"next_run_at"`
	Status     string `json:"status"`
	Timezone   string `json:"timezone,omitempty"`
}

type EventDTO struct {
	ID      string         `json:"id"`
	Type    string         `json:"type"`
	Source  string         `json:"source,omitempty"`
	Subject string         `json:"subject,omitempty"`
	Time    string         `json:"time,omitempty"`
	Attrs   map[string]any `json:"attributes,omitempty"`
}
