package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
)

// YAMLParser decodes a workflow document.
type YAMLParser func([]byte) (*domain.WorkflowDefinition, error)

// Automation is the application facade for CI + automation.
type Automation struct {
	ParseYAML YAMLParser
	Pipelines PipelineDiscoverer
	Workflows WorkflowStore
	Runs      PipelineRunStore
	Schedules ScheduleStore
	Events    EventStore
	Exec      *ExecutionScheduler
	Auto      *AutomationScheduler
	Caps      *CapabilityRegistry
	Logs      ExecutionLogStore
	Clock     Clock
}

func (a *Automation) now() time.Time {
	if a.Clock == nil {
		return time.Now()
	}
	return a.Clock.Now()
}

func (a *Automation) ValidateYAML(raw []byte) (domain.ValidationResult, *domain.WorkflowDefinition) {
	if a.ParseYAML == nil {
		r := domain.NewValidationResult()
		r.Add(domain.ValidationIssue{Path: "", Code: "yaml", Message: "yaml parser not configured", Level: domain.ValidationSyntax})
		return r, nil
	}
	w, err := a.ParseYAML(raw)
	if err != nil {
		r := domain.NewValidationResult()
		r.Add(domain.ValidationIssue{Path: "", Code: "yaml", Message: err.Error(), Level: domain.ValidationSyntax})
		return r, nil
	}
	if a.Auto != nil {
		return a.Auto.Validate(context.Background(), w), w
	}
	return domain.ValidateSyntax(w), w
}

func (a *Automation) SaveWorkflow(ctx context.Context, w *domain.WorkflowDefinition) (*domain.WorkflowDefinition, domain.ValidationResult, error) {
	if w.ID == "" {
		w.ID = domain.NewID("wf")
	}
	if w.CreatedAt.IsZero() {
		w.CreatedAt = a.now().UTC()
	}
	w.UpdatedAt = a.now().UTC()
	r := domain.ValidateSyntax(w)
	if a.Auto != nil {
		r = a.Auto.Validate(ctx, w)
	}
	if r.Verdict() == "INVALID" {
		return w, r, fmt.Errorf("invalid workflow")
	}
	if err := a.Workflows.Put(ctx, w); err != nil {
		return w, r, err
	}
	if w.Enabled && a.Auto != nil {
		_ = a.Auto.EnableWorkflow(ctx, w)
	}
	return w, r, nil
}

func (a *Automation) RunWorkflow(ctx context.Context, id, requestedBy string) (*domain.WorkflowRun, error) {
	return a.runWorkflow(ctx, id, requestedBy, false)
}

// RunWorkflowAsync is like RunWorkflow but schedules in the background.
func (a *Automation) RunWorkflowAsync(ctx context.Context, id, requestedBy string) (*domain.WorkflowRun, error) {
	return a.runWorkflow(ctx, id, requestedBy, true)
}

func (a *Automation) runWorkflow(ctx context.Context, id, requestedBy string, async bool) (*domain.WorkflowRun, error) {
	w, err := a.Workflows.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	run := NewWorkflowRun(*w, requestedBy)
	var startErr error
	if async {
		startErr = a.Exec.StartRunAsync(ctx, run)
	} else {
		startErr = a.Exec.StartRun(ctx, run)
	}
	if startErr != nil {
		return run, startErr
	}
	return a.Exec.Runs.Get(ctx, run.ID)
}

// WaitRun blocks until the run reaches a terminal state or the timeout
// expires. Returns the latest run snapshot. A non-positive timeout means
// poll once and return immediately.
func (a *Automation) WaitRun(ctx context.Context, runID string, timeout time.Duration) (*domain.WorkflowRun, error) {
	deadline := time.Now().Add(timeout)
	for {
		run, err := a.Exec.Runs.Get(ctx, runID)
		if err != nil {
			return nil, err
		}
		if run.Status.IsTerminal() {
			return run, nil
		}
		if timeout > 0 && time.Now().After(deadline) {
			return run, nil
		}
		select {
		case <-ctx.Done():
			return run, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func workflowDTO(w *domain.WorkflowDefinition, avail string, reason string) contracts.AutomationDTO {
	trigs := make([]contracts.TriggerDTO, 0, len(w.Triggers))
	for _, t := range w.Triggers {
		td := contracts.TriggerDTO{
			ID: t.ID, Kind: string(t.Kind), Family: string(t.Family),
			Event: t.Event, Cron: t.Cron, Timezone: t.Timezone, Interval: t.Interval.String(),
		}
		if t.At != nil {
			td.At = t.At.Format(time.RFC3339)
		}
		trigs = append(trigs, td)
	}
	jobs := make([]contracts.JobDTO, 0, len(w.Jobs))
	for _, j := range w.Jobs {
		needs := make([]string, 0, len(j.Needs))
		for _, n := range j.Needs {
			needs = append(needs, n.Job)
		}
		steps := make([]contracts.StepDTO, 0, len(j.Steps))
		for _, s := range j.Steps {
			steps = append(steps, contracts.StepDTO{ID: s.ID, Name: s.Name, Run: s.Run, Uses: s.Uses})
		}
		jobs = append(jobs, contracts.JobDTO{ID: j.ID, Name: j.Name, Needs: needs, Steps: steps})
	}
	caps := w.ReferencedCapabilities()
	return contracts.AutomationDTO{
		ID: w.ID, Name: w.Name, Enabled: w.Enabled, Availability: avail,
		BlockedReason: reason, Triggers: trigs, Jobs: jobs, Capabilities: caps,
		UpdatedAt: w.UpdatedAt.Format(time.RFC3339),
	}
}

func runDTO(r *domain.WorkflowRun) contracts.CIRunDTO {
	jobs := make([]contracts.CIJobDTO, 0, len(r.Jobs))
	for _, j := range r.Jobs {
		steps := make([]contracts.CIStepDTO, 0, len(j.Steps))
		for _, s := range j.Steps {
			st := contracts.CIStepDTO{ID: s.ID, Name: s.Name, Status: string(s.Status), ExitCode: s.ExitCode, Error: s.Error, Output: s.Output}
			if s.StartedAt != nil {
				st.StartedAt = s.StartedAt.Format(time.RFC3339)
			}
			steps = append(steps, st)
		}
		jd := contracts.CIJobDTO{ID: j.JobID, Name: j.Name, Status: string(j.Status), ExitCode: j.ExitCode, Error: j.FailureReason, Steps: steps}
		if j.StartedAt != nil {
			jd.StartedAt = j.StartedAt.Format(time.RFC3339)
		}
		jobs = append(jobs, jd)
	}
	sum := r.Summary()
	dto := contracts.CIRunDTO{
		ID: r.ID, WorkflowID: r.WorkflowID, Name: r.Name, Status: string(r.Status),
		BlockedReason: r.BlockedReason, TriggerID: r.TriggerID, Workspace: r.Workspace,
		RequestedBy: r.RequestedBy, Jobs: jobs,
		Summary: contracts.CIRunSummaryDTO{
			Success: sum.Success, Failed: sum.Failed, Running: sum.Running, Queued: sum.Queued,
			Blocked: sum.Blocked, Waiting: sum.Waiting,
		},
		CreatedAt: r.CreatedAt.Format(time.RFC3339),
	}
	if r.WakeAt != nil {
		dto.WakeAt = r.WakeAt.Format(time.RFC3339)
	}
	if r.FinishedAt != nil {
		dto.FinishedAt = r.FinishedAt.Format(time.RFC3339)
	}
	return dto
}

func validationDTO(r domain.ValidationResult) contracts.ValidationDTO {
	issues := make([]contracts.ValidationIssueDTO, 0, len(r.Issues))
	for _, i := range r.Issues {
		issues = append(issues, contracts.ValidationIssueDTO{Path: i.Path, Code: i.Code, Message: i.Message, Level: string(i.Level)})
	}
	return contracts.ValidationDTO{
		Verdict: r.Verdict(), Syntax: r.Syntax, Capabilities: r.Capabilities, Providers: r.Providers,
		ProviderID: r.ProviderID, Issues: issues,
	}
}

func (a *Automation) AvailabilityOf(ctx context.Context, w *domain.WorkflowDefinition) (string, string) {
	if a.Auto == nil {
		return "runnable", ""
	}
	r := a.Auto.Validate(ctx, w)
	switch r.Verdict() {
	case "BLOCKED":
		msg := ""
		if len(r.Issues) > 0 {
			msg = r.Issues[len(r.Issues)-1].Message
		}
		return "blocked", msg
	case "INVALID":
		return "invalid", ""
	default:
		if !w.Enabled {
			return "disabled", ""
		}
		return "runnable", ""
	}
}

func (a *Automation) ParseDefinition(src string) (*domain.WorkflowDefinition, error) {
	src = strings.TrimSpace(src)
	if src == "" {
		return nil, fmt.Errorf("definition is required")
	}
	if strings.HasPrefix(src, "{") {
		var w domain.WorkflowDefinition
		if err := json.Unmarshal([]byte(src), &w); err != nil {
			return nil, err
		}
		return &w, nil
	}
	return a.ParseYAML([]byte(src))
}

// DiscoverPipelines scans the pipeline directory for YAML files, upserts
// each into the WorkflowStore, and registers triggers with the scheduler.
// It is idempotent: running it twice does not duplicate workflows or
// schedules. Call once on boot after wiring is complete.
func (a *Automation) DiscoverPipelines(ctx context.Context) ([]*domain.WorkflowDefinition, error) {
	if a.Pipelines == nil {
		return nil, nil
	}
	defs, err := a.Pipelines.Discover()
	if err != nil {
		return nil, fmt.Errorf("discover pipelines: %w", err)
	}
	loaded := make([]*domain.WorkflowDefinition, 0, len(defs))
	for _, w := range defs {
		w.Enabled = true
		if err := a.Workflows.Put(ctx, w); err != nil {
			continue
		}
		if a.Auto != nil {
			_ = a.Auto.EnableWorkflow(ctx, w)
		}
		loaded = append(loaded, w)
	}
	return loaded, nil
}
