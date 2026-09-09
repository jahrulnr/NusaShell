package domain

import (
	"strings"
	"time"
)

// WorkflowDefinition is the canonical automation/pipeline document.
// PipelineDefinition is a compatibility alias used by Automation-oriented APIs.
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
	WebhookURL  string
	// Notify optionally forwards headless agent-step lifecycle to an MCP
	// plugin (host-side; the agent never calls send_message itself). Nil or
	// empty Plugin means disabled. Allowed only on trusted/privileged workflows.
	Notify    *NotifyConfig
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NotifyDetail controls how much of an agent step's lifecycle is forwarded.
type NotifyDetail string

const (
	// NotifyDetailNone forwards only step start/end.
	NotifyDetailNone NotifyDetail = "none"
	// NotifyDetailTools forwards step boundaries plus one progress call per
	// tool-call round (default).
	NotifyDetailTools NotifyDetail = "tools"
	// NotifyDetailText forwards step boundaries plus throttled text/reasoning.
	NotifyDetailText NotifyDetail = "text"
	// NotifyDetailAll forwards tools and text/reasoning detail.
	NotifyDetailAll NotifyDetail = "all"
)

// NotifyConfig is the top-level `notify:` block on a workflow definition.
type NotifyConfig struct {
	// Plugin is the MCP plugin id that receives progress (e.g. nusashell.telegram).
	Plugin string
	// Detail selects the event detail level. Empty means tools.
	Detail NotifyDetail
	// ChatID is an optional ${event.*} template for the delivery target.
	// When empty or unresolved, the forwarder skips delivery.
	ChatID string
}

// NormalizedDetail returns the effective notify detail (default tools).
func (n *NotifyConfig) NormalizedDetail() NotifyDetail {
	if n == nil {
		return NotifyDetailTools
	}
	switch n.Detail {
	case NotifyDetailNone, NotifyDetailTools, NotifyDetailText, NotifyDetailAll:
		return n.Detail
	case "":
		return NotifyDetailTools
	default:
		return n.Detail
	}
}

// Enabled reports whether notify is configured with a plugin target.
func (n *NotifyConfig) Enabled() bool {
	return n != nil && strings.TrimSpace(n.Plugin) != ""
}

// PipelineDefinition is the Automation-facing name for a workflow.
type PipelineDefinition = WorkflowDefinition

// WorkflowSource records where a definition came from.
type WorkflowSource struct {
	Kind       string // file | store | agent | ui
	Workspace  string
	Path       string
	ParseError string // unparseable YAML; workflow is listed as invalid
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
	// Model is an optional "provider_id:model_id" or bare model ID. When
	// empty, the Settings "Internal delegate model" is used; if that is also
	// unset, the first enabled provider's first model is used.
	Model string
	// Reuse keeps one agent conversation across workflow runs instead of
	// starting a fresh hidden conversation for every step. false (default)
	// preserves the one-shot behavior: every run starts a new transcript.
	Reuse bool
	// Conversation is an optional ${event.<key>} template naming the reused
	// conversation (rendered and sanitized via RenderConversationKey). When
	// Reuse is true and this is empty, the workflow ID is the key (one
	// conversation for the whole workflow). Ignored when Reuse is false.
	Conversation string
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

// ReferencedCapabilities lists logical action names used by `uses:` steps.
// Trigger events are intentionally excluded — they are event types (e.g.
// "telegram.message"), not capabilities that providers resolve.
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
	// Only `uses:` steps reference capabilities. Trigger events are event
	// types (e.g. "telegram.message", "automation.run.completed"), not capabilities
	// a plugin provides — including them here makes validation fail with
	// unknown_capability for valid when-triggers.
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
