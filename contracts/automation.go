package contracts

import "encoding/json"

const (
	MethodAutomationRunsStart     = "automation.runs.start"
	MethodAutomationRunsList      = "automation.runs.list"
	MethodAutomationRunsGet       = "automation.runs.get"
	MethodAutomationRunsCancel    = "automation.runs.cancel"
	MethodAutomationRunsSteer     = "automation.runs.steer"
	MethodAutomationRunsRetry     = "automation.runs.retry"
	MethodAutomationJobsGet       = "automation.jobs.get"
	MethodAutomationJobsLogs      = "automation.jobs.logs"
	MethodAutomationJobsCancel    = "automation.jobs.cancel"
	MethodAutomationArtifactsList = "automation.artifacts.list"
	MethodAutomationRunnersList   = "automation.runners.list"
	MethodAutomationCacheList     = "automation.cache.list"
	MethodAutomationCacheClear    = "automation.cache.clear"

	MethodAutomationList            = "automation.list"
	MethodAutomationGet             = "automation.get"
	MethodAutomationSave            = "automation.save"
	MethodAutomationDelete          = "automation.delete"
	MethodAutomationEnable          = "automation.enable"
	MethodAutomationDisable         = "automation.disable"
	MethodAutomationRun             = "automation.run"
	MethodAutomationValidate        = "automation.validate"
	MethodAutomationEvents          = "automation.events"
	MethodAutomationIngest          = "automation.ingest"
	MethodAutomationDependents      = "automation.dependents"
	MethodAutomationSchedules       = "automation.schedules"
	MethodAutomationCapabilities    = "automation.capabilities"
	MethodAutomationProviderDisable = "automation.provider.disable"

	EventAutomationRunCreated    = "automation.run.created"
	EventAutomationRunStarted    = "automation.run.started"
	EventAutomationRunCompleted  = "automation.run.completed"
	EventAutomationRunFailed     = "automation.run.failed"
	EventAutomationRunCancelled  = "automation.run.cancelled"
	EventAutomationRunWaiting    = "automation.run.waiting"
	EventAutomationRunBlocked    = "automation.run.blocked"
	EventAutomationJobQueued     = "automation.job.queued"
	EventAutomationJobStarted    = "automation.job.started"
	EventAutomationJobCompleted  = "automation.job.completed"
	EventAutomationJobFailed     = "automation.job.failed"
	EventAutomationJobCancelled  = "automation.job.cancelled"
	EventAutomationJobSkipped    = "automation.job.skipped"
	EventAutomationStepStarted   = "automation.step.started"
	EventAutomationStepOutput    = "automation.step.output"
	EventAutomationStepCompleted = "automation.step.completed"
	EventAutomationStepFailed    = "automation.step.failed"
	EventAutomationEvent         = "automation.event"
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

type WorkflowDTO struct {
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

type StepRunDTO struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	Status    string `json:"status"`
	ExitCode  int    `json:"exit_code,omitempty"`
	Error     string `json:"error,omitempty"`
	Output    string `json:"output,omitempty"`
	StartedAt string `json:"started_at,omitempty"`
}

type JobRunDTO struct {
	ID        string       `json:"id"`
	Name      string       `json:"name,omitempty"`
	Status    string       `json:"status"`
	ExitCode  int          `json:"exit_code,omitempty"`
	Error     string       `json:"error,omitempty"`
	StartedAt string       `json:"started_at,omitempty"`
	Steps     []StepRunDTO `json:"steps,omitempty"`
}

type RunSummaryDTO struct {
	Success int `json:"success"`
	Failed  int `json:"failed"`
	Running int `json:"running"`
	Queued  int `json:"queued"`
	Blocked int `json:"blocked"`
	Waiting int `json:"waiting"`
}

type RunDTO struct {
	ID            string        `json:"id"`
	WorkflowID    string        `json:"workflow_id"`
	Name          string        `json:"name,omitempty"`
	Status        string        `json:"status"`
	BlockedReason string        `json:"blocked_reason,omitempty"`
	TriggerID     string        `json:"trigger_id,omitempty"`
	Workspace     string        `json:"workspace,omitempty"`
	RequestedBy   string        `json:"requested_by,omitempty"`
	WakeAt        string        `json:"wake_at,omitempty"`
	CreatedAt     string        `json:"created_at,omitempty"`
	FinishedAt    string        `json:"finished_at,omitempty"`
	Jobs          []JobRunDTO   `json:"jobs,omitempty"`
	Summary       RunSummaryDTO `json:"summary"`
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

type AutomationWorkspaceRequest struct {
	Workspace string   `json:"workspace,omitempty"`
	YAML      string   `json:"yaml,omitempty"`
	Jobs      []string `json:"jobs,omitempty"`
}

// AutomationRunStartRequest starts a workflow run by workflow id.
type AutomationRunStartRequest struct {
	ID    string `json:"id"`
	Async bool   `json:"async,omitempty"`
}

type AutomationRunIDRequest struct {
	ID        string `json:"id"`
	Workspace string `json:"workspace,omitempty"`
}

type AutomationRunSteerRequest struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type AutomationLogsRequest struct {
	RunID string `json:"run_id"`
	JobID string `json:"job_id"`
	After uint64 `json:"after,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

type AutomationLogsResult struct {
	Chunks []json.RawMessage `json:"chunks"`
}

type AutomationWorkflowSaveRequest struct {
	ID      string `json:"id,omitempty"`
	YAML    string `json:"yaml,omitempty"`
	Enabled *bool  `json:"enabled,omitempty"`
	Name    string `json:"name,omitempty"`
}

type AutomationWorkflowIDRequest struct {
	ID string `json:"id"`
}

type AutomationIngestRequest struct {
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

type WorkflowListResult struct {
	Workflows []WorkflowDTO `json:"workflows"`
}

type RunListResult struct {
	Runs []RunDTO `json:"runs"`
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
