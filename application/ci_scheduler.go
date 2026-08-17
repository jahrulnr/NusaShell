package application

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
)

const (
	jobLease     = 30 * time.Second
	jobHeartbeat = 10 * time.Second
	maxParallel  = 4
	maxFanout    = 32
)

// ExecutionScheduler evaluates the DAG, matches runners, and runs jobs.
type ExecutionScheduler struct {
	Runs    PipelineRunStore
	Logs    ExecutionLogStore
	Exec    JobExecutor
	Caps    CapabilityResolver
	Agent   AgentStepRunner
	Runners RunnerRegistry
	Waits   WaitStore
	Bus     *Bus
	Clock   Clock
	MaxJobs int

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
	runMu   sync.Map
}

func NewExecutionScheduler() *ExecutionScheduler {
	return &ExecutionScheduler{cancels: map[string]context.CancelFunc{}, MaxJobs: maxParallel, Clock: SystemClock{}}
}

func (s *ExecutionScheduler) lockRun(id string) *sync.Mutex {
	v, _ := s.runMu.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func (s *ExecutionScheduler) now() time.Time {
	if s.Clock == nil {
		return time.Now()
	}
	return s.Clock.Now()
}

// StartRun persists a snapshot and begins scheduling.
func (s *ExecutionScheduler) StartRun(ctx context.Context, run *domain.WorkflowRun) error {
	if run.Status == "" {
		run.Status = domain.StatusQueued
	}
	t := s.now().UTC()
	run.CreatedAt = t
	if err := s.Runs.Create(ctx, run); err != nil {
		return err
	}
	s.emit(contracts.EventCIRunCreated, map[string]any{"run_id": run.ID, "workflow_id": run.WorkflowID, "status": run.Status})
	return s.Tick(ctx, run.ID)
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
		run.Status = domain.StatusQueued
		run.WakeAt = nil
		for i := range run.Jobs {
			if run.Jobs[i].Status == domain.StatusWaiting {
				run.Jobs[i].Status = domain.StatusQueued
				for j := range run.Jobs[i].Steps {
					if run.Jobs[i].Steps[j].Status == domain.StatusWaiting {
						run.Jobs[i].Steps[j].Status = domain.StatusQueued
					}
				}
			}
		}
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
			return s.failRun(ctx, run, issues[0].Message)
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
		claimed := s.claimJobs(run, ready)
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
	env := conditionEnv(run)
	ok, err := domain.EvalIf(job.If, env)
	if err != nil {
		return s.failJob(ctx, run, jr, err.Error())
	}
	if !ok {
		jr.Status = domain.StatusSkipped
		t := s.now().UTC()
		jr.FinishedAt = &t
		return s.persist(ctx, run)
	}
	jr.Status = domain.StatusRunning
	t := s.now().UTC()
	jr.StartedAt = &t
	if run.Status != domain.StatusWaiting {
		run.Status = domain.StatusRunning
		if run.StartedAt == nil {
			run.StartedAt = &t
		}
	}
	if err := s.persist(ctx, run); err != nil {
		return err
	}
	s.emit(contracts.EventCIJobStarted, map[string]any{"run_id": run.ID, "job_id": jobID})

	jobCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.cancels[jr.ID] = cancel
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.cancels, jr.ID)
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
		if i >= len(jr.Steps) {
			jr.Steps = append(jr.Steps, domain.StepRun{ID: domain.NewID("step"), StepID: step.ID, Name: step.Name, Status: domain.StatusQueued})
		}
		sr := &jr.Steps[i]
		if step.WaitUntil != nil {
			if s.now().Before(*step.WaitUntil) {
				return s.parkWait(ctx, run, jr, sr, step)
			}
		}
		st := s.now().UTC()
		sr.Status = domain.StatusRunning
		sr.StartedAt = &st
		_ = s.persist(ctx, run)
		s.emit(contracts.EventCIStepStarted, map[string]any{"run_id": run.ID, "job_id": jobID, "step_id": sr.ID})

		var result StepResult
		switch {
		case step.WaitUntil != nil:
			result = StepResult{ExitCode: 0}
		case step.Uses != "":
			result, err = s.runUses(jobCtx, run, step)
		case step.Agent != nil:
			result, err = s.runAgent(jobCtx, step)
		default:
			envMap := mergeEnv(run.Definition.Env, job.Env, step.Env)
			for k, v := range ciEnv(run, *jr, *sr, ws.Dir) {
				envMap[k] = v
			}
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
		ft := s.now().UTC()
		sr.FinishedAt = &ft
		if err != nil || result.ExitCode != 0 || result.Error != "" {
			sr.Status = domain.StatusFailed
			sr.ExitCode = result.ExitCode
			sr.Error = result.Error
			if err != nil && sr.Error == "" {
				sr.Error = err.Error()
			}
			return s.failJob(ctx, run, jr, sr.Error)
		}
		sr.Status = domain.StatusSuccess
		sr.ExitCode = result.ExitCode
		for k, v := range result.Outputs {
			outputs[k] = v
		}
		_ = s.persist(ctx, run)
		s.emit(contracts.EventCIStepCompleted, map[string]any{"run_id": run.ID, "job_id": jobID, "step_id": sr.ID})
	}
	jr.Status = domain.StatusSuccess
	jr.Outputs = outputs
	done := s.now().UTC()
	jr.FinishedAt = &done
	if err := s.persist(ctx, run); err != nil {
		return err
	}
	s.emit(contracts.EventCIJobCompleted, map[string]any{"run_id": run.ID, "job_id": jobID})
	return nil
}

func (s *ExecutionScheduler) parkWait(ctx context.Context, run *domain.WorkflowRun, jr *domain.JobRun, sr *domain.StepRun, step domain.Step) error {
	sr.Status = domain.StatusWaiting
	sr.WakeAt = step.WaitUntil
	jr.Status = domain.StatusWaiting
	run.Status = domain.StatusWaiting
	run.WakeAt = step.WaitUntil
	if s.Waits != nil {
		_ = s.Waits.Put(ctx, &domain.WaitRecord{
			ID: domain.NewID("wait"), WorkflowRunID: run.ID, JobID: jr.JobID, StepID: sr.StepID,
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
		run.Status = domain.StatusBlocked
		run.BlockedReason = fmt.Sprintf("Required capability %q is provided by %s (%s). status=%s", binding.Capability, binding.ProviderID, binding.Kind, binding.Status)
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

func (s *ExecutionScheduler) runAgent(ctx context.Context, step domain.Step) (StepResult, error) {
	if s.Agent == nil {
		return StepResult{Error: "agent steps are not configured"}, fmt.Errorf("agent steps are not configured")
	}
	out, err := s.Agent.RunAgentStep(ctx, step.Agent.Prompt, step.Agent.OutputSchema)
	if err != nil {
		return StepResult{Error: err.Error()}, err
	}
	return StepResult{ExitCode: 0, Outputs: out}, nil
}

func (s *ExecutionScheduler) failJob(ctx context.Context, run *domain.WorkflowRun, jr *domain.JobRun, reason string) error {
	jr.Status = domain.StatusFailed
	jr.FailureReason = reason
	t := s.now().UTC()
	jr.FinishedAt = &t
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
			dep.Status = domain.StatusBlocked
			dep.BlockedReason = "upstream failed: " + jr.JobID
		}
	}
	if err := s.persist(ctx, run); err != nil {
		return err
	}
	s.emit(contracts.EventCIJobFailed, map[string]any{"run_id": run.ID, "job_id": jr.JobID, "error": reason})
	return nil
}

func (s *ExecutionScheduler) failRun(ctx context.Context, run *domain.WorkflowRun, reason string) error {
	run.Status = domain.StatusFailed
	t := s.now().UTC()
	run.FinishedAt = &t
	_ = s.persist(ctx, run)
	s.emit(contracts.EventCIRunFailed, map[string]any{"run_id": run.ID, "error": reason})
	return fmt.Errorf("%s", reason)
}

func (s *ExecutionScheduler) maybeFinalize(ctx context.Context, run *domain.WorkflowRun) error {
	if run.Status == domain.StatusWaiting || run.Status == domain.StatusBlocked {
		return s.persist(ctx, run)
	}
	sum := run.Summary()
	if sum.Running > 0 || sum.Queued > 0 || sum.Waiting > 0 {
		return s.persist(ctx, run)
	}
	t := s.now().UTC()
	run.FinishedAt = &t
	if sum.Failed > 0 {
		run.Status = domain.StatusFailed
		s.emit(contracts.EventCIRunFailed, map[string]any{"run_id": run.ID})
	} else if sum.Blocked > 0 && sum.Success+sum.Skipped < sum.Total {
		run.Status = domain.StatusFailed
		s.emit(contracts.EventCIRunFailed, map[string]any{"run_id": run.ID})
	} else {
		run.Status = domain.StatusSuccess
		s.emit(contracts.EventCIRunCompleted, map[string]any{"run_id": run.ID})
	}
	return s.persist(ctx, run)
}

func (s *ExecutionScheduler) Cancel(ctx context.Context, runID string) error {
	run, err := s.Runs.Get(ctx, runID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	for _, cancel := range s.cancels {
		cancel()
	}
	s.mu.Unlock()
	run.Status = domain.StatusCancelled
	t := s.now().UTC()
	run.FinishedAt = &t
	for i := range run.Jobs {
		if run.Jobs[i].Status.IsActive() {
			run.Jobs[i].Status = domain.StatusCancelled
			run.Jobs[i].FinishedAt = &t
		}
	}
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
				j.FailureReason = "stale lease after process restart"
				changed = true
			}
		}
		if changed {
			_ = s.persist(ctx, run)
		}
	}
	return nil
}

func (s *ExecutionScheduler) claimJobs(run *domain.WorkflowRun, ready []string) []string {
	var claimed []string
	for _, jobID := range ready {
		jr := run.JobRunByID(jobID)
		if jr == nil {
			continue
		}
		if jr.Status == domain.StatusQueued || jr.Status == domain.StatusPending {
			jr.Status = domain.StatusRunning
			claimed = append(claimed, jobID)
		}
	}
	return claimed
}

func (s *ExecutionScheduler) persist(ctx context.Context, run *domain.WorkflowRun) error {
	s.lockRun(run.ID).Lock()
	defer s.lockRun(run.ID).Unlock()
	cur, err := s.Runs.Get(ctx, run.ID)
	if err == nil {
		mergeRun(cur, run)
		run = cur
	}
	return s.Runs.Update(ctx, run)
}

func mergeRun(dst, src *domain.WorkflowRun) {
	dst.Status = mergeStatus(dst.Status, src.Status)
	if src.BlockedReason != "" {
		dst.BlockedReason = src.BlockedReason
	}
	if src.StartedAt != nil {
		dst.StartedAt = src.StartedAt
	}
	if src.FinishedAt != nil {
		dst.FinishedAt = src.FinishedAt
	}
	if src.Status == domain.StatusWaiting {
		dst.WakeAt = src.WakeAt
	} else if dst.Status != domain.StatusWaiting {
		dst.WakeAt = src.WakeAt
	}
	for i := range src.Jobs {
		sj := src.Jobs[i]
		found := false
		for j := range dst.Jobs {
			if dst.Jobs[j].JobID == sj.JobID {
				dst.Jobs[j] = mergeJobRun(dst.Jobs[j], sj)
				found = true
				break
			}
		}
		if !found {
			dst.Jobs = append(dst.Jobs, sj)
		}
	}
}

func mergeStatus(dst, src domain.RunStatus) domain.RunStatus {
	if dst == domain.StatusWaiting && (src == domain.StatusQueued || src == domain.StatusPending || src == domain.StatusRunning) {
		return src
	}
	if statusRank(src) >= statusRank(dst) {
		return src
	}
	return dst
}

func mergeJobRun(dst, src domain.JobRun) domain.JobRun {
	if dst.Status == domain.StatusWaiting && (src.Status == domain.StatusQueued || src.Status == domain.StatusPending || src.Status == domain.StatusRunning) {
		return src
	}
	sr, dr := statusRank(src.Status), statusRank(dst.Status)
	if sr > dr {
		return src
	}
	if sr < dr {
		return dst
	}
	if finishedSteps(src) >= finishedSteps(dst) {
		return src
	}
	return dst
}

func statusRank(s domain.RunStatus) int {
	switch s {
	case domain.StatusPending, domain.StatusQueued:
		return 1
	case domain.StatusRunning:
		return 2
	case domain.StatusWaiting:
		return 3
	case domain.StatusBlocked, domain.StatusSkipped:
		return 4
	case domain.StatusSuccess, domain.StatusFailed, domain.StatusCancelled, domain.StatusExpired:
		return 5
	default:
		return 0
	}
}

func finishedSteps(j domain.JobRun) int {
	n := 0
	for _, s := range j.Steps {
		if s.Status.IsTerminal() {
			n++
		}
	}
	return n
}

func (s *ExecutionScheduler) emit(typ string, v any) {
	if s.Bus != nil {
		s.Bus.Emit(typ, v)
	}
}

func NewWorkflowRun(def domain.WorkflowDefinition, requestedBy string) *domain.WorkflowRun {
	run := &domain.WorkflowRun{
		ID:          domain.NewID("run"),
		WorkflowID:  def.ID,
		Name:        def.Name,
		Status:      domain.StatusQueued,
		Workspace:   def.Source.Workspace,
		Definition:  def,
		RequestedBy: requestedBy,
		CreatedAt:   time.Now().UTC(),
	}
	if run.WorkflowID == "" {
		run.WorkflowID = "pipeline"
	}
	for _, j := range def.Jobs {
		jr := domain.JobRun{ID: domain.NewID("job"), JobID: j.ID, Name: j.Name, Status: domain.StatusQueued}
		for _, st := range j.Steps {
			jr.Steps = append(jr.Steps, domain.StepRun{ID: domain.NewID("step"), StepID: st.ID, Name: st.Name, Status: domain.StatusQueued})
		}
		run.Jobs = append(run.Jobs, jr)
	}
	return run
}

func conditionEnv(run *domain.WorkflowRun) domain.ConditionEnv {
	jobs := map[string]domain.JobRun{}
	outputs := map[string]any{}
	for _, j := range run.Jobs {
		jobs[j.JobID] = j
		for k, v := range j.Outputs {
			outputs[j.JobID+"."+k] = v
		}
	}
	return domain.ConditionEnv{Jobs: jobs, Outputs: outputs}
}

func mergeEnv(layers ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, layer := range layers {
		for k, v := range layer {
			out[k] = v
		}
	}
	return out
}

func ciEnv(run *domain.WorkflowRun, job domain.JobRun, step domain.StepRun, workspace string) map[string]string {
	return map[string]string{
		"NUSASHELL":             "true",
		"NUSASHELL_CI":          "true",
		"NUSASHELL_PIPELINE_ID": run.WorkflowID,
		"NUSASHELL_RUN_ID":      run.ID,
		"NUSASHELL_JOB_ID":      job.JobID,
		"NUSASHELL_STEP_ID":     step.StepID,
		"NUSASHELL_WORKSPACE":   workspace,
	}
}
