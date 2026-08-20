package application

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"nusashell/domain"
)

// Clock is injected so schedulers are deterministic in tests.
type Clock interface {
	Now() time.Time
}

// SystemClock is the production clock.
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

// FrozenClock is a test clock.
type FrozenClock struct{ T time.Time }

func (c *FrozenClock) Now() time.Time          { return c.T }
func (c *FrozenClock) Advance(d time.Duration) { c.T = c.T.Add(d) }

// WorkflowStore persists automation definitions (not conversation JSON).
type WorkflowStore interface {
	Put(ctx context.Context, w *domain.WorkflowDefinition) error
	Get(ctx context.Context, id string) (*domain.WorkflowDefinition, error)
	List(ctx context.Context) ([]*domain.WorkflowDefinition, error)
	Delete(ctx context.Context, id string) error
}

// PipelineFileStore loads `.nusashell/pipeline.yaml` from a workspace.
type PipelineFileStore interface {
	GetDefinition(ctx context.Context, workspace string) (*domain.WorkflowDefinition, error)
}

// RunFilter lists workflow runs.
type RunFilter struct {
	WorkflowID string
	Workspace  string
	Status     domain.RunStatus
	Limit      int
}

// PipelineRunStore persists run snapshots.
type PipelineRunStore interface {
	Create(ctx context.Context, run *domain.WorkflowRun) error
	Get(ctx context.Context, id string) (*domain.WorkflowRun, error)
	List(ctx context.Context, filter RunFilter) ([]*domain.WorkflowRun, error)
	Update(ctx context.Context, run *domain.WorkflowRun) error
}

// ScheduleStore is the durable timer table.
type ScheduleStore interface {
	Put(ctx context.Context, rec *domain.ScheduleRecord) error
	Due(ctx context.Context, now time.Time, limit int) ([]*domain.ScheduleRecord, error)
	Claim(ctx context.Context, id string, now time.Time) (*domain.ScheduleRecord, error)
	List(ctx context.Context) ([]*domain.ScheduleRecord, error)
}

// EventStore persists normalized events and deliveries.
type EventStore interface {
	PutEvent(ctx context.Context, ev *domain.Event) error
	RecordDelivery(ctx context.Context, eventID, triggerID, workflowID, runID string, matchedAt time.Time) (created bool, err error)
	ListEvents(ctx context.Context, limit int) ([]*domain.Event, error)
}

// WaitStore persists wait_until / event-wait wakeups.
type WaitStore interface {
	Put(ctx context.Context, rec *domain.WaitRecord) error
	Due(ctx context.Context, now time.Time, limit int) ([]*domain.WaitRecord, error)
	Claim(ctx context.Context, id string) (*domain.WaitRecord, error)
	WaitingForEvent(ctx context.Context, eventType string) ([]*domain.WaitRecord, error)
}

// RunLockStore implements concurrency keys.
type RunLockStore interface {
	Active(ctx context.Context, key string) (runID string, ok bool, err error)
	Acquire(ctx context.Context, key, runID string) error
	Release(ctx context.Context, key, runID string) error
}

// ExecutionLogStore is append-only log chunks.
type ExecutionLogStore interface {
	Append(ctx context.Context, chunk domain.LogChunk) error
	Read(ctx context.Context, jobID string, after uint64, limit int) ([]domain.LogChunk, error)
}

// ArtifactPutRequest is a new artifact blob.
type ArtifactPutRequest struct {
	RunID   string
	JobID   string
	Name    string
	Paths   []string
	Body    io.Reader
	Expires time.Time
}

// ArtifactStore persists job outputs.
type ArtifactStore interface {
	Put(ctx context.Context, req ArtifactPutRequest) (domain.Artifact, error)
	List(ctx context.Context, runID string) ([]domain.Artifact, error)
	Open(ctx context.Context, artifactID string) (io.ReadCloser, error)
}

// CacheStore is an optional optimization.
type CacheStore interface {
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Put(ctx context.Context, key string, src io.Reader) error
	Delete(ctx context.Context, key string) error
	List(ctx context.Context) ([]domain.CacheEntry, error)
}

// RunnerRegistry matches jobs to executors.
type RunnerRegistry interface {
	List(ctx context.Context) ([]domain.Runner, error)
	Claim(ctx context.Context, labels []string) (domain.Runner, error)
	Heartbeat(ctx context.Context, runnerID string) error
	Release(ctx context.Context, runnerID string) error
}

// PrepareRequest is executor workspace setup.
type PrepareRequest struct {
	Run       *domain.WorkflowRun
	Job       domain.Job
	JobRun    *domain.JobRun
	Workspace string
}

// ExecutionWorkspace is the prepared job directory.
type ExecutionWorkspace struct {
	Dir string
}

// RunStepRequest is one step invocation.
type RunStepRequest struct {
	Run       *domain.WorkflowRun
	Job       domain.Job
	JobRun    *domain.JobRun
	Step      domain.Step
	StepRun   *domain.StepRun
	Workspace ExecutionWorkspace
	Env       map[string]string
	OnOutput  func(domain.LogChunk)
}

// StepResult is executor output.
type StepResult struct {
	ExitCode int
	Outputs  map[string]any
	Error    string
}

// CleanupRequest tears down a job workspace.
type CleanupRequest struct {
	Workspace ExecutionWorkspace
}

// JobExecutor is the runner-side port. The scheduler does not know about
// Docker, shell, or OS.
type JobExecutor interface {
	Prepare(ctx context.Context, req PrepareRequest) (ExecutionWorkspace, error)
	RunStep(ctx context.Context, req RunStepRequest) (StepResult, error)
	Cleanup(ctx context.Context, req CleanupRequest) error
}

// CapabilityResolver binds logical names to providers.
type CapabilityResolver interface {
	Resolve(ctx context.Context, name string, policy domain.AutoStartPolicy) (domain.CapabilityBinding, error)
	EnsureAvailable(ctx context.Context, binding domain.CapabilityBinding, policy domain.AutoStartPolicy) (domain.CapabilityBinding, error)
	Execute(ctx context.Context, binding domain.CapabilityBinding, input json.RawMessage) (json.RawMessage, error)
	List(ctx context.Context) []domain.CapabilityBinding
	Dependents(ctx context.Context, providerID string) ([]*domain.WorkflowDefinition, error)
	SetDisabled(ctx context.Context, providerID string, disabled bool) error
}

// MCPToolCaller is the optional MCP execution surface.
type MCPToolCaller interface {
	CallTool(ctx context.Context, serverID, toolName string, args map[string]any) (string, error)
}

// AgentStepRunner executes an agent: prompt step. Nil means the step fails.
// Returns the step outputs and the headless conversation ID (for steer).
type AgentStepRunner interface {
	RunAgentStep(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any) (map[string]any, string, error)
}

// HeadlessTurnRunner executes a full agent turn synchronously (no streaming
// UI) and returns the final assistant text as {"output": text} plus the
// conversation ID. The conversation ID lets callers steer the running turn.
type HeadlessTurnRunner interface {
	RunHeadlessTurn(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any) (map[string]any, string, error)
	SteerHeadlessTurn(conversationID, text string) error
}

// DebounceStore remembers last fire times per trigger.
type DebounceStore interface {
	Last(ctx context.Context, workflowID, triggerID string) (time.Time, bool, error)
	Touch(ctx context.Context, workflowID, triggerID string, at time.Time) error
}

// ProviderStateStore records explicit user disable of a capability provider.
type ProviderStateStore interface {
	Get(ctx context.Context, providerID string) (disabled bool, ok bool, err error)
	SetDisabled(ctx context.Context, providerID string, disabled bool) error
}

// RunNotifier sends a notification when a workflow run completes or fails.
// Implementations POST a JSON payload to an external webhook URL.
type RunNotifier interface {
	NotifyRunCompleted(ctx context.Context, url string, run *domain.WorkflowRun) error
}
