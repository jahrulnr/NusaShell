package domain

import "time"

// WorkflowDefinition is the canonical automation/pipeline document.
// PipelineDefinition is a compatibility alias used by CI-oriented APIs.
type WorkflowDefinition struct {
	ID          string
	Name        string
	Version     int
	Enabled     bool
	Trust       TrustLevel
	Concurrency Concurrency
	Missed      MissedRunPolicy
	Triggers    []Trigger
	Defaults    WorkflowDefaults
	Env         map[string]string
	Jobs        []Job
	Source      WorkflowSource
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// PipelineDefinition is the CI-facing name for a workflow.
type PipelineDefinition = WorkflowDefinition

// WorkflowSource records where a definition came from.
type WorkflowSource struct {
	Kind      string // file | store | agent | ui
	Workspace string
	Path      string
}

// TrustLevel is the user-visible execution trust of a workflow.
type TrustLevel string

const (
	TrustSafe       TrustLevel = "safe"
	TrustTrusted    TrustLevel = "trusted"
	TrustPrivileged TrustLevel = "privileged"
)

// ConcurrencyPolicy controls overlapping runs of the same workflow.
type ConcurrencyPolicy string

const (
	ConcurrencyAllow   ConcurrencyPolicy = "allow"
	ConcurrencyQueue   ConcurrencyPolicy = "queue"
	ConcurrencyReplace ConcurrencyPolicy = "replace"
	ConcurrencySkip    ConcurrencyPolicy = "skip"
)

// Concurrency is the per-workflow overlap policy.
type Concurrency struct {
	Key    string
	Policy ConcurrencyPolicy
}

// MissedRunPolicy decides what happens after the process was down
// across a scheduled fire time.
type MissedRunPolicy string

const (
	MissedSkip        MissedRunPolicy = "skip_missed"
	MissedRunOnce     MissedRunPolicy = "run_once_after_restart"
	MissedCatchUpAll  MissedRunPolicy = "catch_up_all"
	MissedDefaultOnce MissedRunPolicy = "" // resolved by trigger kind
)

// WorkflowDefaults apply when a job/step omits a field.
type WorkflowDefaults struct {
	Shell   string
	Timeout time.Duration
}

// Job is one DAG node. Steps inside a job run sequentially.
type Job struct {
	ID              string
	Name            string
	Needs           []JobNeed
	If              string
	RunsOn          []string
	Env             map[string]string
	Timeout         time.Duration
	ContinueOnError bool
	Retry           RetryPolicy
	Steps           []Step
	Artifacts       ArtifactSpec
	Cache           CacheSpec
}

// JobNeed is a dependency on another job. Artifacts requests that the
// producer’s artifacts are extracted into this job’s workspace.
type JobNeed struct {
	Job       string
	Artifacts bool
}

// RetryPolicy is user-configured retry for a job. Command failure is not
// retried unless the pipeline asks for it.
type RetryPolicy struct {
	MaxAttempts int
	On          []string // runner_error | timeout
}

// ArtifactSpec describes durable job outputs.
type ArtifactSpec struct {
	Paths     []string
	Retention time.Duration
}

// CacheSpec is an optional dependency cache; never required for correctness.
type CacheSpec struct {
	Namespace string
	Paths     []string
	KeyParts  []string
}

// Step is one sequential unit inside a job.
type Step struct {
	ID        string
	Name      string
	Run       string
	Uses      string
	With      map[string]any
	WaitUntil *time.Time
	Agent     *AgentStep
	Shell     string
	Env       map[string]string
	Timeout   time.Duration
}

// AgentStep runs through the existing NusaShell agent runtime.
type AgentStep struct {
	Prompt       string
	OutputSchema map[string]any
}

// JobByID returns the job with the given id, or nil.
func (w *WorkflowDefinition) JobByID(id string) *Job {
	if w == nil {
		return nil
	}
	for i := range w.Jobs {
		if w.Jobs[i].ID == id {
			return &w.Jobs[i]
		}
	}
	return nil
}

// JobIDs returns job identifiers in definition order.
func (w *WorkflowDefinition) JobIDs() []string {
	if w == nil {
		return nil
	}
	out := make([]string, len(w.Jobs))
	for i, j := range w.Jobs {
		out[i] = j.ID
	}
	return out
}

// ReferencedCapabilities lists logical action/event names used by the
// workflow (triggers + uses steps). Definitions persist these names, not
// provider IDs.
func (w *WorkflowDefinition) ReferencedCapabilities() []string {
	if w == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	add := func(name string) {
		if name == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	for _, t := range w.Triggers {
		if t.Event != "" {
			add(t.Event)
		}
	}
	for _, j := range w.Jobs {
		for _, s := range j.Steps {
			add(s.Uses)
		}
	}
	return out
}

// DefaultConcurrency returns allow when unset.
func (c Concurrency) Normalized() Concurrency {
	if c.Policy == "" {
		c.Policy = ConcurrencyAllow
	}
	return c
}

// ResolveMissed returns the effective missed-run policy for a trigger kind.
func ResolveMissed(policy MissedRunPolicy, kind TriggerKind) MissedRunPolicy {
	if policy != "" && policy != MissedDefaultOnce {
		return policy
	}
	if kind == TriggerOnce {
		return MissedRunOnce
	}
	return MissedSkip
}
