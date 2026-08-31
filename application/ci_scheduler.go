package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
	clock "nusashell/pkg/time"
)

const (
	jobLease     = domain.JobLease
	jobHeartbeat = domain.JobHeartbeat
	maxParallel  = domain.MaxParallelJobs
	maxFanout    = domain.MaxFanout
)

// ExecutionScheduler evaluates the DAG, matches runners, and runs jobs.
type ExecutionScheduler struct {
	Runs     PipelineRunStore
	Logs     ExecutionLogStore
	Exec     JobExecutor
	Caps     CapabilityResolver
	Agent    AgentStepRunner
	Runners  RunnerRegistry
	Waits    WaitStore
	Bus      *Bus
	Clock    Clock
	MaxJobs  int
	Notifier RunNotifier

	mu          sync.Mutex
	cancels     map[string]context.CancelFunc
	cancelOwner map[string]string
	runMu       sync.Map
}

func NewExecutionScheduler() *ExecutionScheduler {
	return &ExecutionScheduler{cancels: map[string]context.CancelFunc{}, cancelOwner: map[string]string{}, MaxJobs: maxParallel, Clock: SystemClock{}}
}

func (s *ExecutionScheduler) lockRun(id string) *sync.Mutex {
	v, _ := s.runMu.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func (s *ExecutionScheduler) now() time.Time {
	if s.Clock == nil {
		return clock.NewTime().Time()
	}
	return clock.NewTime(s.Clock.Now()).Time()
}

// StartRun persists a snapshot and begins scheduling.
func (s *ExecutionScheduler) StartRun(ctx context.Context, run *domain.WorkflowRun) error {
	run.StartRun(s.now())
	if err := s.Runs.Create(ctx, run); err != nil {
		return err
	}
	s.emit(contracts.EventCIRunCreated, map[string]any{"run_id": run.ID, "workflow_id": run.WorkflowID, "status": run.Status})
	return s.Tick(ctx, run.ID)
}

// StartRunAsync persists a snapshot, then begins scheduling in a background
// goroutine with a detached context. The caller receives the run ID
// immediately and can poll status with ci_run_status or block with ci_wait.
// Cancel still works: it sets terminal status, which the Tick loop observes
// on its next iteration.
func (s *ExecutionScheduler) StartRunAsync(ctx context.Context, run *domain.WorkflowRun) error {
	run.StartRun(s.now())
	if err := s.Runs.Create(ctx, run); err != nil {
		return err
	}
	s.emit(contracts.EventCIRunCreated, map[string]any{"run_id": run.ID, "workflow_id": run.WorkflowID, "status": run.Status})
	go func() { _ = s.Tick(context.Background(), run.ID) }()
	return nil
}

// Tick re-evaluates one run.
func (s *ExecutionScheduler) Tick(ctx context.Context, runID string) error {
	run, err := s.Runs.Get(ctx, runID)
	if err != nil {
		return err
	}
	if run.Status.IsTerminal() {
		return nil
	}
	if run.Status == domain.StatusWaiting {
		if run.WakeAt != nil && s.now().Before(*run.WakeAt) {
			return nil
		}
		domain.WakeWaitingRun(run)
		if err := s.persist(ctx, run); err != nil {
			return err
		}
	}
	if run.Status == domain.StatusBlocked {
		return nil
	}
	for {
		run, err = s.Runs.Get(ctx, runID)
		if err != nil {
			return err
		}
		if run.Status.IsTerminal() || run.Status == domain.StatusBlocked {
			return nil
		}
		dag, issues := domain.BuildDAG(run.Definition.Jobs)
		if len(issues) > 0 {
			run.FailDAG(s.now())
			_ = s.persist(ctx, run)
			s.emit(contracts.EventCIRunFailed, map[string]any{"run_id": run.ID, "error": issues[0].Message})
			s.notifyWebhook(ctx, run)
			return fmt.Errorf("%s", issues[0].Message)
		}
		status := map[string]domain.RunStatus{}
		continueOn := map[string]bool{}
		for i := range run.Jobs {
			status[run.Jobs[i].JobID] = run.Jobs[i].Status
			if j := run.Definition.JobByID(run.Jobs[i].JobID); j != nil {
				continueOn[run.Jobs[i].JobID] = j.ContinueOnError
			}
		}
		ready := domain.ReadyJobs(dag, status, continueOn)
		if len(ready) == 0 {
			return s.maybeFinalize(ctx, run)
		}
		claimed := domain.ClaimJobs(run, ready)
		if err := s.persist(ctx, run); err != nil {
			return err
		}
		if len(claimed) == 0 {
			return s.maybeFinalize(ctx, run)
		}
		n := s.MaxJobs
		if n <= 0 {
			n = maxParallel
		}
		var wg sync.WaitGroup
		sem := make(chan struct{}, n)
		for _, jobID := range claimed {
			jobID := jobID
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				_ = s.runJob(ctx, runID, jobID)
			}()
		}
		wg.Wait()
	}
}

func (s *ExecutionScheduler) runJob(ctx context.Context, runID, jobID string) error {
	run, err := s.Runs.Get(ctx, runID)
	if err != nil {
		return err
	}
	jr := run.JobRunByID(jobID)
	job := run.Definition.JobByID(jobID)
	if jr == nil || job == nil {
		return fmt.Errorf("unknown job %s", jobID)
	}
	if jr.Status != domain.StatusQueued && jr.Status != domain.StatusPending && jr.Status != domain.StatusRunning {
		return nil
	}
	ok, err := domain.EvalIf(job.If, domain.BuildConditionEnv(run))
	if err != nil {
		return s.failJob(ctx, run, jr, err.Error())
	}
	if !ok {
		jr.Skip(s.now())
		return s.persist(ctx, run)
	}
	t := s.now()
	jr.BeginRunning(t)
	run.BeginRunning(t)
	if err := s.persist(ctx, run); err != nil {
		return err
	}
	s.emit(contracts.EventCIJobStarted, map[string]any{"run_id": run.ID, "job_id": jobID})

	jobCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.cancels[jr.ID] = cancel
	s.cancelOwner[jr.ID] = runID
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.cancels, jr.ID)
		delete(s.cancelOwner, jr.ID)
		s.mu.Unlock()
	}()
	if job.Timeout > 0 {
		var timeoutCancel context.CancelFunc
		jobCtx, timeoutCancel = context.WithTimeout(jobCtx, job.Timeout)
		defer timeoutCancel()
	}

	ws, err := s.Exec.Prepare(jobCtx, PrepareRequest{Run: run, Job: *job, JobRun: jr, Workspace: run.Workspace})
	if err != nil {
		return s.failJob(ctx, run, jr, err.Error())
	}
	defer func() { _ = s.Exec.Cleanup(context.Background(), CleanupRequest{Workspace: ws}) }()

	outputs := map[string]any{}
	for i := range job.Steps {
		step := job.Steps[i]
		sr := jr.EnsureStep(step)
		if sr.Status.IsTerminal() {
			continue // finished before a wait_until pause; never re-run side effects
		}
		if step.WaitUntil != nil {
			if s.now().Before(*step.WaitUntil) {
				return s.parkWait(ctx, run, jr, sr, step)
			}
		}
		sr.BeginRunning(s.now())
		_ = s.persist(ctx, run)
		s.emit(contracts.EventCIStepStarted, map[string]any{"run_id": run.ID, "job_id": jobID, "step_id": sr.ID})

		var result StepResult
		switch {
		case step.WaitUntil != nil:
			result = StepResult{ExitCode: 0}
		case step.Uses != "":
			result, err = s.runUses(jobCtx, run, step)
		case step.Agent != nil:
			if s.Agent == nil {
				result, err = StepResult{Error: "agent steps are not configured"}, fmt.Errorf("agent steps are not configured")
				break
			}
			prompt := domain.RenderAgentPrompt(step.Agent.Prompt, run.Event)
			out, convID, agentErr := s.Agent.RunAgentStep(jobCtx, prompt, step.Agent.Model, run.Definition.Trust, step.Agent.OutputSchema)
			if agentErr != nil {
				result, err = StepResult{Error: agentErr.Error()}, agentErr
				break
			}
			sr.ConversationID = convID
			result, err = StepResult{ExitCode: 0, Outputs: out}, nil
		default:
			envMap := domain.MergeEnv(run.Definition.Env, job.Env, step.Env)
			envMap["NUSASHELL"] = "true"
			envMap["NUSASHELL_CI"] = "true"
			envMap["NUSASHELL_PIPELINE_ID"] = run.WorkflowID
			envMap["NUSASHELL_RUN_ID"] = run.ID
			envMap["NUSASHELL_JOB_ID"] = jr.JobID
			envMap["NUSASHELL_STEP_ID"] = sr.StepID
			envMap["NUSASHELL_WORKSPACE"] = ws.Dir
			result, err = s.Exec.RunStep(jobCtx, RunStepRequest{
				Run: run, Job: *job, JobRun: jr, Step: step, StepRun: sr, Workspace: ws, Env: envMap,
				OnOutput: func(chunk domain.LogChunk) {
					if s.Logs != nil {
						_ = s.Logs.Append(context.Background(), chunk)
					}
					s.emit(contracts.EventCIStepOutput, chunk)
				},
			})
		}
		ft := s.now()
		if err != nil || result.ExitCode != 0 || result.Error != "" {
			errMsg := result.Error
			if err != nil && errMsg == "" {
				errMsg = err.Error()
			}
			sr.Fail(result.ExitCode, errMsg, ft)
			return s.failJob(ctx, run, jr, sr.Error)
		}
		sr.Succeed(result.ExitCode, ft)
		if step.Agent != nil && result.Outputs != nil {
			if text, ok := result.Outputs["output"].(string); ok {
				sr.Output = text
			}
		}
		for k, v := range result.Outputs {
			outputs[k] = v
		}
		_ = s.persist(ctx, run)
		s.emit(contracts.EventCIStepCompleted, map[string]any{"run_id": run.ID, "job_id": jobID, "step_id": sr.ID})
	}
	jr.Succeed(outputs, s.now())
	if err := s.persist(ctx, run); err != nil {
		return err
	}
	s.emit(contracts.EventCIJobCompleted, map[string]any{"run_id": run.ID, "job_id": jobID})
	return nil
}

func (s *ExecutionScheduler) parkWait(ctx context.Context, run *domain.WorkflowRun, jr *domain.JobRun, sr *domain.StepRun, step domain.Step) error {
	sr.ParkWait(*step.WaitUntil)
	jr.ParkWait()
	run.ParkWait(*step.WaitUntil)
	if s.Waits != nil {
		_ = s.Waits.Put(ctx, &domain.WaitRecord{
			ID: domain.NewID(domain.IDPrefixWait), WorkflowRunID: run.ID, JobID: jr.JobID, StepID: sr.StepID,
			WakeAt: step.WaitUntil, Status: domain.SchedulePending,
		})
	}
	if err := s.persist(ctx, run); err != nil {
		return err
	}
	s.emit(contracts.EventCIRunWaiting, map[string]any{"run_id": run.ID, "wake_at": step.WaitUntil})
	return nil
}

func (s *ExecutionScheduler) runUses(ctx context.Context, run *domain.WorkflowRun, step domain.Step) (StepResult, error) {
	if s.Caps == nil {
		return StepResult{Error: "no capability resolver"}, fmt.Errorf("no capability resolver")
	}
	policy := domain.DefaultAutoStart
	for _, t := range run.Definition.Triggers {
		if t.AutoStart != "" {
			policy = t.AutoStart
			break
		}
	}
	binding, err := s.Caps.Resolve(ctx, step.Uses, policy)
	if err != nil && binding.Status == domain.CapMissing {
		return StepResult{Error: err.Error()}, err
	}
	binding, err = s.Caps.EnsureAvailable(ctx, binding, policy)
	if err != nil {
		return StepResult{Error: err.Error()}, err
	}
	avail := domain.MapAvailability(binding.Status, domain.AllowsAutoStart(binding.Status, policy, true))
	if avail == domain.AvailBlocked || avail == domain.AvailError {
		reason := fmt.Sprintf("Required capability %q is provided by %s (%s). status=%s", binding.Capability, binding.ProviderID, binding.Kind, binding.Status)
		run.ParkBlocked(reason)
		_ = s.persist(ctx, run)
		s.emit(contracts.EventCIRunBlocked, map[string]any{
			"run_id": run.ID, "capability": binding.Capability, "provider": binding.ProviderID, "status": binding.Status, "reason": binding.Reason,
		})
		return StepResult{Error: run.BlockedReason}, fmt.Errorf("%s", run.BlockedReason)
	}
	raw, _ := json.Marshal(step.With)
	out, err := s.Caps.Execute(ctx, binding, raw)
	if err != nil {
		return StepResult{Error: err.Error()}, err
	}
	var outputs map[string]any
	_ = json.Unmarshal(out, &outputs)
	return StepResult{ExitCode: 0, Outputs: outputs}, nil
}

func (s *ExecutionScheduler) failJob(ctx context.Context, run *domain.WorkflowRun, jr *domain.JobRun, reason string) error {
	jr.Fail(reason, s.now())
	dag, _ := domain.BuildDAG(run.Definition.Jobs)
	cont := false
	if j := run.Definition.JobByID(jr.JobID); j != nil {
		cont = j.ContinueOnError
	}
	status := map[string]domain.RunStatus{}
	for i := range run.Jobs {
		status[run.Jobs[i].JobID] = run.Jobs[i].Status
	}
	for _, id := range domain.BlockedByFailure(dag, jr.JobID, cont, status) {
		if dep := run.JobRunByID(id); dep != nil {
			dep.ParkBlocked("upstream failed: " + jr.JobID)
		}
	}
	if err := s.persist(ctx, run); err != nil {
		return err
	}
	s.emit(contracts.EventCIJobFailed, map[string]any{"run_id": run.ID, "job_id": jr.JobID, "error": reason})
	return nil
}

func (s *ExecutionScheduler) maybeFinalize(ctx context.Context, run *domain.WorkflowRun) error {
	sum := run.Summary()
	run.Finalize(s.now(), sum)
	if run.Status == domain.StatusFailed {
		s.emit(contracts.EventCIRunFailed, map[string]any{"run_id": run.ID})
	} else if run.Status == domain.StatusSuccess {
		s.emit(contracts.EventCIRunCompleted, map[string]any{"run_id": run.ID})
	}
	if run.Status.IsTerminal() {
		s.notifyWebhook(ctx, run)
	}
	return s.persist(ctx, run)
}

// notifyWebhook fires the workflow webhook (if configured) in a
// fire-and-forget goroutine. Errors are logged but never block the run.
func (s *ExecutionScheduler) notifyWebhook(ctx context.Context, run *domain.WorkflowRun) {
	if s.Notifier == nil || strings.TrimSpace(run.Definition.WebhookURL) == "" {
		return
	}
	url := run.Definition.WebhookURL
	go func() {
		notifyCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.Notifier.NotifyRunCompleted(notifyCtx, url, run); err != nil {
			s.emit("ci.webhook.failed", map[string]any{"run_id": run.ID, "url": url, "error": err.Error()})
		}
	}()
	_ = ctx
}

func (s *ExecutionScheduler) Cancel(ctx context.Context, runID string) error {
	run, err := s.Runs.Get(ctx, runID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	for id, cancel := range s.cancels {
		if s.cancelOwner[id] != runID {
			continue // belongs to another workflow run - leave it running
		}
		cancel()
		delete(s.cancels, id)
		delete(s.cancelOwner, id)
	}
	s.mu.Unlock()
	run.Cancel(s.now())
	_ = s.persist(ctx, run)
	s.emit(contracts.EventCIRunCancelled, map[string]any{"run_id": run.ID})
	return nil
}

func (s *ExecutionScheduler) RecoverStale(ctx context.Context) error {
	runs, err := s.Runs.List(ctx, RunFilter{})
	if err != nil {
		return err
	}
	now := s.now()
	for _, run := range runs {
		if run.Status != domain.StatusRunning {
			continue
		}
		changed := false
		for i := range run.Jobs {
			j := &run.Jobs[i]
			if j.Status == domain.StatusRunning && j.HeartbeatAt != nil && now.Sub(*j.HeartbeatAt) > time.Minute {
				j.Status = domain.StatusFailed
				j.Error = "stale lease after process restart"
				changed = true
			}
		}
		if changed {
			_ = s.persist(ctx, run)
		}
	}
	return nil
}

func (s *ExecutionScheduler) persist(ctx context.Context, run *domain.WorkflowRun) error {
	s.lockRun(run.ID).Lock()
	defer s.lockRun(run.ID).Unlock()
	cur, err := s.Runs.Get(ctx, run.ID)
	if err == nil {
		domain.MergeRun(cur, run)
		run = cur
	}
	return s.Runs.Update(ctx, run)
}

func (s *ExecutionScheduler) emit(typ string, v any) {
	if s.Bus != nil {
		s.Bus.Emit(typ, v)
	}
}

func NewWorkflowRun(def domain.WorkflowDefinition, requestedBy string) *domain.WorkflowRun {
	run := &domain.WorkflowRun{
		TaskState: domain.TaskState[domain.RunStatus]{
			ID:     domain.NewID(domain.IDPrefixRun),
			Status: domain.StatusQueued,
		},
		WorkflowID:  def.ID,
		Name:        def.Name,
		Workspace:   def.Source.Workspace,
		Definition:  def,
		RequestedBy: requestedBy,
		CreatedAt:   clock.NewTime().Time(),
	}
	if run.WorkflowID == "" {
		run.WorkflowID = "pipeline"
	}
	for _, j := range def.Jobs {
		jr := domain.JobRun{
			TaskState: domain.TaskState[domain.RunStatus]{ID: domain.NewID(domain.IDPrefixJob), Status: domain.StatusQueued},
			JobID:     j.ID,
			Name:      j.Name,
		}
		for _, st := range j.Steps {
			jr.Steps = append(jr.Steps, domain.StepRun{
				TaskState: domain.TaskState[domain.RunStatus]{ID: domain.NewID(domain.IDPrefixStep), Status: domain.StatusQueued},
				StepID:    st.ID,
				Name:      st.Name,
			})
		}
		run.Jobs = append(run.Jobs, jr)
	}
	return run
}
